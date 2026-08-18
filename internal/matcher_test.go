package internal

import "testing"

func patternNames(matches []Match) []string {
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Pattern
	}
	return names
}

func TestFindMatches_ExactSubstring(t *testing.T) {
	cfg := PatternConfig{
		"payslip": {Patterns: []string{"payslip"}, Password: "a"},
	}
	matches := FindMatches(cfg, []string{"Monthly payslip"})
	if len(matches) != 1 || matches[0].Pattern != "payslip" {
		t.Fatalf("expected single match for payslip, got %v", matches)
	}
}

func TestFindMatches_CaseInsensitive(t *testing.T) {
	cfg := PatternConfig{
		"payslip": {Patterns: []string{"payslip"}, Password: "a"},
	}
	matches := FindMatches(cfg, []string{"MONTHLY PAYSLIP AUGUST 2026"})
	if len(matches) != 1 {
		t.Fatalf("expected case-insensitive match, got %v", matches)
	}
}

func TestFindMatches_SubstringWithinEmail(t *testing.T) {
	cfg := PatternConfig{
		"bank": {Patterns: []string{"example-bank.com"}, Password: "a"},
	}
	matches := FindMatches(cfg, []string{"estatements@example-bank.com"})
	if len(matches) != 1 {
		t.Fatalf("expected match within email address, got %v", matches)
	}
}

func TestFindMatches_SubstringWithinSubject(t *testing.T) {
	cfg := PatternConfig{
		"cc": {Patterns: []string{"credit card statement"}, Password: "a"},
	}
	matches := FindMatches(cfg, []string{"August 2026 Credit Card Statement"})
	if len(matches) != 1 {
		t.Fatalf("expected match within subject line, got %v", matches)
	}
}

func TestFindMatches_SubstringWithinFilename(t *testing.T) {
	cfg := PatternConfig{
		"cc": {Patterns: []string{"statement-202608"}, Password: "a"},
	}
	matches := FindMatches(cfg, []string{"statement-202608.pdf"})
	if len(matches) != 1 {
		t.Fatalf("expected match within filename, got %v", matches)
	}
}

func TestFindMatches_Unicode(t *testing.T) {
	cfg := PatternConfig{
		"payslip-de": {Patterns: []string{"gehaltsabrechnung"}, Password: "a"},
	}
	matches := FindMatches(cfg, []string{"GEHALTSABRECHNUNG Müller Januar"})
	if len(matches) != 1 {
		t.Fatalf("expected unicode case-insensitive match, got %v", matches)
	}

	// German sharp s / uppercase eszett style folding is not guaranteed,
	// but basic multi-byte case folding (e.g. Cyrillic) should still work.
	cfg2 := PatternConfig{
		"invoice-ru": {Patterns: []string{"платеж"}, Password: "a"},
	}
	matches2 := FindMatches(cfg2, []string{"ПЛАТЕЖ за август"})
	if len(matches2) != 1 {
		t.Fatalf("expected cyrillic case-insensitive match, got %v", matches2)
	}
}

func TestFindMatches_ZeroMatches(t *testing.T) {
	cfg := PatternConfig{
		"payslip": {Patterns: []string{"payslip"}, Password: "a"},
	}
	matches := FindMatches(cfg, []string{"invoice.pdf", "random@example.com"})
	if len(matches) != 0 {
		t.Fatalf("expected zero matches, got %v", matches)
	}
}

func TestFindMatches_MultipleMatches(t *testing.T) {
	cfg := PatternConfig{
		"payslip":    {Patterns: []string{"payslip"}, Password: "a"},
		"august-doc": {Patterns: []string{"august"}, Password: "b"},
		"invoice":    {Patterns: []string{"invoice"}, Password: "c"},
	}
	matches := FindMatches(cfg, []string{"August Payslip 2026"})
	got := patternNames(matches)
	if len(got) != 2 {
		t.Fatalf("expected two matches, got %v", got)
	}
}

func TestFindMatches_PatternMatchedOnceDespiteMultipleIdentifierHits(t *testing.T) {
	cfg := PatternConfig{
		"payslip": {Patterns: []string{"payslip"}, Password: "a"},
	}
	matches := FindMatches(cfg, []string{"payslip.pdf", "Monthly Payslip", "payslip@example.com"})
	if len(matches) != 1 {
		t.Fatalf("expected pattern to be counted once, got %v", matches)
	}
}

func TestFindMatches_ProfileMatchedOnceDespiteMultiplePatternHits(t *testing.T) {
	cfg := PatternConfig{
		"bank statements": {
			Patterns: []string{"bills@banka.com", "statement@bankb.com", "here is your monthly statement"},
			Password: "a",
		},
	}
	matches := FindMatches(cfg, []string{"here is your monthly statement", "sent from bills@banka.com"})
	if len(matches) != 1 {
		t.Fatalf("expected profile to be counted once despite multiple pattern hits, got %v", matches)
	}
	if matches[0].ProfileName != "bank statements" {
		t.Fatalf("expected profile name %q, got %q", "bank statements", matches[0].ProfileName)
	}
	if matches[0].Pattern != "bills@banka.com" {
		t.Fatalf("expected first matching pattern in list order, got %q", matches[0].Pattern)
	}
}
