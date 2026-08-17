package internal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// PDFMagic is the header every valid PDF file must begin with.
const PDFMagic = "%PDF-"

var (
	// ErrNotPDF indicates the supplied input does not look like a PDF file.
	ErrNotPDF = errors.New("input does not appear to be a PDF file")
	// ErrDecryptFailed indicates qpdf ran but could not decrypt the file
	// (e.g. wrong password, or the file is otherwise not decryptable).
	ErrDecryptFailed = errors.New("failed to decrypt PDF")
	// ErrDecryptTimeout indicates qpdf did not finish within the configured
	// timeout.
	ErrDecryptTimeout = errors.New("decryption timed out")
)

// LooksLikePDF performs a basic sanity check that data begins with the PDF
// magic header.
func LooksLikePDF(header []byte) bool {
	return bytes.HasPrefix(header, []byte(PDFMagic))
}

// Decryptor decrypts a password-protected PDF file on disk. It exists as an
// interface so HTTP handler tests can substitute a fake implementation
// without invoking the real qpdf binary.
type Decryptor interface {
	Decrypt(ctx context.Context, inputPath, outputPath, password string) error
}

// QPDFDecryptor decrypts PDFs by invoking the qpdf binary directly (never
// via a shell) with an enforced timeout.
type QPDFDecryptor struct {
	// BinPath is the path to (or name of) the qpdf executable. Defaults to
	// "qpdf" if empty.
	BinPath string
	// Timeout bounds how long a single qpdf invocation may run.
	Timeout time.Duration
	// Logger receives diagnostic information. qpdf's stderr is logged here
	// at debug level, never returned to callers.
	Logger *slog.Logger
}

func (q *QPDFDecryptor) binPath() string {
	if q.BinPath == "" {
		return "qpdf"
	}
	return q.BinPath
}

func (q *QPDFDecryptor) timeout() time.Duration {
	if q.Timeout <= 0 {
		return 30 * time.Second
	}
	return q.Timeout
}

// Decrypt invokes:
//
//	qpdf --password=<password> --decrypt <inputPath> <outputPath>
//
// The binary is invoked directly via exec.CommandContext with explicit
// argument separation; no shell is involved.
func (q *QPDFDecryptor) Decrypt(ctx context.Context, inputPath, outputPath, password string) error {
	ctx, cancel := context.WithTimeout(ctx, q.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, q.binPath(),
		"--password="+password,
		"--decrypt",
		inputPath,
		outputPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()

	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrDecryptTimeout
	}

	if err != nil {
		// qpdf's exit code convention is: 0 = success, 3 = success with
		// warnings (e.g. it had to reconstruct a damaged cross-reference
		// table), anything else = failure. Warnings-only runs still
		// produce valid decrypted output and must not be treated as an
		// error.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
			if q.Logger != nil {
				q.Logger.Debug("qpdf succeeded with warnings", "stderr", stderr.String())
			}
		} else {
			if q.Logger != nil {
				// qpdf stderr may reference filenames but never passwords; it
				// is safe to log at debug level for operator troubleshooting.
				// It is intentionally never returned to the HTTP caller.
				q.Logger.Debug("qpdf invocation failed", "error", err, "stderr", stderr.String())
			}
			return fmt.Errorf("%w", ErrDecryptFailed)
		}
	}

	info, statErr := os.Stat(outputPath)
	if statErr != nil || info.Size() == 0 {
		return fmt.Errorf("%w: output file missing or empty", ErrDecryptFailed)
	}

	return nil
}
