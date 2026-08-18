package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	content := `{
		"company-payslip": {"patterns": ["payslip"], "password": "PASSWORD_A"},
		"example-bank-account": {"patterns": ["example-bank.com"], "password": "PASSWORD_B"}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(cfg))
	}
	payslip := cfg["company-payslip"]
	if len(payslip.Patterns) != 1 || payslip.Patterns[0] != "payslip" || payslip.Password != "PASSWORD_A" {
		t.Fatalf("unexpected company-payslip entry: %+v", payslip)
	}
}

func TestLoadConfig_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	if err := os.WriteFile(path, []byte(`{"company-payslip": {`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadConfig_EmptyObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for empty config, got nil")
	}
}

func TestLoadConfig_MissingPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	content := `{"company-payslip": {"patterns": ["payslip"]}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for missing password, got nil")
	}
}

func TestLoadConfig_MissingPatterns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	content := `{"company-payslip": {"password": "PASSWORD_A"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for missing patterns, got nil")
	}
}

func TestLoadConfig_EmptyPatternInList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	content := `{"company-payslip": {"patterns": ["payslip", ""], "password": "PASSWORD_A"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for empty pattern in list, got nil")
	}
}

func TestLoadConfig_ErrorDoesNotLeakPasswords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	if err := os.WriteFile(path, []byte(`not json at all, super-secret-password`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); strings.Contains(got, "super-secret-password") {
		t.Fatalf("error message leaked file contents: %s", got)
	}
}
