package common

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestResolverCache_DefaultAndOverride 验证：空覆盖→默认 resolver；非空→按 workDir 缓存的独立 resolver。
func TestResolverCache_DefaultAndOverride(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	c, err := NewResolverCache(dir1, DefaultPathSecurityConfig())
	if err != nil {
		t.Fatalf("NewResolverCache: %v", err)
	}

	// 空 → 默认（dir1）
	r0, err := c.Get("")
	if err != nil || r0 != c.Default() {
		t.Fatalf("空覆盖应返回默认 resolver，got %v err=%v", r0, err)
	}

	// dir2 覆盖
	r2, err := c.Get(dir2)
	if err != nil {
		t.Fatalf("Get(dir2): %v", err)
	}
	if r2 == c.Default() {
		t.Fatal("覆盖 resolver 不应等于默认")
	}
	if r2.Workspace() != dir2 {
		t.Fatalf("覆盖 resolver workspace 应为 %s，got %s", dir2, r2.Workspace())
	}

	// 再次 Get(dir2) → 同一实例（缓存命中）
	r2b, _ := c.Get(dir2)
	if r2b != r2 {
		t.Fatal("同 workDir 应返回缓存的同一实例")
	}
}

// TestResolverCache_ReadUsesInjectedWorkDir 端到端验证 ctx 注入：文件工具通过 WorkDirFromCtx
// 拿到覆盖 workDir 后，能解析到该目录下的文件（而非默认 workDir）。
func TestResolverCache_ReadUsesInjectedWorkDir(t *testing.T) {
	defaultDir := t.TempDir()
	overrideDir := t.TempDir()

	// 只在 overrideDir 放文件
	target := filepath.Join(overrideDir, "secret.txt")
	if err := os.WriteFile(target, []byte("override-content"), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := NewResolverCache(defaultDir, DefaultPathSecurityConfig())
	if err != nil {
		t.Fatal(err)
	}

	// 模拟 buildRunContext 注入：把 overrideDir 注入 ctx
	ctx := WithWorkDir(context.Background(), overrideDir)

	r, err := c.Get(WorkDirFromCtx(ctx))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resolved, err := r.Resolve("secret.txt")
	if err != nil {
		t.Fatalf("在注入 workDir 下解析 secret.txt 失败: %v", err)
	}
	if resolved != target {
		t.Fatalf("应解析到 %s，got %s", target, resolved)
	}

	// 未注入 ctx（空）→ 默认 dir：同一相对路径解析到不同绝对路径（defaultDir 而非 overrideDir）。
	// 注：Resolve 只查路径越界不查存在性，故此处不报错；关键断言是解析目标不同。
	rDefault, _ := c.Get(WorkDirFromCtx(context.Background()))
	resolvedDefault, err := rDefault.Resolve("secret.txt")
	if err != nil {
		t.Fatalf("默认 workDir 解析: %v", err)
	}
	if resolvedDefault != filepath.Join(defaultDir, "secret.txt") {
		t.Fatalf("默认 workDir 应解析到 %s，got %s", filepath.Join(defaultDir, "secret.txt"), resolvedDefault)
	}
	if resolvedDefault == resolved {
		t.Fatal("注入 vs 默认 workDir 解析到同一路径，注入未生效")
	}
}
