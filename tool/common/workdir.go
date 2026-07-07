package common

import "context"

// 通用 workDir 注入机制：业务层通过 WithWorkDir 把工作目录注入 ctx，工具（bash/read/write/edit）
// 通过 WorkDirFromCtx 读取。基础库不认任何业务字段（如 projectId/taskId）——业务层负责把业务标识
// 转成路径后注入，基础库只认通用的 workDir 字符串。不同应用注入各自路径，互不干扰。

// workDirCtxKey 专用 context key 类型，避免碰撞。
type workDirCtxKey struct{}

// WithWorkDir 把工作目录注入 ctx（通用机制）。
// 业务层调用，如主 agent 派子 agent 时把 projects/<projectId>/ 注入，子 agent 工具自动用此目录。
func WithWorkDir(ctx context.Context, workDir string) context.Context {
	return context.WithValue(ctx, workDirCtxKey{}, workDir)
}

// WorkDirFromCtx 从 ctx 取注入的工作目录（空=未注入，工具回退 config）。
func WorkDirFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(workDirCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// allowDirsCtxKey 专用 context key 类型，存额外允许目录列表（多根）。
type allowDirsCtxKey struct{}

// WithAllowDirs 把额外允许目录列表注入 ctx（多根机制，与 workDir 配合）。
// 业务层（如 buildRunContext）调用，工具经 AllowDirsFromCtx 读取。
func WithAllowDirs(ctx context.Context, dirs []string) context.Context {
	return context.WithValue(ctx, allowDirsCtxKey{}, dirs)
}

// AllowDirsFromCtx 从 ctx 取注入的额外允许目录（nil=未注入，仅 workDir 主根）。
func AllowDirsFromCtx(ctx context.Context) []string {
	if v, ok := ctx.Value(allowDirsCtxKey{}).([]string); ok {
		return v
	}
	return nil
}

// allowCrossCtxKey 专用 context key 类型，存"跨目录放行"开关（与 allowDirs 同源注入）。
type allowCrossCtxKey struct{}

// WithAllowCrossDir 把"跨目录放行"开关注入 ctx（true=允许所有路径，跳过越界检查）。
// 业务层（如 buildRunContext）调用，文件工具经 AllowCrossDirFromCtx 读取。
func WithAllowCrossDir(ctx context.Context, on bool) context.Context {
	return context.WithValue(ctx, allowCrossCtxKey{}, on)
}

// AllowCrossDirFromCtx 从 ctx 取"跨目录放行"开关。
// 未注入=false=收紧，走 workDir + allowDirs 白名单（与 resolver 默认行为一致）。
func AllowCrossDirFromCtx(ctx context.Context) bool {
	if v, ok := ctx.Value(allowCrossCtxKey{}).(bool); ok {
		return v
	}
	return false
}
