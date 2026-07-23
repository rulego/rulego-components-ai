// token_security_test.go:read Testing tool.
// Override isBinaryBytes (including UTF-8 Chinese without false positives), writeMatchesMerged range merging,
// search three types: output_mode, head_limit, and isWithinResolved.
package read

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
)

// ============================================================================
// isBinaryBytes unit test
// ============================================================================

func TestIsBinaryBytes_NUL(t *testing.T) {
	// Contains NUL bytes → instantly binary
	assert.True(t, isBinaryBytes([]byte{'a', 0x00, 'b'}))
}

func TestIsBinaryBytes_HighBytesBinary(t *testing.T) {
	// C6: High byte 0x80-0xFF and non-valid UTF-8 → binary
	// Construct a binary sample that is entirely isolated continuous bytes (0x80-0xBF cannot be used as the first byte).
	bin := make([]byte, 1024)
	for i := range bin {
		bin[i] = 0x80 + byte(i%0x3E) // All are illegal UTF-8 first bytes
	}
	assert.True(t, isBinaryBytes(bin))
}

func TestIsBinaryBytes_UTF8ChineseNotBinary(t *testing.T) {
	// C6 Key: Normal UTF-8 Chinese (multibyte sequence) should not be misidentified as binary
	chinese := []byte("你好世界，这是一个中文测试文件。今天天气不错。" +
		strings.Repeat("中文内容测试用于验证 UTF-8 解码合法性。", 20))
	assert.False(t, isBinaryBytes(chinese), "合法 UTF-8 中文不应判为二进制")
}

func TestIsBinaryBytes_UTF8EmojiNotBinary(t *testing.T) {
	// 4 bytes UTF-8 (emoji)
	emoji := []byte("Hello 🌍 🚀 🎉 — unicode test " + strings.Repeat("😀", 30))
	assert.False(t, isBinaryBytes(emoji))
}

func TestIsBinaryBytes_PlainASCII(t *testing.T) {
	assert.False(t, isBinaryBytes([]byte("plain ascii text without any issues\nnew line")))
}

func TestIsBinaryBytes_MixedChineseAndBinary(t *testing.T) {
	// Large amounts of Chinese + a small number of illegal bytes: Non-printed accounts for <30%, still classified as text
	mixed := []byte(strings.Repeat("中文字符测试", 50) + "\xff\xfe\xfd")
	assert.False(t, isBinaryBytes(mixed))
}

func TestIsBinaryBytes_AllControlChars(t *testing.T) {
	// All control characters (excluding spaces) → binary
	ctrl := make([]byte, 200)
	for i := range ctrl {
		ctrl[i] = 0x01 // SOH
	}
	assert.True(t, isBinaryBytes(ctrl))
}

func TestDecodeRune_LegalUTF8(t *testing.T) {
	// Directly test the boundary behavior of decodeRune
	cases := []struct {
		name    string
		in      []byte
		wantR   rune
		wantLen int
	}{
		{"ascii", []byte{'A'}, 'A', 1},
		{"2-byte", []byte{0xC2, 0x80}, 0x80, 2},
		{"3-byte chinese", []byte{0xE4, 0xB8, 0xAD}, '中', 3},
		{"illegal continuation as lead", []byte{0x80}, 0xFFFD, 1},
		{"illegal lead 0xC1", []byte{0xC1, 0x80}, 0xFFFD, 1},
		{"out of range 0xF5", []byte{0xF5, 0x80, 0x80, 0x80}, 0xFFFD, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, n := decodeRune(c.in)
			assert.Equal(t, c.wantR, r)
			assert.Equal(t, c.wantLen, n)
		})
	}
}

// ============================================================================
// writeMatchesMerged test (M8: Deduplicate merging of adjacent match intervals)
// ============================================================================

func TestWriteMatchesMerged_NoOverlap(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	// 1-indexed matches
	var sb strings.Builder
	writeMatchesMerged(&sb, lines, []int{1, 5}, 0, 0)
	out := sb.String()
	// 0 context, only 1 line per match
	assert.Contains(t, out, "> Line 1: a")
	assert.Contains(t, out, "> Line 5: e")
	// Duplicate line numbers should not occur
	assert.Equal(t, 1, strings.Count(out, "Line 1:"))
	assert.Equal(t, 1, strings.Count(out, "Line 5:"))
}

func TestWriteMatchesMerged_OverlappingContextDedup(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	// Matches at 2 and 3, context -C 1 → overlaps between [1,3] and [2,4].
	// Before repairs, Line 2 and Line 3 are printed repeatedly
	var sb strings.Builder
	writeMatchesMerged(&sb, lines, []int{2, 3}, 1, 1)
	out := sb.String()
	// Each line number appears only once
	assert.Equal(t, 1, strings.Count(out, "Line 1:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 2:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 3:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 4:"), out)
	// The matching line is marked with a ">"
	assert.Contains(t, out, "> Line 2: b")
	assert.Contains(t, out, "> Line 3: c")
}

func TestWriteMatchesMerged_AdjacentSpansMerge(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f", "g"}
	// matches at 1 and 5，context before=1 after=1
	// Intervals [1,2] and [4,6] do not overlap or adjacently → two independent blocks
	var sb strings.Builder
	writeMatchesMerged(&sb, lines, []int{1, 5}, 1, 1)
	out := sb.String()
	// There should be a "---" separation between the two sections
	assert.Equal(t, 2, strings.Count(out, "---"))
}

// ============================================================================
// isWithinResolved Test (C5: Soft Chain Escape Helper)
// ============================================================================

func TestIsWithinResolved_SamePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	assert.NoError(t, os.WriteFile(f, []byte("x"), 0644))
	assert.True(t, isWithinResolved(f, dir))
}

func TestIsWithinResolved_Subdirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub", "deep", "a.txt")
	assert.NoError(t, os.MkdirAll(filepath.Dir(sub), 0755))
	assert.NoError(t, os.WriteFile(sub, []byte("x"), 0644))
	assert.True(t, isWithinResolved(sub, dir))
}

func TestIsWithinResolved_OutsideEscape(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	f := filepath.Join(dir2, "secret.txt")
	assert.NoError(t, os.WriteFile(f, []byte("x"), 0644))
	// f is not below dir1
	assert.False(t, isWithinResolved(f, dir1))
}

func TestIsWithinResolved_PrefixButNotChild(t *testing.T) {
	// Anti-counterfeit prefix matching: /tmp/foo123 should not be below /tmp/foo
	dir := t.TempDir()
	sibling := dir + "-sibling"
	assert.NoError(t, os.WriteFile(sibling, []byte("x"), 0644))
	assert.False(t, isWithinResolved(sibling, dir))
}

func TestIsWithinResolved_NonexistentPath(t *testing.T) {
	dir := t.TempDir()
	// Nonexistent path: EvalSymlinks failed → conservatively returns false
	assert.False(t, isWithinResolved(filepath.Join(dir, "nope.txt"), dir))
}

// ============================================================================
// Search end-to-end: output_mode Three states, head_limit, and true total
// ============================================================================

// setupSearchTestDir creates a temporary workspace and writes several files for searching.
func setupSearchTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"a.txt": "foo\nbar\nfoo\nbaz\nfoo\n", // 3 foo
		"b.txt": "foo\nqux\n",                // 1 foo
		"c.md":  "bar\nbar\nbar\n",           // 0 foo
	}
	for name, content := range files {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	}
	// Subdirectory files to verify recursion
	assert.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "d.txt"), []byte("foo\nfoo\n"), 0644))
	return dir
}

func newSearchTool(t *testing.T, workDir string) tool.BaseTool {
	t.Helper()
	rTool, err := NewTool(Config{WorkDir: workDir, MaxReadLines: 1000, MaxSearchResults: 30})
	assert.NoError(t, err)
	return rTool
}

func runSearch(t *testing.T, rTool tool.BaseTool, params map[string]interface{}) string {
	t.Helper()
	params["operation"] = "search"
	b, err := json.Marshal(params)
	assert.NoError(t, err)
	runner := rTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})
	out, err := runner.InvokableRun(context.Background(), string(b))
	assert.NoError(t, err)
	return out
}

func TestSearch_OutputModeContent(t *testing.T) {
	dir := setupSearchTestDir(t)
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":        dir,
		"query":       "foo",
		"output_mode": "content",
	})
	// The content mode should include the "Line N:" line
	assert.Contains(t, out, "Line ")
	// True total: a(3) + b(1) + sub/d(2) = 6
	assert.Contains(t, out, "Found 6 match(es)")
}

func TestSearch_OutputModeFilesWithMatches(t *testing.T) {
	dir := setupSearchTestDir(t)
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":        dir,
		"query":       "foo",
		"output_mode": "files_with_matches",
	})
	// files_with_matches Mode: Lists matching file paths without outputting line content
	assert.Contains(t, out, "a.txt")
	assert.Contains(t, out, "b.txt")
	assert.Contains(t, out, "d.txt")
	assert.NotContains(t, out, "Line ")
	// Hit 3 files
	assert.Contains(t, out, "in 3 file(s)")
}

func TestSearch_OutputModeCount(t *testing.T) {
	dir := setupSearchTestDir(t)
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":        dir,
		"query":       "foo",
		"output_mode": "count",
	})
	// count mode: counts one line per file
	assert.Contains(t, out, "a.txt: 3")
	assert.Contains(t, out, "b.txt: 1")
	assert.Contains(t, out, "d.txt: 2")
}

func TestSearch_HeadLimitTruncates(t *testing.T) {
	dir := setupSearchTestDir(t)
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":        dir,
		"query":       "foo",
		"output_mode": "content",
		"head_limit":  2,
	})
	// Truncation marks
	assert.Contains(t, out, "results limited")
	// After truncation, the true total remains 6, which is not underestimated
	assert.Contains(t, out, "Found 6 match(es)")
	// No more than 2 matching rows are shown
	assert.Equal(t, 2, strings.Count(out, "> Line "), out)
}

func TestSearch_HeadLimitFilesWithMatches(t *testing.T) {
	dir := setupSearchTestDir(t)
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":        dir,
		"query":       "foo",
		"output_mode": "files_with_matches",
		"head_limit":  2,
	})
	// 3 matching files, head_limit=2 → truncated
	assert.Contains(t, out, "results limited")
	// The total number of real files remains 3
	assert.Contains(t, out, "in 3 file(s)")
}

func TestSearch_ContextBeforeAfterMerge(t *testing.T) {
	// Single file contains adjacent matches, verify -C interval merging (no duplicate lines)
	dir := t.TempDir()
	content := "l1\nl2\nfoo\nfoo\nl5\nl6\n"
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "x.txt"), []byte(content), 0644))
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":    dir,
		"query":   "foo",
		"context": 1,
	})
	// Line 3 and Line 4 are matched; context=1 → The interval [2,5] is merged into one block
	// Each line number appears only once (M8)
	assert.Equal(t, 1, strings.Count(out, "Line 2:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 3:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 4:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 5:"), out)
}

// ============================================================================
// isBinaryFile end-to-end (authentic file verification, verifying Chinese files is not judged as binary)
// ============================================================================

func TestIsBinaryFile_ChineseText(t *testing.T) {
	// Omniplatform: Ensures Chinese text files won't be misjudged by binary sniffing (C6 end-to-end)
	dir := t.TempDir()
	f := filepath.Join(dir, "zh.txt")
	assert.NoError(t, os.WriteFile(f, []byte(strings.Repeat("中文测试文件内容，用于验证不会被误判。", 20)), 0644))
	assert.False(t, isBinaryFile(f))
}

func TestIsBinaryFile_ActualBinary(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data.bin")
	bin := make([]byte, 4096)
	for i := range bin {
		bin[i] = byte(i * 7) // Pseudo-random bytes, containing a large amount of non-UTF-8 data
	}
	assert.NoError(t, os.WriteFile(f, bin, 0644))
	assert.True(t, isBinaryFile(f))
}

func TestIsBinaryFile_ExtBlacklist(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "image.png")
	assert.NoError(t, os.WriteFile(f, []byte("not really png but ext blacklisted"), 0644))
	assert.True(t, isBinaryFile(f), "扩展名黑名单命中即判二进制")
}
