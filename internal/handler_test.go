package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeDecryptor is a test double for Decryptor so HTTP handler tests never
// need a real qpdf binary.
type fakeDecryptor struct {
	err        error
	outputData []byte

	// unencrypted, when true, makes IsEncrypted report the input as already
	// decrypted (the zero value keeps existing tests on the normal
	// match-and-decrypt path).
	unencrypted    bool
	isEncryptedErr error
}

func (f *fakeDecryptor) Decrypt(ctx context.Context, inputPath, outputPath, password string) error {
	if f.err != nil {
		return f.err
	}
	data := f.outputData
	if data == nil {
		data = []byte("%PDF-1.4\ndecrypted content")
	}
	return os.WriteFile(outputPath, data, 0o600)
}

func (f *fakeDecryptor) IsEncrypted(ctx context.Context, path string) (bool, error) {
	if f.isEncryptedErr != nil {
		return false, f.isEncryptedErr
	}
	return !f.unencrypted, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	content := `{
		"company-payslip": {"patterns": ["payslip"], "password": "secret-a"},
		"example-bank-credit-card": {"patterns": ["credit card statement"], "password": "secret-b"},
		"august-catchall": {"patterns": ["august"], "password": "secret-c"}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestServer(t *testing.T, dec Decryptor) *Server {
	t.Helper()
	return NewServer(testConfigPath(t), 10*1024*1024, dec, discardLogger())
}

// buildMultipart constructs a multipart/form-data body. If identifiersRaw is
// non-nil, it is written verbatim as the "identifiers" field (allowing tests
// to send malformed JSON). If fileContent is non-nil, it is written as the
// "file" field with the given filename.
func buildMultipart(t *testing.T, identifiersRaw []byte, filename string, fileContent []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)

	if identifiersRaw != nil {
		fw, err := w.CreateFormField("identifiers")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(identifiersRaw); err != nil {
			t.Fatal(err)
		}
	}

	if fileContent != nil {
		fw, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(fileContent); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body, w.FormDataContentType()
}

func doDecryptRequest(t *testing.T, srv *Server, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/decrypt", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func validIdentifiers(t *testing.T, ids []string) []byte {
	t.Helper()
	data, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

var validPDFBytes = []byte("%PDF-1.4\n%%some encrypted looking bytes\n%%EOF")

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %q", resp.Status)
	}
}

func TestDecrypt_MalformedIdentifiersJSON(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	body, ct := buildMultipart(t, []byte(`["unterminated`), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_IdentifiersNotArray(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	body, ct := buildMultipart(t, []byte(`{"not":"an array"}`), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_MissingFile(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"payslip"}), "", nil)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_MissingIdentifiers(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	body, ct := buildMultipart(t, nil, "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_OversizedUpload(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	srv.MaxUploadBytes = 16 // tiny limit to trigger the oversized path deterministically

	big := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("A"), 1024)...)
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"payslip"}), "input.pdf", big)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_NonPDFUpload(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"payslip"}), "input.pdf", []byte("not a pdf at all"))
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_NoMatchingPattern(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"totally unrelated invoice"}), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_MultipleMatchingPatterns(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	// "august" and "credit card statement" both configured; this identifier
	// contains both substrings.
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"August Credit Card Statement"}), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Details) != 2 {
		t.Fatalf("expected 2 matched patterns listed, got %v", resp.Details)
	}
}

func TestDecrypt_Success(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{outputData: []byte("%PDF-1.4\ndecrypted!")})
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"Monthly Payslip 2026"}), "myfile.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("expected application/pdf content type, got %q", ct)
	}
	if profile := rec.Header().Get("X-Document-Profile"); profile != "company-payslip" {
		t.Fatalf("expected profile company-payslip, got %q", profile)
	}
	if pattern := rec.Header().Get("X-Matched-Pattern"); pattern != "payslip" {
		t.Fatalf("expected pattern payslip, got %q", pattern)
	}
	if rec.Body.String() != "%PDF-1.4\ndecrypted!" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
	// The configured password must never appear in the response.
	if bytes.Contains(rec.Body.Bytes(), []byte("secret-a")) {
		t.Fatal("password leaked in response body")
	}
}

func TestDecrypt_EchoUnencryptedPDF(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{unencrypted: true})
	// No identifier here matches any configured profile, proving matching
	// was skipped entirely for an already-unencrypted upload.
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"totally unrelated invoice"}), "myfile.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if enc := rec.Header().Get("X-Document-Encrypted"); enc != "false" {
		t.Fatalf("expected X-Document-Encrypted: false, got %q", enc)
	}
	if rec.Header().Get("X-Document-Profile") != "" || rec.Header().Get("X-Matched-Pattern") != "" {
		t.Fatalf("expected no profile/pattern headers in echo mode, got profile=%q pattern=%q",
			rec.Header().Get("X-Document-Profile"), rec.Header().Get("X-Matched-Pattern"))
	}
	if !bytes.Equal(rec.Body.Bytes(), validPDFBytes) {
		t.Fatalf("expected the original upload to be echoed back unchanged, got %q", rec.Body.String())
	}
}

func TestDecrypt_EncryptionCheckFailure(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{isEncryptedErr: ErrDecryptFailed})
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"payslip"}), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_EncryptionCheckTimeout(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{isEncryptedErr: ErrDecryptTimeout})
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"payslip"}), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_QPDFTimeout(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{err: ErrDecryptTimeout})
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"payslip"}), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_QPDFFailure(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{err: ErrDecryptFailed})
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"payslip"}), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecrypt_ConfigLoadFailure(t *testing.T) {
	srv := newTestServer(t, &fakeDecryptor{})
	srv.LoadConfig = func(path string) (PatternConfig, error) {
		return nil, errors.New("boom")
	}
	body, ct := buildMultipart(t, validIdentifiers(t, []string{"payslip"}), "input.pdf", validPDFBytes)
	rec := doDecryptRequest(t, srv, body, ct)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("boom")) {
		t.Fatal("internal config error detail leaked to caller")
	}
}
