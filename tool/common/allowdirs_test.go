package common

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllowDirsCtx 验证 WithAllowDirs 注入 / AllowDirsFromCtx 读取（含 nil 情况）。
func TestAllowDirsCtx(t *testing.T) {
	// 未注入 → nil
	if got := AllowDirsFromCtx(context.Background()); got != nil {
		t.Fatalf("未注入 ctx 应返回 nil，got %v", got)
	}

	// 注入 → 读回相同切片
	dirs := []string{"/tmp/a", "/tmp/b"}
	ctx := WithAllowDirs(context.Background(), dirs)
	got := AllowDirsFromCtx(ctx)
	assert.Equal(t, dirs, got, "注入后应读回相同的 allowDirs 切片")

	// 注入空切片（非 nil）→ 读回 len=0 切片（GetWithAllowDirs 用 len 退化为 Get）
	emptyCtx := WithAllowDirs(context.Background(), []string{})
	if got := AllowDirsFromCtx(emptyCtx); len(got) != 0 {
		t.Fatalf("空切片应读回 len=0，got %v", got)
	}
}

// TestSecurePathResolver_AllowDirs 验证多根判定：path 在 workDir 主根 OR 任一 allowDir 内放行，越界拒。
func TestSecurePathResolver_AllowDirs(t *testing.T) {
	A := t.TempDir() // 主根 workDir
	B := t.TempDir() // 额外允许目录（allowDir）
	C := t.TempDir() // 越界目标（A/B 之外）

	// 在 B 下放一个文件，用于测绝对路径放行
	bFile := filepath.Join(B, "in-b.txt")
	require.NoError(t, os.WriteFile(bFile, []byte("x"), 0644))

	// 在 C 下放一个文件，用于测越界拒
	cFile := filepath.Join(C, "in-c.txt")
	require.NoError(t, os.WriteFile(cFile, []byte("x"), 0644))

	t.Run("multi-root 放行与越界拒", func(t *testing.T) {
		cfg := DefaultPathSecurityConfig()
		cfg.AllowDirs = []string{B}
		r, err := NewSecurePathResolver(A, cfg)
		require.NoError(t, err)

		// 主根 A 内：相对路径 a.txt → 解析到 A/a.txt，放行
		got, err := r.Resolve("a.txt")
		assert.NoError(t, err, "主根 A 内的相对路径应放行")
		assert.Equal(t, filepath.Join(A, "a.txt"), got)

		// allowDir B 内：绝对路径 bFile 放行
		got, err = r.Resolve(bFile)
		assert.NoError(t, err, "allowDir B 内的绝对路径应放行")
		assert.Equal(t, bFile, got)

		// path == allowDir 本身（B）放行
		got, err = r.Resolve(B)
		assert.NoError(t, err, "path == allowDir 本身应放行")
		assert.Equal(t, B, got)

		// 越界：C 内绝对路径拒（C 在 A/B 之外）
		_, err = r.Resolve(cFile)
		assert.Error(t, err, "C 在 A/B 之外，应拒绝")
	})

	t.Run("allowDirs 空 → 仅 workDir，越界拒（原行为）", func(t *testing.T) {
		cfg := DefaultPathSecurityConfig() // AllowDirs 为 nil
		r, err := NewSecurePathResolver(A, cfg)
		require.NoError(t, err)

		// 主根内放行
		_, err = r.Resolve("a.txt")
		assert.NoError(t, err, "主根 A 内应放行")

		// B 此刻不在允许范围 → 拒
		_, err = r.Resolve(bFile)
		assert.Error(t, err, "allowDirs 空，B 外部路径应拒")
	})
}

// TestResolverCache_GetWithAllowDirs 验证多根 resolver 的缓存与退化行为。
func TestResolverCache_GetWithAllowDirs(t *testing.T) {
	wd := t.TempDir()
	allow1 := t.TempDir()
	allow2 := t.TempDir()

	c, err := NewResolverCache(wd, DefaultPathSecurityConfig())
	require.NoError(t, err)

	t.Run("同 workDir 不同 allowDirs → 不同 resolver（Workspace 同，allowedDirs 不同）", func(t *testing.T) {
		r1, err := c.GetWithAllowDirs(wd, []string{allow1}, false)
		require.NoError(t, err)
		r2, err := c.GetWithAllowDirs(wd, []string{allow2}, false)
		require.NoError(t, err)

		if r1 == r2 {
			t.Fatal("不同 allowDirs 应返回不同 resolver 实例")
		}
		assert.Equal(t, r1.Workspace(), r2.Workspace(), "Workspace 应相同（同 workDir）")
		require.Len(t, r1.allowedDirs, 1, "r1 应解析出 1 个 allowDir")
		require.Len(t, r2.allowedDirs, 1, "r2 应解析出 1 个 allowDir")
		assert.NotEqual(t, r1.allowedDirs[0], r2.allowedDirs[0], "allowedDirs 应不同")

		// 功能验证：r1 能放行 allow1 内文件、拒 allow2 内文件（证明 allowedDirs 真生效，不只是字段不同）
		r1File := filepath.Join(allow1, "x.txt")
		require.NoError(t, os.WriteFile(r1File, []byte("x"), 0644))
		_, err = r1.Resolve(r1File)
		assert.NoError(t, err, "r1 应放行 allow1 内文件")
		r2File := filepath.Join(allow2, "y.txt")
		require.NoError(t, os.WriteFile(r2File, []byte("y"), 0644))
		_, err = r1.Resolve(r2File)
		assert.Error(t, err, "r1 应拒绝 allow2 内文件（不在其 allowDirs）")
	})

	t.Run("allowDirs 空 → 退化为 Get（返回 byDir 同一实例）", func(t *testing.T) {
		// 同一 wd 的 Get 结果作为基准（byDir[wd]）
		base, err := c.Get(wd)
		require.NoError(t, err)

		got, err := c.GetWithAllowDirs(wd, nil, false)
		require.NoError(t, err)
		assert.Same(t, base, got, "nil allowDirs 应退化到 Get(wd) 同一实例")

		got2, err := c.GetWithAllowDirs(wd, []string{}, false)
		require.NoError(t, err)
		assert.Same(t, base, got2, "空 allowDirs 切片也应退化到 Get(wd)")
	})

	t.Run("同 workDir+allowDirs 两次 → 同一实例（缓存命中）", func(t *testing.T) {
		r1, err := c.GetWithAllowDirs(wd, []string{allow1, allow2}, false)
		require.NoError(t, err)
		r2, err := c.GetWithAllowDirs(wd, []string{allow1, allow2}, false)
		require.NoError(t, err)
		assert.Same(t, r1, r2, "同 workDir+allowDirs 应命中缓存返回同一实例")
	})
}

// TestIsPathInside 验证 path_security.go 的未导出 helper（同包测试可直接调）。
func TestIsPathInside(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	sibling := filepath.Join(filepath.Dir(base), "sibling")

	tests := []struct {
		name string
		path string
		base string
		want bool
	}{
		{"path==base 放行", base, base, true},
		{"子目录放行", sub, base, true},
		{"深层子目录放行", filepath.Join(sub, "deep", "file.txt"), base, true},
		{"../sibling 拒", sibling, base, false},
		{"绝对路径(base 外)拒", filepath.Join(os.TempDir(), "outside-allowdirs-test"), base, false},
		{"base 空 拒", base, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPathInside(tt.path, tt.base)
			assert.Equal(t, tt.want, got)
		})
	}
}
