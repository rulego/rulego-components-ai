package common

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllowCrossCtx verifies WithAllowCrossDir injection / AllowCrossDirFromCtx reads (including uninjected).
func TestAllowCrossCtx(t *testing.T) {
	// Not injected → false (tighten, go to workDir+allowDirs whitelist)
	if AllowCrossDirFromCtx(context.Background()) {
		t.Fatal("Uninjected ctx should return false")
	}
	// Inject true → read back true
	ctx := WithAllowCrossDir(context.Background(), true)
	assert.True(t, AllowCrossDirFromCtx(ctx), "注入 true 应读回 true")
	// Inject false → read back false
	ctx2 := WithAllowCrossDir(context.Background(), false)
	assert.False(t, AllowCrossDirFromCtx(ctx2), "注入 false 应读回 false")
}

// TestSetDefaultAllowCrossDir verifies the settings/read of the global default switch. Restores false at the end to avoid contaminating other tests.
func TestSetDefaultAllowCrossDir(t *testing.T) {
	SetDefaultAllowCrossDir(false)
	assert.False(t, GetDefaultAllowCrossDir(), "未设置/设 false 应读回 false")
	SetDefaultAllowCrossDir(true)
	assert.True(t, GetDefaultAllowCrossDir(), "设 true 应读回 true")
	SetDefaultAllowCrossDir(false) // Reset to default (tighten)
}

// TestResolverCache_CrossKey Verify cross dimension cache key:
// Same workDir+allowDirs, different cross → different resolvers (stale caching);
// The resolver with cross=true releases the out-of-workspace path (skipping the out-of-bounds check), while cross=false rejects.
func TestResolverCache_CrossKey(t *testing.T) {
	wd := t.TempDir()
	outside := t.TempDir() // Directory outside the work area
	outsideFile := filepath.Join(outside, "o.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("x"), 0644))

	c, err := NewResolverCache(wd, DefaultPathSecurityConfig())
	require.NoError(t, err)

	// Same WD, empty allowDirs, different cross → different resolvers
	rFalse, err := c.GetWithAllowDirs(wd, nil, false)
	require.NoError(t, err)
	rTrue, err := c.GetWithAllowDirs(wd, nil, true)
	require.NoError(t, err)
	if rFalse == rTrue {
		t.Fatal("Different cross should return different resolver instances (cross must go into cache key, otherwise stale cache)")
	}

	// cross=false → Path outside the workspace is rejected
	_, err = rFalse.Resolve(outsideFile)
	assert.Error(t, err, "cross=false 应拒绝工作区外路径")

	// cross=true → Release of paths outside the workspace (skipped by checkPathTraversal)
	got, err := rTrue.Resolve(outsideFile)
	assert.NoError(t, err, "cross=true 应放行工作区外路径")
	assert.Equal(t, outsideFile, got)

	// Same triple double → Same instance (cache hit)
	rTrue2, err := c.GetWithAllowDirs(wd, nil, true)
	require.NoError(t, err)
	assert.Same(t, rTrue, rTrue2, "同 (workDir,allowDirs,cross) 应命中缓存返回同一实例")
}
