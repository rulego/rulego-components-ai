package common

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllowDirsCtx verifies with WithAllowDirs injection / AllowDirsFromCtx reads (including nil cases).
func TestAllowDirsCtx(t *testing.T) {
	// Not injected → nil
	if got := AllowDirsFromCtx(context.Background()); got != nil {
		t.Fatalf("Uninjected ctx should return nil got %v", got)
	}

	// Inject → Read back the same slice
	dirs := []string{"/tmp/a", "/tmp/b"}
	ctx := WithAllowDirs(context.Background(), dirs)
	got := AllowDirsFromCtx(ctx)
	assert.Equal(t, dirs, got, "注入后应读回相同的 allowDirs 切片")

	// Inject empty slice (non-nil)→ Read back slice len=0 (GetWithAllowDirs degenerates to get with len)
	emptyCtx := WithAllowDirs(context.Background(), []string{})
	if got := AllowDirsFromCtx(emptyCtx); len(got) != 0 {
		t.Fatalf("The empty slice should be read back len=0,got %v", got)
	}
}

// TestSecurePathResolver_AllowDirs Verify multi-root determination: The path allows passage OR within either the main root OR any allowDir of the workDir and rejects it AND crosses the boundary.
func TestSecurePathResolver_AllowDirs(t *testing.T) {
	A := t.TempDir() // Root workDir
	B := t.TempDir() // allowDir
	C := t.TempDir() // Cross-boundary Goals (Beyond A/B)

	// Send a file to B to test absolute path release
	bFile := filepath.Join(B, "in-b.txt")
	require.NoError(t, os.WriteFile(bFile, []byte("x"), 0644))

	// A file is placed in C for measuring boundary crossing
	cFile := filepath.Join(C, "in-c.txt")
	require.NoError(t, os.WriteFile(cFile, []byte("x"), 0644))

	t.Run("multi-root 放行与越界拒", func(t *testing.T) {
		cfg := DefaultPathSecurityConfig()
		cfg.AllowDirs = []string{B}
		r, err := NewSecurePathResolver(A, cfg)
		require.NoError(t, err)

		// Inside the primary root A: Relative path a.txt → parses to A/a.txt, then released
		got, err := r.Resolve("a.txt")
		assert.NoError(t, err, "主根 A 内的相对路径应放行")
		assert.Equal(t, filepath.Join(A, "a.txt"), got)

		// allowDir B: Absolute path bFile release
		got, err = r.Resolve(bFile)
		assert.NoError(t, err, "allowDir B 内的绝对路径应放行")
		assert.Equal(t, bFile, got)

		// path == allowDir itself (B) is released
		got, err = r.Resolve(B)
		assert.NoError(t, err, "path == allowDir 本身应放行")
		assert.Equal(t, B, got)

		// Out-of-bounds: absolute path denial within C (C outside A/B)
		_, err = r.Resolve(cFile)
		assert.Error(t, err, "C 在 A/B 之外，应拒绝")
	})

	t.Run("allowDirs 空 → 仅 workDir，越界拒（原行为）", func(t *testing.T) {
		cfg := DefaultPathSecurityConfig() // AllowDirs is nil
		r, err := NewSecurePathResolver(A, cfg)
		require.NoError(t, err)

		// Release within the main root
		_, err = r.Resolve("a.txt")
		assert.NoError(t, err, "主根 A 内应放行")

		// B is not within the allowed range at this moment→ refuse
		_, err = r.Resolve(bFile)
		assert.Error(t, err, "allowDirs 空，B 外部路径应拒")
	})
}

// TestResolverCache_GetWithAllowDirs Verify the caching and degradation behavior of multiple resolvers.
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
			t.Fatal("Different allowDirs should return different resolver instances")
		}
		assert.Equal(t, r1.Workspace(), r2.Workspace(), "Workspace 应相同（同 workDir）")
		require.Len(t, r1.allowedDirs, 1, "r1 应解析出 1 个 allowDir")
		require.Len(t, r2.allowedDirs, 1, "r2 应解析出 1 个 allowDir")
		assert.NotEqual(t, r1.allowedDirs[0], r2.allowedDirs[0], "allowedDirs 应不同")

		// Function verification: r1 can allow files inside allow1 and deny files inside allow2 (proves allowedDirs is truly effective, not just field differences)
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
		// Use the Get result of the same WD as the benchmark (byDir[wd])
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

// TestIsPathInside verifies the path_security.go unexported helper (can be directly called for the same package test).
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
