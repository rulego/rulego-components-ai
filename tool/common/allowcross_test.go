package common

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllowCrossCtx 验证 WithAllowCrossDir 注入 / AllowCrossDirFromCtx 读取（含未注入）。
func TestAllowCrossCtx(t *testing.T) {
	// 未注入 → false（收紧，走 workDir+allowDirs 白名单）
	if AllowCrossDirFromCtx(context.Background()) {
		t.Fatal("未注入 ctx 应返回 false")
	}
	// 注入 true → 读回 true
	ctx := WithAllowCrossDir(context.Background(), true)
	assert.True(t, AllowCrossDirFromCtx(ctx), "注入 true 应读回 true")
	// 注入 false → 读回 false
	ctx2 := WithAllowCrossDir(context.Background(), false)
	assert.False(t, AllowCrossDirFromCtx(ctx2), "注入 false 应读回 false")
}

// TestSetDefaultAllowCrossDir 验证全局默认开关的设置/读取。末尾恢复 false 避免污染其它测试。
func TestSetDefaultAllowCrossDir(t *testing.T) {
	SetDefaultAllowCrossDir(false)
	assert.False(t, GetDefaultAllowCrossDir(), "未设置/设 false 应读回 false")
	SetDefaultAllowCrossDir(true)
	assert.True(t, GetDefaultAllowCrossDir(), "设 true 应读回 true")
	SetDefaultAllowCrossDir(false) // 恢复默认（收紧）
}

// TestResolverCache_CrossKey 验证 cross 维度编进 cache key：
// 同 workDir+allowDirs、不同 cross → 不同 resolver（防 stale 缓存）；
// cross=true 的 resolver 放行工作区外路径（跳过越界检查），cross=false 拒绝。
func TestResolverCache_CrossKey(t *testing.T) {
	wd := t.TempDir()
	outside := t.TempDir() // 工作区外目录
	outsideFile := filepath.Join(outside, "o.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("x"), 0644))

	c, err := NewResolverCache(wd, DefaultPathSecurityConfig())
	require.NoError(t, err)

	// 同 wd、空 allowDirs、不同 cross → 不同 resolver
	rFalse, err := c.GetWithAllowDirs(wd, nil, false)
	require.NoError(t, err)
	rTrue, err := c.GetWithAllowDirs(wd, nil, true)
	require.NoError(t, err)
	if rFalse == rTrue {
		t.Fatal("不同 cross 应返回不同 resolver 实例（cross 必须进 cache key，否则 stale 缓存）")
	}

	// cross=false → 工作区外路径拒
	_, err = rFalse.Resolve(outsideFile)
	assert.Error(t, err, "cross=false 应拒绝工作区外路径")

	// cross=true → 工作区外路径放行（checkPathTraversal 跳过）
	got, err := rTrue.Resolve(outsideFile)
	assert.NoError(t, err, "cross=true 应放行工作区外路径")
	assert.Equal(t, outsideFile, got)

	// 同三元组两次 → 同一实例（缓存命中）
	rTrue2, err := c.GetWithAllowDirs(wd, nil, true)
	require.NoError(t, err)
	assert.Same(t, rTrue, rTrue2, "同 (workDir,allowDirs,cross) 应命中缓存返回同一实例")
}
