// token_security_test.go：read 工具的测试。
// 覆盖 isBinaryBytes（含 UTF-8 中文不误判）、writeMatchesMerged 区间合并、
// search 三种 output_mode、head_limit、isWithinResolved。
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
// isBinaryBytes 单元测试
// ============================================================================

func TestIsBinaryBytes_NUL(t *testing.T) {
	// 含 NUL 字节 → 立即判二进制
	assert.True(t, isBinaryBytes([]byte{'a', 0x00, 'b'}))
}

func TestIsBinaryBytes_HighBytesBinary(t *testing.T) {
	// C6：高位字节 0x80-0xFF 且非合法 UTF-8 → 二进制
	// 构造一段全是孤立续字节（0x80-0xBF 不能作为首字节）的二进制样本
	bin := make([]byte, 1024)
	for i := range bin {
		bin[i] = 0x80 + byte(i%0x3E) // 全部是非法 UTF-8 首字节
	}
	assert.True(t, isBinaryBytes(bin))
}

func TestIsBinaryBytes_UTF8ChineseNotBinary(t *testing.T) {
	// C6 关键：正常 UTF-8 中文（多字节序列）不应误判为二进制
	chinese := []byte("你好世界，这是一个中文测试文件。今天天气不错。" +
		strings.Repeat("中文内容测试用于验证 UTF-8 解码合法性。", 20))
	assert.False(t, isBinaryBytes(chinese), "合法 UTF-8 中文不应判为二进制")
}

func TestIsBinaryBytes_UTF8EmojiNotBinary(t *testing.T) {
	// 4 字节 UTF-8（emoji）
	emoji := []byte("Hello 🌍 🚀 🎉 — unicode test " + strings.Repeat("😀", 30))
	assert.False(t, isBinaryBytes(emoji))
}

func TestIsBinaryBytes_PlainASCII(t *testing.T) {
	assert.False(t, isBinaryBytes([]byte("plain ascii text without any issues\nnew line")))
}

func TestIsBinaryBytes_MixedChineseAndBinary(t *testing.T) {
	// 大量中文 + 少量非法字节：非打印占比 < 30% 仍判文本
	mixed := []byte(strings.Repeat("中文字符测试", 50) + "\xff\xfe\xfd")
	assert.False(t, isBinaryBytes(mixed))
}

func TestIsBinaryBytes_AllControlChars(t *testing.T) {
	// 全是控制字符（除空白）→ 二进制
	ctrl := make([]byte, 200)
	for i := range ctrl {
		ctrl[i] = 0x01 // SOH
	}
	assert.True(t, isBinaryBytes(ctrl))
}

func TestDecodeRune_LegalUTF8(t *testing.T) {
	// 直接测试 decodeRune 的边界行为
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
// writeMatchesMerged 测试（M8：相邻 match 区间去重合并）
// ============================================================================

func TestWriteMatchesMerged_NoOverlap(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	// 1-indexed matches
	var sb strings.Builder
	writeMatchesMerged(&sb, lines, []int{1, 5}, 0, 0)
	out := sb.String()
	// 0 上下文，每 match 仅 1 行
	assert.Contains(t, out, "> Line 1: a")
	assert.Contains(t, out, "> Line 5: e")
	// 不应出现重复行号
	assert.Equal(t, 1, strings.Count(out, "Line 1:"))
	assert.Equal(t, 1, strings.Count(out, "Line 5:"))
}

func TestWriteMatchesMerged_OverlappingContextDedup(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	// matches at 2 and 3，context -C 1 → 区间 [1,3] 和 [2,4] 重叠
	// 修复前会重复打印 Line 2 和 Line 3
	var sb strings.Builder
	writeMatchesMerged(&sb, lines, []int{2, 3}, 1, 1)
	out := sb.String()
	// 每个行号仅出现一次
	assert.Equal(t, 1, strings.Count(out, "Line 1:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 2:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 3:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 4:"), out)
	// 匹配行有 ">" 标记
	assert.Contains(t, out, "> Line 2: b")
	assert.Contains(t, out, "> Line 3: c")
}

func TestWriteMatchesMerged_AdjacentSpansMerge(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f", "g"}
	// matches at 1 and 5，context before=1 after=1
	// 区间 [1,2] 和 [4,6] 不重叠不邻接 → 两个独立块
	var sb strings.Builder
	writeMatchesMerged(&sb, lines, []int{1, 5}, 1, 1)
	out := sb.String()
	// 两块之间应有 "---" 分隔
	assert.Equal(t, 2, strings.Count(out, "---"))
}

// ============================================================================
// isWithinResolved 测试（C5：walk 软链逃逸 helper）
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
	// f 不在 dir1 之下
	assert.False(t, isWithinResolved(f, dir1))
}

func TestIsWithinResolved_PrefixButNotChild(t *testing.T) {
	// 防伪前缀匹配：/tmp/foo123 不应在 /tmp/foo 之下
	dir := t.TempDir()
	sibling := dir + "-sibling"
	assert.NoError(t, os.WriteFile(sibling, []byte("x"), 0644))
	assert.False(t, isWithinResolved(sibling, dir))
}

func TestIsWithinResolved_NonexistentPath(t *testing.T) {
	dir := t.TempDir()
	// 不存在的路径：EvalSymlinks 失败 → 保守返回 false
	assert.False(t, isWithinResolved(filepath.Join(dir, "nope.txt"), dir))
}

// ============================================================================
// search 端到端：output_mode 三态、head_limit、真实总数
// ============================================================================

// setupSearchTestDir 建一个临时工作区，写入若干文件供 search。
func setupSearchTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"a.txt": "foo\nbar\nfoo\nbaz\nfoo\n", // 3 个 foo
		"b.txt": "foo\nqux\n",                // 1 个 foo
		"c.md":  "bar\nbar\nbar\n",           // 0 个 foo
	}
	for name, content := range files {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	}
	// 子目录文件，验证递归
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
	// content 模式应包含 "Line N:" 行
	assert.Contains(t, out, "Line ")
	// 真实总数：a(3) + b(1) + sub/d(2) = 6
	assert.Contains(t, out, "Found 6 match(es)")
}

func TestSearch_OutputModeFilesWithMatches(t *testing.T) {
	dir := setupSearchTestDir(t)
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":        dir,
		"query":       "foo",
		"output_mode": "files_with_matches",
	})
	// files_with_matches 模式：列出匹配文件路径，不输出行内容
	assert.Contains(t, out, "a.txt")
	assert.Contains(t, out, "b.txt")
	assert.Contains(t, out, "d.txt")
	assert.NotContains(t, out, "Line ")
	// 命中 3 个文件
	assert.Contains(t, out, "in 3 file(s)")
}

func TestSearch_OutputModeCount(t *testing.T) {
	dir := setupSearchTestDir(t)
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":        dir,
		"query":       "foo",
		"output_mode": "count",
	})
	// count 模式：每个文件一行计数
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
	// 截断标记
	assert.Contains(t, out, "results limited")
	// 截断后真实总数仍为 6，不低估
	assert.Contains(t, out, "Found 6 match(es)")
	// 展示的匹配行不超过 2
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
	// 3 个匹配文件，head_limit=2 → 截断
	assert.Contains(t, out, "results limited")
	// 真实文件总数仍是 3
	assert.Contains(t, out, "in 3 file(s)")
}

func TestSearch_ContextBeforeAfterMerge(t *testing.T) {
	// 单文件含相邻 match，验证 -C 区间合并（不重复行）
	dir := t.TempDir()
	content := "l1\nl2\nfoo\nfoo\nl5\nl6\n"
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "x.txt"), []byte(content), 0644))
	out := runSearch(t, newSearchTool(t, dir), map[string]interface{}{
		"path":    dir,
		"query":   "foo",
		"context": 1,
	})
	// line 3 和 line 4 是 match；context=1 → 区间 [2,5] 合并为一个块
	// 每行号仅出现一次（M8）
	assert.Equal(t, 1, strings.Count(out, "Line 2:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 3:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 4:"), out)
	assert.Equal(t, 1, strings.Count(out, "Line 5:"), out)
}

// ============================================================================
// isBinaryFile 端到端（写真实文件，验证中文文件不被判二进制）
// ============================================================================

func TestIsBinaryFile_ChineseText(t *testing.T) {
	// 全平台：验证中文文本文件不会被二进制嗅探误判（C6 端到端）
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
		bin[i] = byte(i * 7) // 伪随机字节，含大量非 UTF-8
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
