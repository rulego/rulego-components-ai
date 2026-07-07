// Package common 的 diagnostic.go：编辑后诊断回灌闭环的基础设施。
// 对标 MiMo-Code/OpenCode 的 edit→diagnostics→自修复；通用 DiagnosticProvider 接口按文件类型
// 注册多语言 provider（.go→GoVetProvider，.ts→tsserver，.py→pyright...），语言无关。
// 设计依据：docs/plans/工具层优化方案.md §3.4。
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

// 诊断严重度（对标 LSP DiagnosticSeverity）。
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

// DiagnosticProvider 诊断提供者接口。按文件类型注册实现。
// 起步只实现 GoVetProvider（go vet）；其他语言（ts/py/rs...）后续按需加 provider。
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
