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

// Truncate default constants.
const (
	TruncateDefaultMaxLines = 2000
	TruncateDefaultMaxBytes = 50 * 1024 // 50KB
	TruncateTailScanBytes   = 2048      // The tail scans bytes of the wrong keyword
	TruncateHeadRatio       = 0.7       // Head + tail is the proportion of head
)

// truncateErrorPattern If the tail hits a strong error signal, the tail is kept to avoid the error context being cut off and lost.
var truncateErrorPattern = regexp.MustCompile(
	`(?i)(traceback|panic|fatal|core dumped|segfault|exit code [1-9])`,
)

// TruncateDirection: Truncate direction.
type TruncateDirection int

const (
	// TruncHeadTail Error Detection (default zero): sweeps the tail incorrect keyword; if hit, use head+tail(70/30); otherwise, just head.
	TruncHeadTail TruncateDirection = iota
	// TruncHead is a pure head (retaining the beginning).
	TruncHead
	// TruncTail: pure tail (retains the end).
	TruncTail
)

// TruncateOptions Truncate options. The zero field uses the default constant.
type TruncateOptions struct {
	MaxLines  *int
	MaxBytes  *int
	Direction TruncateDirection
}

// TruncateResult Truncation result.
type TruncateResult struct {
	Content    string
	Truncated  bool
	OutputPath string // If the caller places the full original text on the disk, the path is filled in for the agent to retrieve (Truncate itself does not record it).
}

// Truncate performs misperception truncation on long texts.
// Return as it is if not exceeding the limit; Over-limit handling by Direction: HeadTail (default) scans the incorrect keywords at the end to determine head+tail or pure head.
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

// scanTailForError scans the end of the text TruncateTailScanBytes bytes to check if the bytes contain the error keywords.
func scanTailForError(text string) bool {
	region := text
	if len(region) > TruncateTailScanBytes {
		region = region[len(region)-TruncateTailScanBytes:]
	}
	return truncateErrorPattern.MatchString(region)
}

// truncHead keeps the starting maxLines line (and the byte does not exceed maxBytes), and the omitted prompt is used.
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

// truncTail keeps the maxLines line at the end.
func truncTail(lines []string, maxLines, maxBytes int) TruncateResult {
	n := maxLines
	if n > len(lines) {
		n = len(lines)
	}
	tail := capBytes(strings.Join(lines[len(lines)-n:], "\n"), maxBytes)
	header := fmt.Sprintf("\n... [%d lines omitted — showing tail. Use Grep to search or Read with offset to see more]\n\n", len(lines)-n)
	return TruncateResult{Content: header + tail, Truncated: true}
}

// truncHeadTail head (70%) + tail (30%), with omitted prompts in the middle (error perception: tail error context retained).
func truncHeadTail(lines []string, maxLines, maxBytes int) TruncateResult {
	headLines := int(float64(maxLines) * TruncateHeadRatio)
	if headLines < 1 {
		headLines = 1
	}
	tailLines := maxLines - headLines
	if tailLines < 1 {
		tailLines = 1
	}
	// When the number of rows is insufficient to split, it degenerates into a half-section
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

// capBytes is truncated to maxBytes (cut at the rune boundary to avoid truncating multibyte characters).
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

// WriteToTruncationDir files the full original text into the dir and returns the file path for the agent to retrieve.
// Used when the caller (such as a bash tool) holds the context of the data directory; Truncate itself does not place bets.
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
