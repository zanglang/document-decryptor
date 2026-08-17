package internal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLooksLikePDF(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
		want   bool
	}{
		{"valid magic", []byte("%PDF-1.7\n..."), true},
		{"not a pdf", []byte("not a pdf at all"), false},
		{"empty", []byte(""), false},
		{"truncated magic", []byte("%PDF"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LooksLikePDF(tc.header); got != tc.want {
				t.Errorf("LooksLikePDF(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

// TestQPDFDecryptor_Integration exercises the real qpdf binary end-to-end:
// it encrypts a minimal PDF with a known password using qpdf itself, then
// verifies QPDFDecryptor can decrypt it back. It is skipped when qpdf is
// not available on PATH (e.g. in CI environments without it installed).
func TestQPDFDecryptor_Integration(t *testing.T) {
	qpdfPath, err := exec.LookPath("qpdf")
	if err != nil {
		t.Skip("qpdf not available on PATH, skipping integration test")
	}

	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.pdf")
	encrypted := filepath.Join(dir, "encrypted.pdf")
	decrypted := filepath.Join(dir, "decrypted.pdf")

	minimalPDF := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[]/Count 0>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF"
	if err := os.WriteFile(plain, []byte(minimalPDF), 0o600); err != nil {
		t.Fatal(err)
	}

	const password = "integration-test-password"
	encryptCmd := exec.Command(qpdfPath,
		"--encrypt", password, password, "256",
		"--", plain, encrypted,
	)
	if out, err := encryptCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to prepare encrypted fixture: %v\n%s", err, out)
	}

	dec := &QPDFDecryptor{Timeout: 10 * time.Second}
	if err := dec.Decrypt(context.Background(), encrypted, decrypted, password); err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	info, err := os.Stat(decrypted)
	if err != nil {
		t.Fatalf("expected decrypted output to exist: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("decrypted output is empty")
	}
}

func TestQPDFDecryptor_WrongPassword(t *testing.T) {
	qpdfPath, err := exec.LookPath("qpdf")
	if err != nil {
		t.Skip("qpdf not available on PATH, skipping integration test")
	}

	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.pdf")
	encrypted := filepath.Join(dir, "encrypted.pdf")
	decrypted := filepath.Join(dir, "decrypted.pdf")

	minimalPDF := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[]/Count 0>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF"
	if err := os.WriteFile(plain, []byte(minimalPDF), 0o600); err != nil {
		t.Fatal(err)
	}

	encryptCmd := exec.Command(qpdfPath,
		"--encrypt", "correct-password", "correct-password", "256",
		"--", plain, encrypted,
	)
	if out, err := encryptCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to prepare encrypted fixture: %v\n%s", err, out)
	}

	dec := &QPDFDecryptor{Timeout: 10 * time.Second}
	err = dec.Decrypt(context.Background(), encrypted, decrypted, "wrong-password")
	if err == nil {
		t.Fatal("expected error for wrong password, got nil")
	}
}

func TestQPDFDecryptor_Timeout(t *testing.T) {
	// Use a fake "qpdf" binary that sleeps, to deterministically exercise
	// the timeout path without depending on real qpdf's speed.
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "qpdf")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	dec := &QPDFDecryptor{BinPath: script, Timeout: 50 * time.Millisecond}
	err := dec.Decrypt(context.Background(), "in.pdf", "out.pdf", "pw")
	if err != ErrDecryptTimeout {
		t.Fatalf("expected ErrDecryptTimeout, got %v", err)
	}
}
