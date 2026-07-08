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

// 诊断严重度。
const (
	SeverityError   = 1 // 仅 Error 级回灌给 agent，避免 warning 噪音
	SeverityWarning = 2
	SeverityInfo    = 3
	SeverityHint    = 4
)

// Diagnostic 单条诊断。
type Diagnostic struct {
	Severity int
	Line     int
	Col      int
	Message  string
}

// DiagnosticProvider 诊断提供者接口。按文件扩展名注册实现。
// 库内置 GoVetProvider（go vet）；其他语言用 NewCommandDiagnosticProvider 配置外部命令注册（ruff/eslint/tsc...）。
type DiagnosticProvider interface {
	// Report 对指定文件跑诊断，返回诊断列表。
	Report(filePath string) ([]Diagnostic, error)
	// Supports 判断 provider 是否处理该文件类型。
	Supports(filePath string) bool
}

// DiagnosticReport 把诊断格式化为回灌文本。只保留 severity=Error，每文件上限 maxPerFile（0=不限）。
// 空结果返回空串（调用方据此决定是否拼到 output）。
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

// goDiagPattern 匹配 go build/vet 输出：<file>:<line>:<col>: <msg> 或 <file>:<line>: <msg>（无 col 时 line 与 msg 共用冒号）
var goDiagPattern = regexp.MustCompile(`^(\S+\.go):(\d+)(?::(\d+))?:\s*(.*)$`)

// GoVetProvider 用 go vet 跑诊断（仅 .go 文件）。无状态，可全局注册复用。
type GoVetProvider struct{}

// Supports 仅处理 .go 文件。
func (p *GoVetProvider) Supports(filePath string) bool {
	return strings.ToLower(filepath.Ext(filePath)) == ".go"
}

// Report 跑 go vet 并解析输出。go vet 非零退出码表示有诊断（非执行错误），故忽略 Run 的 error。
func (p *GoVetProvider) Report(filePath string) ([]Diagnostic, error) {
	if !p.Supports(filePath) {
		return nil, nil
	}
	// go 不可用直接跳过（不启动进程、不阻塞主流程）
	if _, err := exec.LookPath("go"); err != nil {
		return nil, nil
	}
	// 超时保护：vet 对损坏/超大文件可能卡住，限 10s 避免阻塞工具调用
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

// parseGoDiagnostics 解析 go build/vet 的行输出为 Diagnostic 列表。
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
		// go vet 输出含编译错误与可疑构造，默认按 Error；显式 warning 降级
		sev := SeverityError
		if strings.HasPrefix(strings.ToLower(msg), "warning") {
			sev = SeverityWarning
		}
		diags = append(diags, Diagnostic{Severity: sev, Line: lineNo, Col: col, Message: msg})
	}
	return diags
}

// diagnosticProviders 按文件扩展名注册诊断 provider（".go"→GoVetProvider 等）。
// write 写后按此查 provider；未注册的类型不跑诊断（默认关闭）。应用层启动时注册。
var diagnosticProviders sync.Map

// RegisterDiagnosticProvider 注册某扩展名（如 ".go"）对应的诊断 provider。
func RegisterDiagnosticProvider(ext string, p DiagnosticProvider) {
	diagnosticProviders.Store(strings.ToLower(ext), p)
}

// LookupDiagnosticProvider 按文件扩展名查注册的 provider；未注册返回 nil。
func LookupDiagnosticProvider(filePath string) DiagnosticProvider {
	v, ok := diagnosticProviders.Load(strings.ToLower(filepath.Ext(filePath)))
	if !ok {
		return nil
	}
	p, _ := v.(DiagnosticProvider)
	return p
}

// defaultDiagPattern 通用 lint 输出解析：<file>:<line>:<col>: <msg> 或 <file>:<line>: <msg>。
// 文件名用 [^:\s]+ 限定（不含冒号/空白），避免贪婪吞掉行号。覆盖 ruff/eslint/tsc/mypy 等多数 colon 格式。
var defaultDiagPattern = regexp.MustCompile(`^([^:\s]+):(\d+)(?::(\d+))?:\s*(.*)$`)

// CommandProviderConfig 通用命令式诊断 provider 的配置。
type CommandProviderConfig struct {
	// Ext 关联扩展名（含点，如 ".py"），决定 Supports 是否处理该文件。
	Ext string
	// Command 可执行命令（如 "ruff"）。PATH 中不可用时静默跳过，不阻塞工具调用。
	Command string
	// Args 命令参数；"{file}" 占位符替换为目标文件绝对路径。
	Args []string
	// Dir 命令工作目录，空则取文件所在目录。
	Dir string
	// Timeout 执行超时，<=0 取默认 10s。
	Timeout time.Duration
	// ParseFunc 自定义输出解析；为 nil 时用默认 colon 正则解析。
	ParseFunc func(output string) []Diagnostic
}

// CommandDiagnosticProvider 通过外部命令跑诊断的通用 provider。
// 应用层无需为每种语言写 struct，配置命令模板即可注册，例如：
//
//	common.RegisterDiagnosticProvider(".py", common.NewCommandDiagnosticProvider(common.CommandProviderConfig{
//	    Ext: ".py", Command: "ruff", Args: []string{"check", "--output-format=concise", "{file}"},
//	}))
type CommandDiagnosticProvider struct {
	cfg CommandProviderConfig
}

// NewCommandDiagnosticProvider 构造通用命令 provider，补默认超时、规范化扩展名。
func NewCommandDiagnosticProvider(cfg CommandProviderConfig) *CommandDiagnosticProvider {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	cfg.Ext = strings.ToLower(cfg.Ext)
	return &CommandDiagnosticProvider{cfg: cfg}
}

// 预置常用语言的诊断命令模板。命令未安装时 NewCommandDiagnosticProvider 内部
// LookPath 自动跳过，注册无害；每个扩展名注册一个 provider 即可：
//
//	common.RegisterDiagnosticProvider(".py", common.NewCommandDiagnosticProvider(common.RuffDiagnosticConfig))
//	common.RegisterDiagnosticProvider(".ts", common.NewCommandDiagnosticProvider(common.TscDiagnosticConfig))
//	common.RegisterDiagnosticProvider(".js", common.NewCommandDiagnosticProvider(common.EslintDiagnosticConfig))
//	common.RegisterDiagnosticProvider(".sh", common.NewCommandDiagnosticProvider(common.ShellcheckDiagnosticConfig))
//	// .go 用内置 GoVetProvider：
//	common.RegisterDiagnosticProvider(".go", &common.GoVetProvider{})
//
// 输出非 colon 格式的工具自带 ParseFunc（见 TscDiagnosticConfig）；其他自定义工具同理用 CommandProviderConfig.ParseFunc。
var (
	// RuffDiagnosticConfig Python ruff，concise 格式：file:line:col: CODE msg。
	RuffDiagnosticConfig = CommandProviderConfig{Ext: ".py", Command: "ruff", Args: []string{"check", "--output-format=concise", "{file}"}}
	// TscDiagnosticConfig TypeScript tsc，file(line,col): error TSxxxx: msg（自带 ParseFunc，兼容新版 colon 格式）。
	TscDiagnosticConfig = CommandProviderConfig{Ext: ".ts", Command: "tsc", Args: []string{"--noEmit", "--pretty", "false", "{file}"}, ParseFunc: parseTscDiagnostics}
	// EslintDiagnosticConfig JavaScript eslint，unix 格式：file:line:col: msg [rule]。
	EslintDiagnosticConfig = CommandProviderConfig{Ext: ".js", Command: "eslint", Args: []string{"--format=unix", "{file}"}}
	// ShellcheckDiagnosticConfig shell 脚本 shellcheck，gcc 格式：file:line:col: severity: msg。
	ShellcheckDiagnosticConfig = CommandProviderConfig{Ext: ".sh", Command: "shellcheck", Args: []string{"-f", "gcc", "{file}"}}
)

// Supports 按扩展名匹配（大小写不敏感）。
func (p *CommandDiagnosticProvider) Supports(filePath string) bool {
	return p.cfg.Ext != "" && strings.ToLower(filepath.Ext(filePath)) == p.cfg.Ext
}

// buildArgs 替换参数中的 {file} 占位符为目标文件路径。
func (p *CommandDiagnosticProvider) buildArgs(filePath string) []string {
	args := make([]string, len(p.cfg.Args))
	for i, a := range p.cfg.Args {
		args[i] = strings.ReplaceAll(a, "{file}", filePath)
	}
	return args
}

// Report 跑外部命令并解析输出。命令不可用直接返回 nil（不阻塞主流程）。
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

// parseCommandDiagnostics 用默认 colon 正则解析 lint 输出为诊断列表。
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

// tsc 输出格式随版本不同：旧版/pretty 为 file(line,col): error TSxxxx: msg；
// 新版 --pretty false 为 file:line:col - error TSxxxx: msg。两种都解析。
var (
	tscParenPattern = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\):\s*(error|warning)\s+(.*)$`)
	tscColonPattern = regexp.MustCompile(`^([^:\s]+):(\d+):(\d+)\s*-\s*(error|warning)\s+(.*)$`)
)

// parseTscDiagnostics 解析 tsc 输出（圆括号与 colon 两种格式）。
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
