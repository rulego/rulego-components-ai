package common

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Diagnosis of severity.
const (
	SeverityError   = 1 // Only the error level is fed back to the agent to avoid warning noise
	SeverityWarning = 2
	SeverityInfo    = 3
	SeverityHint    = 4
)

// Diagnostic: Single diagnosis.
type Diagnostic struct {
	Severity int
	Line     int
	Col      int
	Message  string
}

// DiagnosticProvider diagnostic provider interface. Register and implement by file extension.
// The library includes GoVetProvider(go vet); For other languages, use NewCommandDiagnosticProvider to configure external command registration (ruff/eslint/tsc...).
type DiagnosticProvider interface {
	// Report: Run diagnostics on the specified file and return the diagnostic list.
	Report(filePath string) ([]Diagnostic, error)
	// Supports determines whether the provider handles this file type.
	Supports(filePath string) bool
}

// DiagnosticReport formats diagnostics as feedback text. Only severity=Error is retained, with a maximum maxPerFile (0=unlimited) per file.
// The empty result returns an empty string (the caller decides whether to confix the output accordingly).
func DiagnosticReport(filePath string, diags []Diagnostic, maxPerFile int) string {
	var errs []Diagnostic
	for _, d := range diags {
		if d.Severity == SeverityError {
			errs = append(errs, d)
		}
	}
	if len(errs) == 0 {
		return ""
	}
	total := len(errs)
	shown := errs
	if maxPerFile > 0 && total > maxPerFile {
		shown = errs[:maxPerFile]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<diagnostics file=\"%s\">\n", filePath)
	for _, d := range shown {
		fmt.Fprintf(&b, "ERROR [line %d", d.Line)
		if d.Col > 0 {
			fmt.Fprintf(&b, ":%d", d.Col)
		}
		fmt.Fprintf(&b, "] %s\n", d.Message)
	}
	if maxPerFile > 0 && total > maxPerFile {
		fmt.Fprintf(&b, "... and %d more\n", total-maxPerFile)
	}
	b.WriteString("</diagnostics>")
	return b.String()
}

// goDiagPattern matches go build/vet output:<file>:<line>:<col>: <msg> or <file>:<line>: <msg>(If no col, line and msg share colon)
var goDiagPattern = regexp.MustCompile(`^(\S+\.go):(\d+)(?::(\d+))?:\s*(.*)$`)

// GoVetProvider runs diagnostics with GoVet (only.go files). No status, can be registered and reused globally.
type GoVetProvider struct{}

// Supports only handles.go files.
func (p *GoVetProvider) Supports(filePath string) bool {
	return strings.ToLower(filepath.Ext(filePath)) == ".go"
}

// Report: run go vet and parse the output. Go vet is not zero and the exit code indicates a diagnosis (not an execution error), so the Run error is ignored.
func (p *GoVetProvider) Report(filePath string) ([]Diagnostic, error) {
	if !p.Supports(filePath) {
		return nil, nil
	}
	// Go is unusable and skips directly (no process start, no main flow blocked)
	if _, err := exec.LookPath("go"); err != nil {
		return nil, nil
	}
	// Timeout protection: VET may freeze damaged or oversized files, limited to 10 seconds to avoid blocking tool calls
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "vet", filePath)
	cmd.Dir = filepath.Dir(filePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	output := stderr.String()
	if output == "" {
		output = stdout.String()
	}
	return parseGoDiagnostics(output), nil
}

// parseGoDiagnostics parses the row output of go build/vet as a Diagnostic list.
func parseGoDiagnostics(output string) []Diagnostic {
	var diags []Diagnostic
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		m := goDiagPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		col := 0
		if m[3] != "" {
			col, _ = strconv.Atoi(m[3])
		}
		msg := strings.TrimSpace(m[4])
		// go vet outputs include compilation errors and suspicious constructs, default is Error; Explicit warning: Downgrade
		sev := SeverityError
		if strings.HasPrefix(strings.ToLower(msg), "warning") {
			sev = SeverityWarning
		}
		diags = append(diags, Diagnostic{Severity: sev, Line: lineNo, Col: col, Message: msg})
	}
	return diags
}

// diagnosticProviders registers diagnostic providers by file extension (".go"→GoVetProvider, etc.).
// write: After writing, click here to search for provider; Unregistered types do not run diagnostics (disabled by default). Register when the application layer starts.
var diagnosticProviders sync.Map

// RegisterDiagnosticProvider registers the diagnostic provider corresponding to a certain extension (such as ".go").
func RegisterDiagnosticProvider(ext string, p DiagnosticProvider) {
	diagnosticProviders.Store(strings.ToLower(ext), p)
}

// LookupDiagnosticProvider Searches for registered providers by file extension; Returns nil if not registered.
func LookupDiagnosticProvider(filePath string) DiagnosticProvider {
	v, ok := diagnosticProviders.Load(strings.ToLower(filepath.Ext(filePath)))
	if !ok {
		return nil
	}
	p, _ := v.(DiagnosticProvider)
	return p
}

// defaultDiagPattern general lint output parsing:<file>:<line>:<col>: <msg> or <file>:<line>:: <msg>.
// Filenames are limited to [^:\s]+ (excluding colons/spaces) to avoid greedily swallowing line numbers. Overrides most colon formats such as ruff/eslint/tsc/mypy.
var defaultDiagPattern = regexp.MustCompile(`^([^:\s]+):(\d+)(?::(\d+))?:\s*(.*)$`)

// CommandProviderConfig General Command Diagnostic Configuration provider.
type CommandProviderConfig struct {
	// Ext is associated with an extension (including a dot, such as ".py"), and determines whether Supports will handle the file.
	Ext string
	// Command can execute commands (such as "ruff"). Silently skips when unavailable in PATH, without blocking tool calls.
	Command string
	// Args command parameters; The "{file}" placeholder is replaced with the absolute path of the target file.
	Args []string
	// Dir command: Operating directory; empty to get the directory where the file is located.
	Dir string
	// Timeout executes timeout, <=0, set to the default 10s.
	Timeout time.Duration
	// ParseFunc custom output parsing; When nil is used, the default colon regex is used.
	ParseFunc func(output string) []Diagnostic
}

// CommandDiagnosticProvider is a general-purpose provider that runs diagnostics through external commands.
// The application layer does not need to write structs for each language; it can be registered by configuring command templates, for example:
//
//	common.RegisterDiagnosticProvider(".py", common.NewCommandDiagnosticProvider(common.CommandProviderConfig{
//	    Ext: ".py", Command: "ruff", Args: []string{"check", "--output-format=concise", "{file}"},
//	}))
type CommandDiagnosticProvider struct {
	cfg CommandProviderConfig
}

// NewCommandDiagnosticProvider constructs a general command provider, supplementing default timeouts and normalized extensions.
func NewCommandDiagnosticProvider(cfg CommandProviderConfig) *CommandDiagnosticProvider {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	cfg.Ext = strings.ToLower(cfg.Ext)
	return &CommandDiagnosticProvider{cfg: cfg}
}

// Pre-installed diagnostic command templates for commonly used languages. The command is not installed inside NewCommandDiagnosticProvider
// LookPath automatically skips it, making registration harmless; You just need to register one provider for each extension:
//
//	common.RegisterDiagnosticProvider(".py", common.NewCommandDiagnosticProvider(common.RuffDiagnosticConfig))
//	common.RegisterDiagnosticProvider(".ts", common.NewCommandDiagnosticProvider(common.TscDiagnosticConfig))
//	common.RegisterDiagnosticProvider(".js", common.NewCommandDiagnosticProvider(common.EslintDiagnosticConfig))
//	common.RegisterDiagnosticProvider(".sh", common.NewCommandDiagnosticProvider(common.ShellcheckDiagnosticConfig))
//	.go with built-in GoVetProvider:
//	common.RegisterDiagnosticProvider(".go", &common.GoVetProvider{})
//
// Tools that output non-colon formats come with ParseFunc (see TscDiagnosticConfig); Other custom tools use CommandProviderConfig.ParseFunc similarly.
var (
	// RuffDiagnosticConfig Python ruff, concise format: file:line:col: CODE msg.
	RuffDiagnosticConfig = CommandProviderConfig{Ext: ".py", Command: "ruff", Args: []string{"check", "--output-format=concise", "{file}"}}
	// TscDiagnosticConfig TypeScript tsc,file(line,col): error TSxxxx: msg (comes with ParseFunc, compatible with the new colon format).
	TscDiagnosticConfig = CommandProviderConfig{Ext: ".ts", Command: "tsc", Args: []string{"--noEmit", "--pretty", "false", "{file}"}, ParseFunc: parseTscDiagnostics}
	// EslintDiagnosticConfig JavaScript eslint, Unix format: file:line:col: msg [rule].
	EslintDiagnosticConfig = CommandProviderConfig{Ext: ".js", Command: "eslint", Args: []string{"--format=unix", "{file}"}}
	// ShellcheckDiagnosticConfig shell script shellcheck, gcc format: file:line:col: severity: msg.
	ShellcheckDiagnosticConfig = CommandProviderConfig{Ext: ".sh", Command: "shellcheck", Args: []string{"-f", "gcc", "{file}"}}
)

// Supports matches by extension (case-insensitive).
func (p *CommandDiagnosticProvider) Supports(filePath string) bool {
	return p.cfg.Ext != "" && strings.ToLower(filepath.Ext(filePath)) == p.cfg.Ext
}

// The {file} placeholder in the buildArgs replacement parameter is the target file path.
func (p *CommandDiagnosticProvider) buildArgs(filePath string) []string {
	args := make([]string, len(p.cfg.Args))
	for i, a := range p.cfg.Args {
		args[i] = strings.ReplaceAll(a, "{file}", filePath)
	}
	return args
}

// Report executes external commands and parses output. If the command is unavailable, it returns nil directly (without blocking the main flow).
func (p *CommandDiagnosticProvider) Report(filePath string) ([]Diagnostic, error) {
	if !p.Supports(filePath) {
		return nil, nil
	}
	if _, err := exec.LookPath(p.cfg.Command); err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.cfg.Command, p.buildArgs(filePath)...)
	if p.cfg.Dir != "" {
		cmd.Dir = p.cfg.Dir
	} else {
		cmd.Dir = filepath.Dir(filePath)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	output := stderr.String()
	if output == "" {
		output = stdout.String()
	}
	if p.cfg.ParseFunc != nil {
		return p.cfg.ParseFunc(output), nil
	}
	return parseCommandDiagnostics(output), nil
}

// parseCommandDiagnostics parses lint using the default colon regex to output as a diagnostic list.
func parseCommandDiagnostics(output string) []Diagnostic {
	var diags []Diagnostic
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		m := defaultDiagPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		col := 0
		if m[3] != "" {
			col, _ = strconv.Atoi(m[3])
		}
		msg := strings.TrimSpace(m[4])
		sev := SeverityError
		if strings.HasPrefix(strings.ToLower(msg), "warning") {
			sev = SeverityWarning
		}
		diags = append(diags, Diagnostic{Severity: sev, Line: lineNo, Col: col, Message: msg})
	}
	return diags
}

// TSC output format varies by version: old version/pretty is file(line, col): error TSxxxx: msg;
// New version --pretty false is file:line:col - error TSxxxx: msg. Both are analyzed.
var (
	tscParenPattern = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\):\s*(error|warning)\s+(.*)$`)
	tscColonPattern = regexp.MustCompile(`^([^:\s]+):(\d+):(\d+)\s*-\s*(error|warning)\s+(.*)$`)
)

// parseTscDiagnostics parses tsc outputs (in parentheses and colon formats).
func parseTscDiagnostics(output string) []Diagnostic {
	var diags []Diagnostic
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		for _, re := range []*regexp.Regexp{tscParenPattern, tscColonPattern} {
			m := re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			lineNo, _ := strconv.Atoi(m[2])
			col, _ := strconv.Atoi(m[3])
			sev := SeverityError
			if m[4] == "warning" {
				sev = SeverityWarning
			}
			diags = append(diags, Diagnostic{Severity: sev, Line: lineNo, Col: col, Message: strings.TrimSpace(m[5])})
			break
		}
	}
	return diags
}
