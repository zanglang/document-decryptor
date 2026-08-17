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
		"payslip": {"name": "company-payslip", "password": "PASSWORD_A"},
		"example-bank.com": {"name": "example-bank-account", "password": "PASSWORD_B"}
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
	if cfg["payslip"].Name != "company-payslip" || cfg["payslip"].Password != "PASSWORD_A" {
		t.Fatalf("unexpected payslip entry: %+v", cfg["payslip"])
	}
}

func TestLoadConfig_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.json")
	if err := os.WriteFile(path, []byte(`{"payslip": {`), 0o600); err != nil {
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
	content := `{"payslip": {"name": "company-payslip"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for missing password, got nil")
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
