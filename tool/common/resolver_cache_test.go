package common

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestResolverCache_DefaultAndOverride Verification: empty override →default resolver; Non-null → independent resolver cached by workDir cache.
func TestResolverCache_DefaultAndOverride(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	c, err := NewResolverCache(dir1, DefaultPathSecurityConfig())
	if err != nil {
		t.Fatalf("NewResolverCache: %v", err)
	}

	// Null → Default (dir1)
	r0, err := c.Get("")
	if err != nil || r0 != c.Default() {
		t.Fatalf("The empty override should return the default resolver, got %v err=%v", r0, err)
	}

	// dir2 override
	r2, err := c.Get(dir2)
	if err != nil {
		t.Fatalf("Get(dir2): %v", err)
	}
	if r2 == c.Default() {
		t.Fatal("Override resolver should not be the default")
	}
	if r2.Workspace() != dir2 {
		t.Fatalf("The coverage resolver workspace should be %s, got %s", dir2, r2.Workspace())
	}

	// Get(dir2) again → Same instance (cache hit)
	r2b, _ := c.Get(dir2)
	if r2b != r2 {
		t.Fatal("The same instance in the cache should be returned to the same instance in the same workDir")
	}
}

// TestResolverCache_ReadUsesInjectedWorkDir End-to-end verification of ctx injection: The file tool is processed via WorkDirFromCtx
// After obtaining the overwrite workDir, it can parse files in that directory (instead of the default workDir).
func TestResolverCache_ReadUsesInjectedWorkDir(t *testing.T) {
	defaultDir := t.TempDir()
	overrideDir := t.TempDir()

	// Only place files in overrideDir
	target := filepath.Join(overrideDir, "secret.txt")
	if err := os.WriteFile(target, []byte("override-content"), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := NewResolverCache(defaultDir, DefaultPathSecurityConfig())
	if err != nil {
		t.Fatal(err)
	}

	// Simulating buildRunContext injection: injecting overrideDir into ctx
	ctx := WithWorkDir(context.Background(), overrideDir)

	r, err := c.Get(WorkDirFromCtx(ctx))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resolved, err := r.Resolve("secret.txt")
	if err != nil {
		t.Fatalf("Parsing secret.txt on injection workDir fails: %v", err)
	}
	if resolved != target {
		t.Fatalf("Should be resolved to %s, got %s", target, resolved)
	}

	// Not injected ctx (empty)→ Default dir: parses the same relative path into different absolute paths (defaultDir instead of overrideDir).
	// Note: Resolve only checks for path boundary crossing, not existence, so no error is reported here; The key assertion is that the analysis targets differ.
	rDefault, _ := c.Get(WorkDirFromCtx(context.Background()))
	resolvedDefault, err := rDefault.Resolve("secret.txt")
	if err != nil {
		t.Fatalf("Default workDir resolution: %v", err)
	}
	if resolvedDefault != filepath.Join(defaultDir, "secret.txt") {
		t.Fatalf("By default, workDir should parse to %s, got %s", filepath.Join(defaultDir, "secret.txt"), resolvedDefault)
	}
	if resolvedDefault == resolved {
		t.Fatal("Injection vs By default, workDir parses to the same path, and injection does not take effect")
	}
}
