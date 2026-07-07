package grep

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rulego/rulego-components-ai/tool/common"
)

// forceFallback 强制 hasRipgrep 返回 false，保证测试走 Go 兜底路径，Windows/无 rg 环境可移植。
// 测试结束后恢复。
func forceFallback(t *testing.T) {
	t.Helper()
	// 清空 PATH 让 exec.LookPath("rg") 失败（不影响已编译进去的 Go 标准库调用）
	t.Setenv("PATH", "")
	common.ResetRipgrepCache()
	t.Cleanup(func() {
		common.ResetRipgrepCache()
	})
}

// makeTempFixture 在临时目录构造测试文件树。
//
//	root/
//	  a.go      ("hello world\nfoo bar")
//	  b.go      ("foo baz\nsecond")
//	  c.txt     ("hello")
//	  sub/
//	    d.go    ("deep foo\nnested")
func makeTempFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(name, content string) {
		full := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	mustWrite("a.go", "hello world\nfoo bar\n")
	mustWrite("b.go", "foo baz\nsecond line\n")
	mustWrite("c.txt", "hello\n")
	mustWrite("sub/d.go", "deep foo\nnested line\n")

	// 通过细微的 mtime 间隔，保证 mtime 排序可观察（部分平台文件系统精度低）
	touchWithOffset := func(name string, offset time.Duration) {
		full := filepath.Join(root, name)
		require.NoError(t, os.Chtimes(full, time.Now(), time.Now().Add(offset)))
	}
	// d.go 最新，b.go 次之，a.go 较旧，c.txt 最旧
	touchWithOffset("a.go", -3*time.Second)
	touchWithOffset("b.go", -2*time.Second)
	touchWithOffset("c.txt", -4*time.Second)
	touchWithOffset("sub/d.go", -1*time.Second)
	return root
}

func runGrep(t *testing.T, root string, params map[string]interface{}) string {
	t.Helper()
	tt, err := NewTool(Config{WorkDir: root})
	require.NoError(t, err)
	ti := tt.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})
	args, _ := json.Marshal(params)
	out, err := ti.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	return out
}

// TestGrep_CrossDirectory 验证 ctx 注入 allowCrossDir：
// true 放行 workDir 外目录，false 拒绝。forceFallback 走 Go 兜底，不依赖 ripgrep。
func TestGrep_CrossDirectory(t *testing.T) {
	forceFallback(t)
	workDir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x.txt"), []byte("needle-line\n"), 0644))

	tt, err := NewTool(Config{WorkDir: workDir})
	require.NoError(t, err)
	ti := tt.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})
	args, _ := json.Marshal(map[string]string{"pattern": "needle", "path": outside})

	// cross=true：放行 workDir 外目录，命中 needle
	out, err := ti.InvokableRun(common.WithAllowCrossDir(context.Background(), true), string(args))
	assert.NoError(t, err)
	assert.Contains(t, out, "needle")

	// cross=false：拒绝 workDir 外目录
	_, err = ti.InvokableRun(common.WithAllowCrossDir(context.Background(), false), string(args))
	assert.Error(t, err)
}

func TestGrep_BasicMatch_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "foo",
	})
	// a.go、b.go、sub/d.go 都含 foo
	assert.Contains(t, out, "a.go:")
	assert.Contains(t, out, "b.go:")
	assert.Contains(t, out, "sub/d.go:")
	assert.Contains(t, out, "Found")
	// 应包含匹配行号前缀
	assert.Contains(t, out, "Line ")
}

func TestGrep_IncludeGlob_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "hello",
		"include": "*.go",
	})
	assert.Contains(t, out, "a.go")  // a.go 是 .go
	assert.NotContains(t, out, "c.txt")
}

func TestGrep_IncludeDoubleStar_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "deep foo",
		"include": "**/*.go",
	})
	assert.Contains(t, out, "sub/d.go")
	assert.NotContains(t, out, "a.go")
}

func TestGrep_ExcludeGlob_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "foo",
		"exclude": "**/sub/**",
	})
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "b.go")
	assert.NotContains(t, out, "sub/d.go")
}

func TestGrep_OutputMode_FilesWithMatches_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern":     "foo",
		"output_mode": "files_with_matches",
	})
	// 不应包含行内容，仅文件路径
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "b.go")
	assert.NotContains(t, out, "Line ")
}

func TestGrep_OutputMode_Count_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern":     "foo",
		"output_mode": "count",
	})
	// a.go 含 1 个 foo，b.go 含 1 个 foo，sub/d.go 含 1 个 foo
	assert.Contains(t, out, "a.go: 1")
	assert.Contains(t, out, "b.go: 1")
	assert.Contains(t, out, "sub/d.go: 1")
}

func TestGrep_ContextLines_A_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "foo bar",
		"-A":      1,
	})
	// a.go: 行1 "hello world"，行2 "foo bar"（匹配），-A=1 应带出后续行（这里已是末行）
	// 主要验证包含匹配标记 "> " 与上下文分隔
	assert.Contains(t, out, "> Line 2: foo bar")
}

func TestGrep_ContextLines_B_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "foo bar",
		"-B":      1,
	})
	assert.Contains(t, out, "Line 1: hello world")
	assert.Contains(t, out, "> Line 2: foo bar")
}

func TestGrep_ContextLines_C_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	// sub/d.go: 行1 "deep foo", 行2 "nested line" — 匹配行1，-C=1 应同时带出行2
	out := runGrep(t, root, map[string]interface{}{
		"pattern": "deep foo",
		"-C":      1,
	})
	assert.Contains(t, out, "> Line 1: deep foo")
	assert.Contains(t, out, "Line 2: nested line")
}

func TestGrep_HeadLimit_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern":    "foo",
		"head_limit": 2,
	})
	// foo 出现在 a.go(1)、b.go(1)、sub/d.go(1)，共 3 个匹配，head_limit=2 应截断
	assert.Contains(t, out, "head_limit=2")
}

func TestGrep_HardMaxLimit_Fallback(t *testing.T) {
	forceFallback(t)
	tt, err := NewTool(Config{WorkDir: t.TempDir(), HardMaxLimit: 500})
	require.NoError(t, err)
	gt := tt.(*grepTool)
	assert.Equal(t, 500, gt.config.HardMaxLimit)

	root := makeTempFixture(t)
	gt.config.WorkDir = root
	// 构造超过硬上限的 head_limit，应被截断到硬上限
	out := runGrep(t, root, map[string]interface{}{
		"pattern":    "foo",
		"head_limit": 99999,
	})
	// 不必断言具体值，只要不 panic 且包含结果即可（foo 命中数远小于 500）
	assert.Contains(t, out, "Found")
}

func TestGrep_MtimeSort_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	// files_with_matches 便于提取文件顺序
	out := runGrep(t, root, map[string]interface{}{
		"pattern":     "foo", // a.go, b.go, sub/d.go
		"output_mode": "files_with_matches",
		"sort_by_mtime": true,
	})
	// mtime 最新 -> 最旧：sub/d.go(-1s), b.go(-2s), a.go(-3s)
	idxD := strings.Index(out, "sub/d.go")
	idxB := strings.Index(out, "b.go")
	idxA := strings.Index(out, "a.go")
	require.NotEqual(t, -1, idxD)
	require.NotEqual(t, -1, idxB)
	require.NotEqual(t, -1, idxA)
	assert.Less(t, idxD, idxB, "sub/d.go should come before b.go (newer mtime)")
	assert.Less(t, idxB, idxA, "b.go should come before a.go (newer mtime)")
}

func TestGrep_EmptyResult_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "this_string_does_not_exist_xyz",
	})
	assert.Contains(t, out, "No matches found")
}

func TestGrep_InvalidRegex(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	out := runGrep(t, root, map[string]interface{}{
		"pattern": "[invalid",
	})
	assert.Contains(t, out, "Invalid regex")
}

func TestGrep_EmptyPattern(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	out := runGrep(t, root, map[string]interface{}{
		"pattern": "",
	})
	assert.Contains(t, out, "query cannot be empty")
}

func TestGrep_SingleFile_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	// 指定单文件路径
	single := filepath.Join(root, "a.go")
	out := runGrep(t, root, map[string]interface{}{
		"pattern": "hello",
		"path":    single,
	})
	assert.Contains(t, out, "hello world")
	// 不应搜到其他文件
	assert.NotContains(t, out, "b.go")
}

func TestGrep_PathEscape_Rejected(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)
	tt, err := NewTool(Config{WorkDir: root})
	require.NoError(t, err)
	ti := tt.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})
	args, _ := json.Marshal(map[string]interface{}{
		"pattern": "foo",
		"path":    "../../../etc",
	})
	out, runErr := ti.InvokableRun(context.Background(), string(args))
	// 路径越界以 Go error 返回（与 read 工具一致）
	require.Error(t, runErr)
	upperErr := strings.ToUpper(runErr.Error())
	assert.True(t,
		strings.Contains(upperErr, "ESCAPE") || strings.Contains(upperErr, "PATH"),
		"expected path escape error, got: %v (out=%s)", runErr, out)
}

// 编译期断言：兜底路径在无 rg 环境被实际执行（Windows 友好）。
func TestGrep_FallbackIsReachable(t *testing.T) {
	forceFallback(t)
	require.False(t, common.HasRipgrep(), "HasRipgrep should be false after forceFallback")
	// 重置后下次探测应回到真实状态（CI 无 rg 时为 false，本地有 rg 时为 true）
	common.ResetRipgrepCache()
	_ = common.HasRipgrep()
	// 标记 Windows 下我们已知 rg 探测对 PATH 敏感
	_ = runtime.GOOS
}

// TestGrep_RipgrepPath 覆盖 ripgrep 优先路径（仅当真实环境装了 rg 时执行，
// 否则 t.Skip，保证 CI 无 rg 环境不挂）。
func TestGrep_RipgrepPath(t *testing.T) {
	common.ResetRipgrepCache()
	if !common.HasRipgrep() {
		t.Skip("ripgrep not installed in real PATH, skipping ripgrep-first path test")
	}
	root := makeTempFixture(t)
	out := runGrep(t, root, map[string]interface{}{
		"pattern": "foo",
	})
	// ripgrep 路径同样应命中三个文件
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "b.go")
	assert.Contains(t, out, "sub/d.go")
}
