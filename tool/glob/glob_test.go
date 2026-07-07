package glob

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rulego/rulego-components-ai/tool/common"
)

// forceFallback 强制 hasRipgrep 返回 false，保证测试走 Go 兜底路径。
func forceFallback(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
	common.ResetRipgrepCache()
	t.Cleanup(func() {
		common.ResetRipgrepCache()
	})
}

// makeTempFixture 构造测试文件树。
//
//	root/
//	  a.go
//	  b.go
//	  c.txt
//	  sub/
//	    d.go
//	    e.txt
func makeTempFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(name, content string) {
		full := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	mustWrite("a.go", "x")
	mustWrite("b.go", "x")
	mustWrite("c.txt", "x")
	mustWrite("sub/d.go", "x")
	mustWrite("sub/e.txt", "x")

	// mtime 间隔，d.go 最新 -> e.txt 最旧
	touchWithOffset := func(name string, offset time.Duration) {
		full := filepath.Join(root, name)
		require.NoError(t, os.Chtimes(full, time.Now(), time.Now().Add(offset)))
	}
	touchWithOffset("a.go", -3*time.Second)
	touchWithOffset("b.go", -2*time.Second)
	touchWithOffset("c.txt", -4*time.Second)
	touchWithOffset("sub/d.go", -1*time.Second)
	touchWithOffset("sub/e.txt", -5*time.Second)
	return root
}

func runGlob(t *testing.T, root string, params map[string]interface{}) string {
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

// TestGlob_CrossDirectory 验证 ctx 注入 allowCrossDir：
// true 放行 workDir 外目录，false 拒绝。forceFallback 走 Go 兜底。
func TestGlob_CrossDirectory(t *testing.T) {
	forceFallback(t)
	workDir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "findme.txt"), []byte("x"), 0644))

	tt, err := NewTool(Config{WorkDir: workDir})
	require.NoError(t, err)
	ti := tt.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})
	args, _ := json.Marshal(map[string]string{"pattern": "*.txt", "path": outside})

	// cross=true：放行 workDir 外目录，匹配到 findme.txt
	out, err := ti.InvokableRun(common.WithAllowCrossDir(context.Background(), true), string(args))
	assert.NoError(t, err)
	assert.Contains(t, out, "findme.txt")

	// cross=false：拒绝 workDir 外目录
	_, err = ti.InvokableRun(common.WithAllowCrossDir(context.Background(), false), string(args))
	assert.Error(t, err)
}

func TestGlob_BasicPattern_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGlob(t, root, map[string]interface{}{
		"pattern": "*.go",
	})
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "b.go")
	assert.NotContains(t, out, "c.txt")
	// 顶层 *.go 不应包含 sub/ 下
	assert.NotContains(t, out, "sub/d.go")
}

func TestGlob_DoubleStar_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGlob(t, root, map[string]interface{}{
		"pattern": "**/*.go",
	})
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "b.go")
	assert.Contains(t, out, "sub/d.go")
	assert.NotContains(t, out, "c.txt")
	assert.NotContains(t, out, "sub/e.txt")
}

func TestGlob_RecursiveTxt_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGlob(t, root, map[string]interface{}{
		"pattern": "**/*.txt",
	})
	assert.Contains(t, out, "c.txt")
	assert.Contains(t, out, "sub/e.txt")
	assert.NotContains(t, out, ".go")
}

func TestGlob_HeadLimit_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGlob(t, root, map[string]interface{}{
		"pattern":    "**/*",
		"head_limit": 2,
	})
	assert.Contains(t, out, "head_limit=2")
}

func TestGlob_HardMaxLimit(t *testing.T) {
	forceFallback(t)
	tt, err := NewTool(Config{HardMaxLimit: 500})
	require.NoError(t, err)
	gt := tt.(*globTool)
	assert.Equal(t, 500, gt.config.HardMaxLimit)
}

func TestGlob_MtimeSort_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGlob(t, root, map[string]interface{}{
		"pattern":       "**/*",
		"sort_by_mtime": true,
	})
	// mtime 最新 -> 最旧：sub/d.go(-1s), b.go(-2s), a.go(-3s), c.txt(-4s), sub/e.txt(-5s)
	order := []string{"sub/d.go", "b.go", "a.go", "c.txt", "sub/e.txt"}
	prev := -1
	for _, name := range order {
		idx := strings.Index(out, name)
		require.NotEqual(t, -1, idx, "missing %s in output:\n%s", name, out)
		assert.Greater(t, idx, prev, "%s should appear after previous (mtime order broken):\n%s", name, out)
		prev = idx
	}
}

func TestGlob_MtimeSortDisabled_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGlob(t, root, map[string]interface{}{
		"pattern":       "*.go",
		"sort_by_mtime": false,
	})
	// 关闭 mtime 排序后顺序由 WalkDir 决定（a.go 通常在前），不强制断言具体顺序，
	// 只验证两个文件都出现
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "b.go")
}

func TestGlob_EmptyResult_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGlob(t, root, map[string]interface{}{
		"pattern": "**/*.md",
	})
	assert.Contains(t, out, "Found 0 file")
}

func TestGlob_EmptyPattern(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	out := runGlob(t, root, map[string]interface{}{
		"pattern": "",
	})
	assert.Contains(t, out, "query cannot be empty")
}

func TestGlob_PathIsFile_Rejected(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)
	single := filepath.Join(root, "a.go")
	out := runGlob(t, root, map[string]interface{}{
		"pattern": "*.go",
		"path":    single,
	})
	// path 必须是目录，传入文件应报错
	upperOut := strings.ToUpper(out)
	assert.True(t,
		strings.Contains(upperOut, "DIRECTORY") || strings.Contains(upperOut, "ERROR"),
		"expected directory error, got: %s", out)
}

func TestGlob_FallbackIsReachable(t *testing.T) {
	forceFallback(t)
	require.False(t, common.HasRipgrep(), "HasRipgrep should be false after forceFallback")
}

// TestGlob_RipgrepPath 覆盖 glob 的 rg --files 路径（环境装了 rg 才执行）。
func TestGlob_RipgrepPath(t *testing.T) {
	common.ResetRipgrepCache()
	if !common.HasRipgrep() {
		t.Skip("ripgrep not installed in real PATH, skipping ripgrep-first path test")
	}
	root := makeTempFixture(t)
	out := runGlob(t, root, map[string]interface{}{
		"pattern": "**/*.go",
	})
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "sub/d.go")
}
