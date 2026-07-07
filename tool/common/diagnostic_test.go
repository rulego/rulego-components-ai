package common

import (
	"strings"
	"testing"
)

func TestParseGoDiagnostics_ErrorWithCol(t *testing.T) {
	out := "main.go:10:5: undefined: foo\n"
	diags := parseGoDiagnostics(out)
	if len(diags) != 1 {
		t.Fatalf("expected 1, got %d", len(diags))
	}
	if diags[0].Line != 10 || diags[0].Col != 5 {
		t.Fatalf("wrong line/col: %+v", diags[0])
	}
	if diags[0].Severity != SeverityError {
		t.Fatalf("expected error severity")
	}
	if diags[0].Message != "undefined: foo" {
		t.Fatalf("wrong message: %s", diags[0].Message)
	}
}

func TestParseGoDiagnostics_NoCol(t *testing.T) {
	out := "main.go:42: expected semicolon\n"
	diags := parseGoDiagnostics(out)
	if len(diags) != 1 || diags[0].Line != 42 || diags[0].Col != 0 {
		t.Fatalf("wrong: %+v", diags)
	}
}

func TestParseGoDiagnostics_WarningDowngraded(t *testing.T) {
	out := "a.go:3:1: warning: foo\n"
	diags := parseGoDiagnostics(out)
	if len(diags) != 1 || diags[0].Severity != SeverityWarning {
		t.Fatalf("expected warning severity: %+v", diags)
	}
}

func TestParseGoDiagnostics_NonGoIgnored(t *testing.T) {
	out := "notgo.txt:3:1: whatever\nsome random line\n"
	if diags := parseGoDiagnostics(out); len(diags) != 0 {
		t.Fatalf("non-go lines should be ignored: %+v", diags)
	}
}

func TestDiagnosticReport_OnlyErrors(t *testing.T) {
	diags := []Diagnostic{
		{Severity: SeverityError, Line: 1, Message: "err1"},
		{Severity: SeverityWarning, Line: 2, Message: "warn"},
		{Severity: SeverityError, Line: 3, Message: "err2"},
	}
	r := DiagnosticReport("a.go", diags, 10)
	if !strings.Contains(r, "err1") || !strings.Contains(r, "err2") {
		t.Fatalf("missing errors: %s", r)
	}
	if strings.Contains(r, "warn") {
		t.Fatalf("warning should be filtered: %s", r)
	}
}

func TestDiagnosticReport_EmptyWhenNoError(t *testing.T) {
	if r := DiagnosticReport("a.go", nil, 10); r != "" {
		t.Fatalf("expected empty, got %s", r)
	}
	if r := DiagnosticReport("a.go", []Diagnostic{{Severity: SeverityWarning, Line: 1, Message: "w"}}, 10); r != "" {
		t.Fatalf("warning-only should be empty, got %s", r)
	}
}

func TestDiagnosticReport_MaxPerFileTruncates(t *testing.T) {
	diags := []Diagnostic{
		{Severity: SeverityError, Line: 1, Message: "e1"},
		{Severity: SeverityError, Line: 2, Message: "e2"},
		{Severity: SeverityError, Line: 3, Message: "e3"},
	}
	r := DiagnosticReport("a.go", diags, 2)
	if !strings.Contains(r, "and 1 more") {
		t.Fatalf("expected truncation hint: %s", r)
	}
	if !strings.Contains(r, "e1") || !strings.Contains(r, "e2") {
		t.Fatalf("first 2 should be shown: %s", r)
	}
	if strings.Contains(r, "] e3") {
		t.Fatalf("e3 should be truncated: %s", r)
	}
}

func TestGoVetProvider_Supports(t *testing.T) {
	p := &GoVetProvider{}
	if !p.Supports("main.go") || !p.Supports("pkg/a.GO") {
		t.Fatal("should support .go")
	}
	if p.Supports("a.ts") || p.Supports("a.py") || p.Supports("Makefile") {
		t.Fatal("should not support non-go")
	}
}
