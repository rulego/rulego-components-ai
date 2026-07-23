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

// forceFallback forces hasRipgrep to return false, ensuring the test follows the Go-safe path, and is portable to Windows/RG environments.
// Resume after the test ends.
func forceFallback(t *testing.T) {
	t.Helper()
	// Empty the PATH to let exec.LookPath("rg") failure (does not affect calls to the compiled Go standard library)
	t.Setenv("PATH", "")
	common.ResetRipgrepCache()
	t.Cleanup(func() {
		common.ResetRipgrepCache()
	})
}

// makeTempFixture constructs a test file tree in a temporary directory.
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

	// With fine mtime intervals, mtime sorting is kept observable (some platforms have low file system precision).
	touchWithOffset := func(name string, offset time.Duration) {
		full := filepath.Join(root, name)
		require.NoError(t, os.Chtimes(full, time.Now(), time.Now().Add(offset)))
	}
	// d.go is the newest, b.go is next, a.go is older, and c.txt is the oldest
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

// TestGrep_CrossDirectory Verifying ctx injection allowCrossDir:
// true allows the workDir external directory; false rejects. forceFallback uses Go as a backup and doesn't rely on ripgrep.
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

	// cross=true: Allows the workDir external directory to hit the needle
	out, err := ti.InvokableRun(common.WithAllowCrossDir(context.Background(), true), string(args))
	assert.NoError(t, err)
	assert.Contains(t, out, "needle")

	// cross=false: Rejects the workDir external directory
	_, err = ti.InvokableRun(common.WithAllowCrossDir(context.Background(), false), string(args))
	assert.Error(t, err)
}

func TestGrep_BasicMatch_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "foo",
	})
	// a.go, b.go, sub/d.go all contain foo
	assert.Contains(t, out, "a.go:")
	assert.Contains(t, out, "b.go:")
	assert.Contains(t, out, "sub/d.go:")
	assert.Contains(t, out, "Found")
	// It should include a prefix that matches the line number
	assert.Contains(t, out, "Line ")
}

func TestGrep_IncludeGlob_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	out := runGrep(t, root, map[string]interface{}{
		"pattern": "hello",
		"include": "*.go",
	})
	assert.Contains(t, out, "a.go") // a.go is.go
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
	// It should not contain line content, only file paths
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
	// a.go contains 1 foo, b.go contains 1 foo, sub/d.go contains 1 foo
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
	// a.go: Line 1 "hello world", line 2 "foo bar" (match), -A=1 should lead the following line (this is the last line)
	// The main validation includes matching tags "> " separated from context
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

	// sub/d.go: Line 1 "deep foo", line 2 "nested line" — matches line 1, -C=1 should include line 2 at the same time
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
	// FOO appears in three matches: a.go(1), b.go(1), sub/d.go(1), head_limit=2, truncated
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
	// head_limit constructed exceeding the hard upper limit should be cut off to the hard upper limit
	out := runGrep(t, root, map[string]interface{}{
		"pattern":    "foo",
		"head_limit": 99999,
	})
	// No need to assert specific values; just don't panic and include the result (foo hits are much less than 500).
	assert.Contains(t, out, "Found")
}

func TestGrep_MtimeSort_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)

	// files_with_matches Facilitates file extraction order
	out := runGrep(t, root, map[string]interface{}{
		"pattern":       "foo", // a.go, b.go, sub/d.go
		"output_mode":   "files_with_matches",
		"sort_by_mtime": true,
	})
	// mtime latest -> oldest: sub/d.go(-1s), b.go(-2s), a.go(-3s)
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

	// Specify the path to a single file
	single := filepath.Join(root, "a.go")
	out := runGrep(t, root, map[string]interface{}{
		"pattern": "hello",
		"path":    single,
	})
	assert.Contains(t, out, "hello world")
	// No other documents should be found
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
	// Path outbounds return with Go error (same as read tool)
	require.Error(t, runErr)
	upperErr := strings.ToUpper(runErr.Error())
	assert.True(t,
		strings.Contains(upperErr, "ESCAPE") || strings.Contains(upperErr, "PATH"),
		"expected path escape error, got: %v (out=%s)", runErr, out)
}

// Compile Time Assertion: The Catch-All Path is actually executed in RG-free environments (Windows-friendly).
func TestGrep_FallbackIsReachable(t *testing.T) {
	forceFallback(t)
	require.False(t, common.HasRipgrep(), "HasRipgrep should be false after forceFallback")
	// After resetting, the next probe should return to the true state (false when CI has no RG, true when local RG is present).
	common.ResetRipgrepCache()
	_ = common.HasRipgrep()
	// Tag Under Windows, we know that RG detection is sensitive to PATH
	_ = runtime.GOOS
}

// TestGrep_RipgrepPath Override ripgrep priority paths (only executed when RG is installed in the real environment,
// Otherwise, t.Skip ensures the CI does not hang in an RG environment).
func TestGrep_RipgrepPath(t *testing.T) {
	common.ResetRipgrepCache()
	if !common.HasRipgrep() {
		t.Skip("ripgrep not installed in real PATH, skipping ripgrep-first path test")
	}
	root := makeTempFixture(t)
	out := runGrep(t, root, map[string]interface{}{
		"pattern": "foo",
	})
	// The ripgrep path should also hit three files
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "b.go")
	assert.Contains(t, out, "sub/d.go")
}

// ---- Regular syntax boundaries (Go as a safeguard, regexp/RE2)----

func TestGrep_RegexAnchors_Fallback(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello world\nworld hello\n"), 0644))
	// ^world only matches the world at the beginning of the line (line 2); the world at the end of the line 1 does not match
	out := runGrep(t, root, map[string]interface{}{"pattern": "^world"})
	assert.Contains(t, out, "world hello")
	assert.NotContains(t, out, "hello world")
}

func TestGrep_RegexAlternation_Fallback(t *testing.T) {
	forceFallback(t)
	root := makeTempFixture(t)
	// Hello|second: a.go contains hello, b.go includes second, sub/d.go does not include it
	out := runGrep(t, root, map[string]interface{}{"pattern": "hello|second"})
	assert.Contains(t, out, "a.go")
	assert.Contains(t, out, "b.go")
	assert.NotContains(t, out, "sub/d.go")
}

func TestGrep_CaseSensitive_Fallback(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("Foo\nfoo\n"), 0644))
	// Default case-sensitive: Foo only matches line 1
	out := runGrep(t, root, map[string]interface{}{"pattern": "Foo"})
	assert.Contains(t, out, "> Line 1: Foo")
	assert.NotContains(t, out, "> Line 2")
	// (?i) Insensitive: Both lines match
	out2 := runGrep(t, root, map[string]interface{}{"pattern": "(?i)foo"})
	assert.Contains(t, out2, "> Line 1: Foo")
	assert.Contains(t, out2, "> Line 2: foo")
}

func TestGrep_RegexCharClass_Fallback(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("cat\ncot\ncut\ndog\n"), 0644))
	// c[aou]t matches cat/cot/cut, not dog
	out := runGrep(t, root, map[string]interface{}{"pattern": "c[aou]t"})
	assert.Contains(t, out, "cat")
	assert.Contains(t, out, "cot")
	assert.Contains(t, out, "cut")
	assert.NotContains(t, out, "dog")
}

// ---- Gitignore + binary (aligned rg)----

func TestGrep_Gitignore_Fallback(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.go"), []byte("needle\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.log"), []byte("needle\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0644))

	out := runGrep(t, root, map[string]interface{}{"pattern": "needle"})
	assert.Contains(t, out, "keep.go")
	assert.NotContains(t, out, "ignored.log") // Skipped by.gitignore
}

func TestGrep_BinaryFile_Fallback(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	// text.go is normal; bin.bin Contains NUL (binary), both containing "nee"
	require.NoError(t, os.WriteFile(filepath.Join(root, "text.go"), []byte("needle\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin.bin"), []byte("nee\x00dle\n"), 0644))

	out := runGrep(t, root, map[string]interface{}{"pattern": "nee"})
	assert.Contains(t, out, "text.go")
	assert.NotContains(t, out, "bin.bin") // Binary skipped
}

// ---- Other boundaries: Unicode/empty file/count Multiple matches ----

func TestGrep_Unicode_Fallback(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "cn.go"), []byte("你好世界\n函数 main\n"), 0644))
	out := runGrep(t, root, map[string]interface{}{"pattern": "函数"})
	assert.Contains(t, out, "函数 main")
}

func TestGrep_EmptyFile_Fallback(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "empty.go"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "has.go"), []byte("needle\n"), 0644))
	out := runGrep(t, root, map[string]interface{}{"pattern": "needle"})
	assert.Contains(t, out, "has.go")
	assert.NotContains(t, out, "empty.go") // Empty file mismatch
}

func TestGrep_CountMultiple_Fallback(t *testing.T) {
	forceFallback(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("foo\nfoo\nbar\nfoo\n"), 0644))
	out := runGrep(t, root, map[string]interface{}{"pattern": "foo", "output_mode": "count"})
	assert.Contains(t, out, "a.go: 3") // One file has multiple hits, count=3
}
