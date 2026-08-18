package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Server holds the dependencies needed to serve the decryptor HTTP API.
type Server struct {
	ConfigPath     string
	MaxUploadBytes int64
	Decryptor      Decryptor
	Logger         *slog.Logger

	// LoadConfig is overridable in tests; defaults to the package-level
	// LoadConfig which reads from disk.
	LoadConfig func(path string) (PatternConfig, error)
}

// NewServer builds a Server with sane defaults filled in.
func NewServer(configPath string, maxUploadBytes int64, dec Decryptor, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		ConfigPath:     configPath,
		MaxUploadBytes: maxUploadBytes,
		Decryptor:      dec,
		Logger:         logger,
		LoadConfig:     LoadConfig,
	}
}

// Routes returns the HTTP handler with all routes registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /decrypt", s.handleDecrypt)
	return mux
}

type healthResponse struct {
	Status string `json:"status"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

type errorResponse struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string, details ...string) {
	writeJSON(w, status, errorResponse{Error: message, Details: details})
}

// multipart overhead budget (boundaries, headers, the identifiers field)
// added on top of the configured max PDF size when bounding the overall
// request body.
const requestOverheadBytes = 1 << 20 // 1 MiB

// maxIdentifiersBytes bounds how much of the "identifiers" part we will
// buffer in memory; it only ever needs to hold a small JSON array of
// strings.
const maxIdentifiersBytes = 1 << 20 // 1 MiB

func (s *Server) handleDecrypt(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logger := s.Logger

	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUploadBytes+requestOverheadBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		logger.Warn("request received", "error", "invalid multipart body")
		writeError(w, http.StatusBadRequest, "invalid multipart/form-data request")
		return
	}

	workDir, err := os.MkdirTemp("", "document-decryptor-*")
	if err != nil {
		logger.Error("failed to create temp working directory", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer os.RemoveAll(workDir)

	var (
		identifiers []string
		haveIDs     bool
		filename    string
		inputPath   string
		haveFile    bool
		oversized   bool
	)

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if isRequestTooLarge(err) {
				writeError(w, http.StatusRequestEntityTooLarge, "uploaded file too large")
				return
			}
			logger.Warn("request received", "error", "malformed multipart body")
			writeError(w, http.StatusBadRequest, "malformed multipart/form-data request")
			return
		}

		switch part.FormName() {
		case "identifiers":
			data, err := io.ReadAll(io.LimitReader(part, maxIdentifiersBytes+1))
			part.Close()
			if err != nil {
				logger.Warn("request received", "error", "failed to read identifiers field")
				writeError(w, http.StatusBadRequest, "failed to read identifiers field")
				return
			}
			if int64(len(data)) > maxIdentifiersBytes {
				writeError(w, http.StatusBadRequest, "identifiers field too large")
				return
			}
			if err := json.Unmarshal(data, &identifiers); err != nil {
				logger.Warn("request received", "error", "identifiers is not valid JSON")
				writeError(w, http.StatusBadRequest, "identifiers must be a JSON-encoded array of strings")
				return
			}
			haveIDs = true

		case "file":
			filename = filepath.Base(part.FileName())
			if filename == "" || filename == "." || filename == string(filepath.Separator) {
				filename = "decrypted.pdf"
			}
			inputPath = filepath.Join(workDir, "input.pdf")

			out, err := os.Create(inputPath)
			if err != nil {
				part.Close()
				logger.Error("failed to create temp input file", "error", err)
				writeError(w, http.StatusInternalServerError, "internal error")
				return
			}

			n, copyErr := io.Copy(out, io.LimitReader(part, s.MaxUploadBytes+1))
			closeErr := out.Close()
			part.Close()

			if copyErr != nil {
				if isRequestTooLarge(copyErr) {
					writeError(w, http.StatusRequestEntityTooLarge, "uploaded file too large")
					return
				}
				logger.Warn("request received", "error", "failed to read uploaded file")
				writeError(w, http.StatusBadRequest, "failed to read uploaded file")
				return
			}
			if closeErr != nil {
				logger.Error("failed to finalize temp input file", "error", closeErr)
				writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if n > s.MaxUploadBytes {
				oversized = true
			}
			haveFile = true

		default:
			// Ignore unknown fields.
			io.Copy(io.Discard, io.LimitReader(part, maxIdentifiersBytes))
			part.Close()
		}
	}

	if oversized {
		writeError(w, http.StatusRequestEntityTooLarge, "uploaded file too large")
		return
	}
	if !haveIDs {
		writeError(w, http.StatusBadRequest, "missing identifiers field")
		return
	}
	if len(identifiers) == 0 {
		writeError(w, http.StatusBadRequest, "identifiers must be a non-empty JSON array of strings")
		return
	}
	if !haveFile {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}

	logger.Info("request received", "filename", filename, "identifier_count", len(identifiers))

	header := make([]byte, len(PDFMagic))
	f, err := os.Open(inputPath)
	if err != nil {
		logger.Error("failed to reopen input file", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := io.ReadFull(f, header)
	f.Close()
	if n < len(header) || !LooksLikePDF(header) {
		logger.Warn("rejected upload: not a PDF", "filename", filename)
		writeError(w, http.StatusUnsupportedMediaType, "uploaded file is not a PDF")
		return
	}

	encrypted, err := s.Decryptor.IsEncrypted(r.Context(), inputPath)
	switch {
	case errors.Is(err, ErrDecryptTimeout):
		logger.Error("request finished", "filename", filename, "result", "encryption check timeout", "duration_ms", time.Since(start).Milliseconds())
		writeError(w, http.StatusGatewayTimeout, "checking PDF encryption status timed out")
		return
	case err != nil:
		logger.Error("request finished", "filename", filename, "error", err, "result", "encryption check failed", "duration_ms", time.Since(start).Milliseconds())
		writeError(w, http.StatusUnprocessableEntity, "unable to inspect uploaded PDF")
		return
	}

	if !encrypted {
		// Already-decrypted input: skip pattern matching entirely and echo
		// the uploaded PDF straight back.
		info, statErr := os.Stat(inputPath)
		if statErr != nil {
			logger.Error("failed to stat input file", "error", statErr)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out, openErr := os.Open(inputPath)
		if openErr != nil {
			logger.Error("failed to reopen input file", "error", openErr)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		defer out.Close()

		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		w.Header().Set("X-Document-Encrypted", "false")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		w.WriteHeader(http.StatusOK)
		io.Copy(w, out)

		logger.Info("request finished", "filename", filename, "result", "echo (already unencrypted)", "duration_ms", time.Since(start).Milliseconds())
		return
	}

	cfg, err := s.LoadConfig(s.ConfigPath)
	if err != nil {
		// Never leak config contents or filesystem details to the caller.
		logger.Error("failed to load configuration", "error", err)
		writeError(w, http.StatusInternalServerError, "configuration error")
		return
	}

	matches := FindMatches(cfg, identifiers)
	switch {
	case len(matches) == 0:
		logger.Info("request finished", "filename", filename, "result", "no match", "duration_ms", time.Since(start).Milliseconds())
		writeError(w, http.StatusNotFound, "no configured profile matched the supplied identifiers")
		return
	case len(matches) > 1:
		profiles := make([]string, len(matches))
		for i, m := range matches {
			profiles[i] = m.ProfileName
		}
		logger.Warn("request finished", "filename", filename, "result", "multiple matches", "profiles", profiles, "duration_ms", time.Since(start).Milliseconds())
		writeError(w, http.StatusConflict, "multiple configured profiles matched the supplied identifiers", profiles...)
		return
	}

	match := matches[0]
	outputPath := filepath.Join(workDir, "output.pdf")

	err = s.Decryptor.Decrypt(r.Context(), inputPath, outputPath, match.Profile.Password)
	duration := time.Since(start)
	switch {
	case errors.Is(err, ErrDecryptTimeout):
		logger.Error("request finished", "filename", filename, "profile", match.ProfileName, "pattern", match.Pattern, "result", "timeout", "duration_ms", duration.Milliseconds())
		writeError(w, http.StatusGatewayTimeout, "decryption timed out")
		return
	case err != nil:
		logger.Error("request finished", "filename", filename, "error", err, "profile", match.ProfileName, "pattern", match.Pattern, "result", "decrypt failed", "duration_ms", duration.Milliseconds())
		writeError(w, http.StatusUnprocessableEntity, "unable to decrypt PDF")
		return
	}

	info, err := os.Stat(outputPath)
	if err != nil || info.Size() == 0 {
		logger.Error("request finished", "filename", filename, "error", err, "profile", match.ProfileName, "pattern", match.Pattern, "result", "empty output", "duration_ms", duration.Milliseconds())
		writeError(w, http.StatusUnprocessableEntity, "unable to decrypt PDF")
		return
	}

	out, err := os.Open(outputPath)
	if err != nil {
		logger.Error("failed to open decrypted output", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer out.Close()

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Document-Encrypted", "true")
	w.Header().Set("X-Document-Profile", match.ProfileName)
	w.Header().Set("X-Matched-Pattern", match.Pattern)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.WriteHeader(http.StatusOK)
	io.Copy(w, out)

	logger.Info("request finished", "filename", filename, "profile", match.ProfileName, "pattern", match.Pattern, "result", "success", "duration_ms", duration.Milliseconds())
}

func isRequestTooLarge(err error) bool {
	return err != nil && strings.Contains(err.Error(), "http: request body too large")
}
