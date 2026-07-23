package common

import "context"

// General workDir Injection Mechanism: The business layer injects the working directory into ctx via WithWorkDir and tools (bash/read/write/edit)
// Read via WorkDirFromCtx. The base library does not recognize any business fields (such as projectId/taskId)—the business layer is responsible for identifying the business
// After conversion to path, inject it; the base library only recognizes the common workDir string. Different applications are injected along their own paths without interfering with each other.

// workDirCtxKey is a dedicated context key type to avoid collisions.
type workDirCtxKey struct{}

// WithWorkDir injects the working directory into ctx (a common mechanism).
// Business layer calls, such as the main agent sending sub-agents <projectId>and injecting projects// into the sub-agent tool, which automatically uses this directory.
func WithWorkDir(ctx context.Context, workDir string) context.Context {
	return context.WithValue(ctx, workDirCtxKey{}, workDir)
}

// WorkDirFromCtx fetchs the injected working directory from ctx (empty = not injected, tool reverts config).
func WorkDirFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(workDirCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// allowDirsCtxKey is a dedicated context key type, storing an extra allowed directory list (multiple roots).
type allowDirsCtxKey struct{}

// WithAllowDirs injects the extra allowed directory list into ctx (a multi-root mechanism, in conjunction with workDir).
// Business layer (such as buildRunContext) calls, and tools are read by AllowDirsFromCtx.
func WithAllowDirs(ctx context.Context, dirs []string) context.Context {
	return context.WithValue(ctx, allowDirsCtxKey{}, dirs)
}

// AllowDirsFromCtx fetchs the additional allowed directory injected from ctx (nil=uninjected, only the workDir root is used).
func AllowDirsFromCtx(ctx context.Context) []string {
	if v, ok := ctx.Value(allowDirsCtxKey{}).([]string); ok {
		return v
	}
	return nil
}

// allowCrossCtxKey is a dedicated context key type, storing the "Cross-directory Release" switch (injected from the same source as allowDirs).
type allowCrossCtxKey struct{}

// WithAllowCrossDir injects the "Cross-directory release" switch into ctx (true=allows all paths, skips out-of-bounds checks).
// Business layer (such as buildRunContext) calls, and file tools are read by AllowCrossDirFromCtx.
func WithAllowCrossDir(ctx context.Context, on bool) context.Context {
	return context.WithValue(ctx, allowCrossCtxKey{}, on)
}

// AllowCrossDirFromCtx takes the "Cross-directory Release" switch from ctx.
// Uninjected = false = tightening, goes to workDir + allowDirs whitelist (consistent with resolver default behavior).
func AllowCrossDirFromCtx(ctx context.Context) bool {
	if v, ok := ctx.Value(allowCrossCtxKey{}).(bool); ok {
		return v
	}
	return false
}
