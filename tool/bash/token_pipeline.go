// 命令输出 token 清洗链。
//
// 处理顺序：
//
//	progress 帧（\r 重绘只留最后一帧）→ ANSI 剥离 →
//	密钥脱敏（Bearer/JWT/AWS/GitHub/OpenAI/Anthropic/Slack/通用 api_key/PEM 块）→
//	longline（超长行折叠）。
//
// never-worse 守卫：清洗后未变小（含裕量）则回滚原文，避免误伤合法输出；
// 但脱敏不可逆，已发生脱敏替换时不回滚。
// opt-out：命令含 `# nofilter` 或 `# raw` 时跳过整条链。
package bash

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	longlineThreshold = 1000 // 超过此长度的行触发折叠（字节；中文约 333 字）
	longlineKeep      = 240  // 折叠时保留头部字符数
	neverWorseMargin  = 64  // never-worse 守卫裕量
)

// pipelinePlugin 是清洗链中的一个处理插件。
type pipelinePlugin interface {
	Name() string
	Apply(text string) string
}

// tokenPipeline 清洗链。按顺序应用各插件，链尾做 never-worse 守卫。
type tokenPipeline struct {
	plugins []pipelinePlugin
}

// newTokenPipeline 构造默认的 L1 清洗链（progress → ansi → redact → longline）。
func newTokenPipeline() *tokenPipeline {
	return &tokenPipeline{
		plugins: []pipelinePlugin{
			progressPlugin{},
			ansiPlugin{},
			redactPlugin{},
			longlinePlugin{},
		},
	}
}

// run 顺序应用插件。脱敏不可逆，已发生脱敏替换时跳过 never-worse 守卫，
// 避免短输入被替换为更长占位符后字节数变长而回滚脱敏。
func (p *tokenPipeline) run(text string) string {
	if text == "" {
		return text
	}
	out := text
	redacted := false
	for _, pl := range p.plugins {
		next := pl.Apply(out)
		if pl.Name() == "redact" && next != out {
			redacted = true
		}
		out = next
	}
	// 未发生脱敏时，清洗后未明显变小则回滚原文
	if !redacted && len(out)+neverWorseMargin >= len(text) {
		return text
	}
	return out
}

// nofilterOptOut 匹配 shell 注释形式的 opt-out 标记（# 前为行首/空白/;&|），
// 避免命令体内（如引号内）碰巧出现 "# nofilter" 字面量被误判。
var nofilterOptOut = regexp.MustCompile(`(?:^|[\s;&|])#\s*(?:nofilter|raw)\b`)

// shouldFilter 命令含 # nofilter / # raw 注释时跳过清洗链。
func shouldFilter(command string) bool {
	return !nofilterOptOut.MatchString(command)
}

// ============================================================================
// progress 帧折叠：\r 重绘只保留最后一帧
// ============================================================================

// progressPlugin 对每个 \n 分隔的行，按 \r 切片只保留最后一段。
type progressPlugin struct{}

func (progressPlugin) Name() string { return "progress" }

func (progressPlugin) Apply(text string) string {
	if !strings.ContainsRune(text, '\r') {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		idx := strings.LastIndexByte(ln, '\r')
		if idx >= 0 {
			lines[i] = ln[idx+1:]
		}
	}
	return strings.Join(lines, "\n")
}

// ============================================================================
// ANSI 转义剥离：CSI / OSC / DCS / SS3 等序列 + 退格/响铃等控制字符
// ============================================================================

type ansiPlugin struct{}

func (ansiPlugin) Name() string { return "ansi" }

// ansiEscape 匹配常见 ANSI/DEC 转义序列：
//
//	CSI（含 < >=? 等参数）、OSC、DCS/SOS/PM/APC（以 ST 终止）、SS2/SS3、其它两/三字节序列
var ansiEscape = regexp.MustCompile(
	`\x1b\[[0-9;:<=>?]*[!-/]*[@-~]` + // CSI
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC
		`|\x1b[PX^_][^\x1b]*(?:\x1b\\)` + // DCS / SOS / PM / APC
		`|\x1b[NO].` + // SS2 / SS3 + 单字节
		`|\x1b[=>]|\x1b[()*+].`) // 其它两/三字节序列

func (ansiPlugin) Apply(text string) string {
	if !strings.ContainsRune(text, 0x1b) {
		return stripControlKeepNL(text)
	}
	out := ansiEscape.ReplaceAllString(text, "")
	return stripControlKeepNL(out)
}

// stripControlKeepNL 移除退格/响铃/垂直制表/换页，保留 \n \r \t。
func stripControlKeepNL(s string) string {
	if !strings.ContainsAny(s, "\b\a\v\f") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\b', '\a', '\v', '\f':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ============================================================================
// 密钥脱敏：把已知密钥模式替换为 <redacted>，避免泄露到 agent 上下文
// ============================================================================

type redactPlugin struct{}

func (redactPlugin) Name() string { return "redact" }

var (
	redactBearer = regexp.MustCompile(`(?i)\b(Bearer|Token)\s+[A-Za-z0-9\-_\.=]{8,}`)
	redactJWT    = regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`)
	// AWS 访问密钥 ID：AKIA/ASIA + 恰好 16 个大写字母/数字
	redactAWS = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	// GitHub token：经典 PAT（gh[pousr]_）+ fine-grained（github_pat_）+ ghg_/ghd_
	redactGitHub     = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,251}\b|github_pat_[A-Za-z0-9_]{82,}|\bgh[gd]_[A-Za-z0-9]{20,}\b`)
	redactOpenAI     = regexp.MustCompile(`\bsk-[A-Za-z0-9]{16,}\b`)
	redactAnthropic  = regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{16,}\b`)
	redactSlack = regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9\-]{10,}\b`)
)

// redactPEMBlocks 脱敏完整的 BEGIN...END PEM 块
var redactPEMBlocks = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]+-----.*?-----END [A-Z ]+-----`)

// redactPEMBeginOnly 兜底：仅有 BEGIN 无 END（被截断/损坏的 PEM），
// 从 BEGIN 脱敏到首个空行或文末（PEM body 是不含空行的 base64）
var redactPEMBeginOnly = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]+-----.*?(?:\n\s*\n|$)`)

func (redactPlugin) Apply(text string) string {
	out := text
	out = redactPEMBlocks.ReplaceAllString(out, "<redacted pem>")
	out = redactPEMBeginOnly.ReplaceAllString(out, "<redacted pem>")
	out = redactBearer.ReplaceAllString(out, "$1 <redacted>")
	out = redactJWT.ReplaceAllString(out, "<redacted jwt>")
	out = redactAWS.ReplaceAllString(out, "<redacted aws>")
	out = redactGitHub.ReplaceAllString(out, "<redacted github token>")
	out = redactOpenAI.ReplaceAllString(out, "<redacted openai key>")
	out = redactAnthropic.ReplaceAllString(out, "<redacted anthropic key>")
	out = redactSlack.ReplaceAllString(out, "<redacted slack token>")
	return out
}

// ============================================================================
// longline 折叠：超长行保留头部 + 省略标记
// ============================================================================

// longlinePlugin 对超过 longlineThreshold 字符的行保留头 longlineKeep 字符。
type longlinePlugin struct{}

func (longlinePlugin) Name() string { return "longline" }

func (longlinePlugin) Apply(text string) string {
	if !strings.ContainsRune(text, '\n') {
		// 单行无换行符的特殊情况：检查整段
		if len(text) > longlineThreshold {
			return foldLine(text)
		}
		return text
	}
	lines := strings.Split(text, "\n")
	changed := false
	for i, ln := range lines {
		if len(ln) > longlineThreshold {
			lines[i] = foldLine(ln)
			changed = true
		}
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "\n")
}

// foldLine 折叠单行：保留头部 longlineKeep 字符，余下用 <elided N chars> 标记。
func foldLine(ln string) string {
	if len(ln) <= longlineThreshold {
		return ln
	}
	keep := longlineKeep
	if keep > len(ln) {
		keep = len(ln)
	}
	return fmt.Sprintf("%s <elided %d chars>", ln[:keep], len(ln)-keep)
}
