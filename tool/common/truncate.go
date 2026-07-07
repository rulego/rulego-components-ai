// Package common 的 truncate.go：统一截断服务（错误感知 head+tail）。
// 对标 MiMo-Code / OpenCode 的输出截断策略，供 read/bash/grep/glob/agent 复用。
// 设计依据：docs/plans/工具层优化方案.md §3.5。
package common

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// 截断默认常量（对标 mimo MAX_LINES=2000 / MAX_BYTES=50KB / TAIL_SCAN=2048）。
const (
	TruncateDefaultMaxLines = 2000
	TruncateDefaultMaxBytes = 50 * 1024 // 50KB
	TruncateTailScanBytes   = 2048      // 尾部扫描错误关键字的字节数
	TruncateHeadRatio = 0.7 // head+tail 时 head 占比
)

// truncateErrorPattern 尾部命中强错误信号则保留 tail，避免错误上下文被截断丢失。
var truncateErrorPattern = regexp.MustCompile(
	`(?i)(traceback|panic|fatal|core dumped|segfault|exit code [1-9])`,
)

// TruncateDirection 截断方向。
type TruncateDirection int

const (
	// TruncHeadTail 错误感知（默认零值）：扫尾部错误关键字，命中则 head+tail(70/30)，否则纯 head。
	TruncHeadTail TruncateDirection = iota
	// TruncHead 纯 head（保留开头）。
	TruncHead
	// TruncTail 纯 tail（保留结尾）。
	TruncTail
)

// TruncateOptions 截断选项。零值字段用默认常量。
type TruncateOptions struct {
	MaxLines  *int
	MaxBytes  *int
	Direction TruncateDirection
}

// TruncateResult 截断结果。
type TruncateResult struct {
	Content    string
	Truncated  bool
	OutputPath string // 若调用方把完整原文落盘，填路径供 agent 取回（Truncate 本身不落盘）
}

// Truncate 对长文本做错误感知截断。
// 未超限原样返回；超限按 Direction 处理：HeadTail(默认) 扫尾部错误关键字决定 head+tail 或纯 head。
func Truncate(text string, opts TruncateOptions) TruncateResult {
	maxLines, maxBytes := resolveTruncateLimits(opts)

	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines && len(text) <= maxBytes {
		return TruncateResult{Content: text, Truncated: false}
	}

	switch opts.Direction {
	case TruncTail:
		return truncTail(lines, maxLines, maxBytes)
	case TruncHead:
		return truncHead(lines, maxLines, maxBytes)
	default: // TruncHeadTail
		if scanTailForError(text) {
			return truncHeadTail(lines, maxLines, maxBytes)
		}
		return truncHead(lines, maxLines, maxBytes)
	}
}

func resolveTruncateLimits(opts TruncateOptions) (int, int) {
	maxLines := TruncateDefaultMaxLines
	if opts.MaxLines != nil && *opts.MaxLines > 0 {
		maxLines = *opts.MaxLines
	}
	maxBytes := TruncateDefaultMaxBytes
	if opts.MaxBytes != nil && *opts.MaxBytes > 0 {
		maxBytes = *opts.MaxBytes
	}
	return maxLines, maxBytes
}

// scanTailForError 扫描文本尾部 TruncateTailScanBytes 字节是否含错误关键字。
func scanTailForError(text string) bool {
	region := text
	if len(region) > TruncateTailScanBytes {
		region = region[len(region)-TruncateTailScanBytes:]
	}
	return truncateErrorPattern.MatchString(region)
}

// truncHead 保留开头 maxLines 行（且字节不超 maxBytes），拼 omitted 提示。
func truncHead(lines []string, maxLines, maxBytes int) TruncateResult {
	n := maxLines
	if n > len(lines) {
		n = len(lines)
	}
	head := capBytes(strings.Join(lines[:n], "\n"), maxBytes)
	omitted := len(lines) - n
	footer := fmt.Sprintf("\n\n... [%d lines omitted — showing head. Use Grep to search or Read with offset to see more]", omitted)
	return TruncateResult{Content: head + footer, Truncated: true}
}

// truncTail 保留结尾 maxLines 行。
func truncTail(lines []string, maxLines, maxBytes int) TruncateResult {
	n := maxLines
	if n > len(lines) {
		n = len(lines)
	}
	tail := capBytes(strings.Join(lines[len(lines)-n:], "\n"), maxBytes)
	header := fmt.Sprintf("\n... [%d lines omitted — showing tail. Use Grep to search or Read with offset to see more]\n\n", len(lines)-n)
	return TruncateResult{Content: header + tail, Truncated: true}
}

// truncHeadTail head(70%)+tail(30%)，中间 omitted 提示（错误感知：尾部错误上下文保留）。
func truncHeadTail(lines []string, maxLines, maxBytes int) TruncateResult {
	headLines := int(float64(maxLines) * TruncateHeadRatio)
	if headLines < 1 {
		headLines = 1
	}
	tailLines := maxLines - headLines
	if tailLines < 1 {
		tailLines = 1
	}
	// 行数不足以拆分时退化为对半切
	if headLines+tailLines >= len(lines) {
		headLines = len(lines) / 2
		tailLines = len(lines) - headLines
	}
	headBytes := int(float64(maxBytes) * TruncateHeadRatio)
	head := capBytes(strings.Join(lines[:headLines], "\n"), headBytes)
	tail := capBytes(strings.Join(lines[len(lines)-tailLines:], "\n"), maxBytes-headBytes)
	omitted := len(lines) - headLines - tailLines
	mid := fmt.Sprintf("\n\n... [%d lines omitted — error detected in tail, showing head and tail. Use Grep to search or Read with offset to see the middle] ...\n\n", omitted)
	return TruncateResult{Content: head + mid + tail, Truncated: true}
}

// capBytes 截断到 maxBytes（在 rune 边界切，避免截断多字节字符）。
func capBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "... [byte-limited]"
}

// WriteToTruncationDir 把完整原文落盘到 dir，返回文件路径供 agent 取回。
// 调用方（如 bash 工具）持有 data 目录上下文时使用；Truncate 本身不落盘。
func WriteToTruncationDir(text, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create truncation dir: %w", err)
	}
	name := fmt.Sprintf("tool-output-%s-%s", time.Now().Format("20060102-150405"), randHex(4))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		return "", fmt.Errorf("write truncation file: %w", err)
	}
	return path, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
