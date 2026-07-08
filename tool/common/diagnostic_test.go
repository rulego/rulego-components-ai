package common

import (
	"strings"
	"testing"
	"time"
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

// ---- CommandDiagnosticProvider ----

func TestCommandProvider_Supports(t *testing.T) {
	p := NewCommandDiagnosticProvider(CommandProviderConfig{Ext: ".py", Command: "ruff"})
	if !p.Supports("a.py") || !p.Supports("pkg/b.PY") {
		t.Fatal("should support .py (case-insensitive)")
	}
	if p.Supports("a.go") || p.Supports("a.ts") || p.Supports("Makefile") {
		t.Fatal("should not support other ext")
	}
}

func TestCommandProvider_DefaultTimeout(t *testing.T) {
	p := NewCommandDiagnosticProvider(CommandProviderConfig{Ext: ".py", Command: "ruff"})
	if p.cfg.Timeout != 10*time.Second {
		t.Fatalf("expected default 10s, got %v", p.cfg.Timeout)
	}
}

func TestCommandProvider_FilePlaceholder(t *testing.T) {
	p := NewCommandDiagnosticProvider(CommandProviderConfig{
		Ext: ".py", Command: "ruff",
		Args: []string{"check", "--output-format=concise", "{file}"},
	})
	args := p.buildArgs("/abs/main.py")
	want := []string{"check", "--output-format=concise", "/abs/main.py"}
	if len(args) != len(want) {
		t.Fatalf("len mismatch: %v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("arg %d: got %s want %s", i, args[i], want[i])
		}
	}
}

// 命令不在 PATH 时应静默跳过（不 error、不执行），与 go vet 不可用语义一致。
func TestCommandProvider_CommandMissingSkips(t *testing.T) {
	p := NewCommandDiagnosticProvider(CommandProviderConfig{
		Ext: ".txt", Command: "definitely-not-a-real-tool-xyz-123",
	})
	diags, err := p.Report(t.TempDir() + "/x.txt")
	if err != nil {
		t.Fatalf("should not error on missing command: %v", err)
	}
	if diags != nil {
		t.Fatalf("should return nil on missing command, got %v", diags)
	}
}

func TestParseCommandDiagnostics_DefaultPattern(t *testing.T) {
	out := "src/a.py:10:5: undefined name 'foo'\nsrc/b.ts:20: some error\nrandom noise line\n"
	diags := parseCommandDiagnostics(out)
	if len(diags) != 2 {
		t.Fatalf("expected 2 diags, got %d: %+v", len(diags), diags)
	}
	if diags[0].Line != 10 || diags[0].Col != 5 || diags[0].Message != "undefined name 'foo'" {
		t.Fatalf("wrong first diag: %+v", diags[0])
	}
	if diags[1].Line != 20 || diags[1].Col != 0 || diags[1].Message != "some error" {
		t.Fatalf("wrong second diag: %+v", diags[1])
	}
}

func TestParseCommandDiagnostics_WarningDowngraded(t *testing.T) {
	diags := parseCommandDiagnostics("a.py:3:1: warning: unused variable\n")
	if len(diags) != 1 || diags[0].Severity != SeverityWarning {
		t.Fatalf("expected warning severity: %+v", diags)
	}
}

func TestParseCommandDiagnostics_Empty(t *testing.T) {
	if diags := parseCommandDiagnostics(""); len(diags) != 0 {
		t.Fatalf("expected empty, got %+v", diags)
	}
}

// ---- 预置常量 ----

func TestPresetConfigs_Fields(t *testing.T) {
	cases := []struct {
		name string
		cfg  CommandProviderConfig
		ext  string
		cmd  string
	}{
		{"ruff", RuffDiagnosticConfig, ".py", "ruff"},
		{"tsc", TscDiagnosticConfig, ".ts", "tsc"},
		{"eslint", EslintDiagnosticConfig, ".js", "eslint"},
		{"shellcheck", ShellcheckDiagnosticConfig, ".sh", "shellcheck"},
	}
	for _, c := range cases {
		if c.cfg.Ext != c.ext || c.cfg.Command != c.cmd {
			t.Errorf("%s: want ext=%s cmd=%s, got ext=%s cmd=%s", c.name, c.ext, c.cmd, c.cfg.Ext, c.cfg.Command)
		}
		if !strings.Contains(strings.Join(c.cfg.Args, " "), "{file}") {
			t.Errorf("%s: Args missing {{file}} placeholder: %v", c.name, c.cfg.Args)
		}
	}
}

// tsc 自带 ParseFunc；其余走默认 colon 正则。各自典型输出必须能解析。
func TestPresetConfigs_OutputParseable(t *testing.T) {
	type tc struct {
		name    string
		sample  string
		parseFn func(string) []Diagnostic
	}
	cases := []tc{
		{"ruff", "a.py:10:5: F401 'os' imported but unused\n", parseCommandDiagnostics},
		{"eslint", "a.js:1:2: Unexpected token [semi]\n", parseCommandDiagnostics},
		{"shellcheck", "a.sh:3:1: warning: Double quote to prevent globbing [SC2086]\n", parseCommandDiagnostics},
		{"tsc-paren", "a.ts(3,5): error TS2322: Type 'string' is not assignable to type 'number'.\n", parseTscDiagnostics},
		{"tsc-colon", "a.ts:3:5 - error TS2322: Type 'string' is not assignable to type 'number'.\n", parseTscDiagnostics},
		{"tsc-warning", "a.ts:1:1 - warning TS6133: 'x' is declared but never read.\n", parseTscDiagnostics},
	}
	for _, c := range cases {
		diags := c.parseFn(c.sample)
		if len(diags) != 1 {
			t.Errorf("%s: expected 1 diag, got %d: %+v\nsample: %q", c.name, len(diags), diags, c.sample)
			continue
		}
		if diags[0].Line == 0 {
			t.Errorf("%s: line not parsed: %+v", c.name, diags[0])
		}
	}
	// tsc warning 应降级为 SeverityWarning
	warnDiags := parseTscDiagnostics("a.ts:1:1 - warning TS6133: unused\n")
	if len(warnDiags) != 1 || warnDiags[0].Severity != SeverityWarning {
		t.Fatalf("tsc warning should downgrade: %+v", warnDiags)
	}
}
