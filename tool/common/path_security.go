package common

import (
	"path/filepath"
	"strings"
	"sync/atomic"
)

// PathSecurityConfig holds path security configuration.
type PathSecurityConfig struct {
	// AllowHiddenFiles allows access to hidden files/directories (starting with .)
	AllowHiddenFiles bool `json:"allowHiddenFiles"`

	// AllowCrossDir allows access to paths outside the workspace directory
	// When true, path traversal checks are skipped, allowing absolute paths and .. paths
	AllowCrossDir bool `json:"allowCrossDir"`

	// ExcludeDirs directories to exclude from operations
	ExcludeDirs []string `json:"excludeDirs"`

	// MaxPathLength maximum allowed path length
	MaxPathLength int `json:"maxPathLength"`

	// AllowDirs 额外允许的目录列表（多根）：path 在 workDir 主根 OR 任一 AllowDir 内即放行。
	// 用于让 agent 访问 workDir 之外的固定目录（如系统临时目录、全局 skills、media），精确不开放全盘。
	AllowDirs []string `json:"allowDirs"`
}

// DefaultPathSecurityConfig returns default security configuration.
func DefaultPathSecurityConfig() PathSecurityConfig {
	return PathSecurityConfig{
		AllowHiddenFiles:    false,
		AllowCrossDir: false, // 默认禁止跨目录
		ExcludeDirs: []string{
			".git", ".svn", ".hg",
			"node_modules", "vendor",
			"__pycache__", ".idea", ".vscode",
		},
		MaxPathLength: 4096,
	}
}

// SecurePathResolver provides path resolution with security checks.
type SecurePathResolver struct {
	resolver      *PathResolver
	config        PathSecurityConfig
	realWorkspace string
	allowedDirs   []string // 额外允许的目录（EvalSymlinks 解析后），多根放行用
}

// NewSecurePathResolver creates a new secure path resolver.
func NewSecurePathResolver(workDir string, config PathSecurityConfig) (*SecurePathResolver, error) {
	resolver, err := NewPathResolver(workDir)
	if err != nil {
		return nil, err
	}
	// 解析工作区的符号链接，确保与 resolved 的基准一致，否则 Rel 会因软链失配
	ws := resolver.Workspace()
	if real, err := filepath.EvalSymlinks(ws); err == nil {
		ws = real
	}
	// 解析额外允许目录（EvalSymlinks + Clean），多根放行用。不存在目录跳过（不报错，运行时再判）
	allowed := make([]string, 0, len(config.AllowDirs))
	for _, d := range config.AllowDirs {
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		if real, err := filepath.EvalSymlinks(d); err == nil {
			d = real
		}
		allowed = append(allowed, d)
	}
	return &SecurePathResolver{
		resolver:      resolver,
		config:        config,
		realWorkspace: ws,
		allowedDirs:   allowed,
	}, nil
}

// Resolve resolves and validates a path.
// Returns an error if the path is unsafe.
func (s *SecurePathResolver) Resolve(path string) (string, error) {
	if len(path) > s.config.MaxPathLength {
		return "", NewErrorf(ErrCodePathInvalid, "path too long (max %d)", s.config.MaxPathLength)
	}

	resolved := s.resolver.Resolve(path)

	// UNC 路径（\\server\share 或 //server/share）可绕过工作区根，直接拒绝
	if strings.HasPrefix(resolved, `\\`) || strings.HasPrefix(resolved, "//") {
		return "", NewErrorf(ErrCodePathEscape, "UNC paths not allowed")
	}

	// 解析符号链接后再做越界检查，防止工作区内软链指向工作区外。
	// 路径不存在时解析父目录的软链，避免写入新文件时跟随软链逃逸
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	} else if realDir, derr := filepath.EvalSymlinks(filepath.Dir(resolved)); derr == nil {
		resolved = filepath.Join(realDir, filepath.Base(resolved))
	}

	if err := s.checkPathTraversal(resolved); err != nil {
		return "", err
	}

	// Check for hidden files
	if !s.config.AllowHiddenFiles {
		if err := s.checkHiddenPath(resolved); err != nil {
			return "", err
		}
	}

	// Check for excluded directories
	if err := s.checkExcludedDirs(resolved); err != nil {
		return "", err
	}

	return resolved, nil
}

// Workspace returns the workspace directory (symlinks resolved).
func (s *SecurePathResolver) Workspace() string {
	return s.realWorkspace
}

// checkPathTraversal ensures the path doesn't escape the workspace.
// When AllowCrossDir is true, this check is skipped.
// 多根判定：path 在 realWorkspace 主根 OR 任一 allowedDir 内即放行。
func (s *SecurePathResolver) checkPathTraversal(resolved string) error {
	// 如果允许跨目录，跳过路径遍历检查
	if s.config.AllowCrossDir {
		return nil
	}
	// 主根 workspace 内放行
	if isPathInside(resolved, s.realWorkspace) {
		return nil
	}
	// 额外允许目录（多根）内放行
	for _, dir := range s.allowedDirs {
		if isPathInside(resolved, dir) {
			return nil
		}
	}
	return NewErrorf(ErrCodePathEscape, "path escapes allowed directory")
}

// isPathInside 判断 path 是否在 base 目录内（含等于 base）：
// filepath.Rel(base, path) 不以 ".." 开头且非绝对，则 path 在 base 内。
func isPathInside(path, base string) bool {
	if base == "" {
		return false
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	if filepath.IsAbs(rel) {
		return false
	}
	relSlash := filepath.ToSlash(rel)
	return relSlash != ".." && !strings.HasPrefix(relSlash, "../")
}

// effectiveBase 返回 resolved 所属的根（realWorkspace 或首个包含 resolved 的 allowedDir），
// 供 checkHiddenPath/checkExcludedDirs 基于正确根算 rel——避免 allowedDir 内路径用 workspace rel
// 误判隐藏/排除目录（如 /tmp/.env、media/.thumbs 不被 workspace rel 误拒）。
func (s *SecurePathResolver) effectiveBase(resolved string) string {
	if isPathInside(resolved, s.realWorkspace) {
		return s.realWorkspace
	}
	for _, dir := range s.allowedDirs {
		if isPathInside(resolved, dir) {
			return dir
		}
	}
	return s.realWorkspace // 不在任何根内（checkPathTraversal 已拒），用 workspace 兜底
}

// defaultAllowDirs 全局默认额外允许目录（atomic.Value 存 []string，配置热更新并发安全）。
// 业务层经 SetDefaultAllowDirs 设置（启动 + 配置热更新），buildRunContext 经 GetDefaultAllowDirs 读取合并。
var defaultAllowDirs atomic.Value

// SetDefaultAllowDirs 设置全局默认额外允许目录（并发安全，启动与配置热更新均经此调用）。
func SetDefaultAllowDirs(dirs []string) {
	defaultAllowDirs.Store(dirs)
}

// GetDefaultAllowDirs 读取全局默认额外允许目录（并发安全）。未设置返回 nil。
func GetDefaultAllowDirs() []string {
	if v := defaultAllowDirs.Load(); v != nil {
		if dirs, ok := v.([]string); ok {
			return dirs
		}
	}
	return nil
}

// defaultAllowCross 全局默认"跨目录放行"开关（atomic.Value 存 bool，配置热更新并发安全）。
// 业务层经 SetDefaultAllowCrossDir 设置（启动 + 配置热更新），buildRunContext 经
// GetDefaultAllowCrossDir 读取，合并 per-agent 配置后注入 ctx。
var defaultAllowCross atomic.Value

// SetDefaultAllowCrossDir 设置全局默认"跨目录放行"开关（并发安全）。
// true=允许所有路径（文件工具跳过越界检查）；false=仅 workDir + allowDirs 白名单。
// 未设置时默认 false（收紧），由业务层显式设 true 开启"默认全开"。
func SetDefaultAllowCrossDir(on bool) {
	defaultAllowCross.Store(on)
}

// GetDefaultAllowCrossDir 读取全局默认"跨目录放行"开关（并发安全）。未设置返回 false（收紧）。
func GetDefaultAllowCrossDir() bool {
	if v := defaultAllowCross.Load(); v != nil {
		if on, ok := v.(bool); ok {
			return on
		}
	}
	return false
}

// defaultDenyHidden 全局默认"拒绝隐藏文件"开关（仅控制 write/edit 写操作）。
// 业务层经 SetDefaultDenyHidden 设置（启动 + 配置热更新）；write/edit 的 pathSecurity 读取。
// 默认 false（允许写隐藏文件，不限制 agent）；true=write/edit 拒绝隐藏文件/目录。
// read/grep/glob 不受此开关影响（读隐藏是代码审查刚需，固定 AllowHiddenFiles=true）。
var defaultDenyHidden atomic.Value

// SetDefaultDenyHidden 设置全局默认"拒绝隐藏文件"开关（并发安全）。
func SetDefaultDenyHidden(on bool) {
	defaultDenyHidden.Store(on)
}

// GetDefaultDenyHidden 读取全局默认"拒绝隐藏文件"开关（并发安全）。未设置返回 false（允许隐藏）。
func GetDefaultDenyHidden() bool {
	if v := defaultDenyHidden.Load(); v != nil {
		if on, ok := v.(bool); ok {
			return on
		}
	}
	return false
}

// defaultExcludeDirs 全局默认"排除目录"黑名单（所有文件工具的 Resolve 路径校验层）。
// 业务层经 SetDefaultExcludeDirs 设置；各工具 pathSecurity 读取填入 PathSecurityConfig.ExcludeDirs。
// 未设置返回 nil（不排除任何目录）；tpclaw 默认设 [.git/.svn/.hg] 保护版本库元数据。
// 注意：仅在 Resolve 单文件路径校验时生效；walk/search 遍历不逐文件 Resolve，故 search 仍可能遍历进排除目录（按需另行处理）。
var defaultExcludeDirs atomic.Value

// SetDefaultExcludeDirs 设置全局默认"排除目录"黑名单（并发安全）。
func SetDefaultExcludeDirs(dirs []string) {
	defaultExcludeDirs.Store(dirs)
}

// GetDefaultExcludeDirs 读取全局默认"排除目录"黑名单（并发安全）。未设置返回 nil。
func GetDefaultExcludeDirs() []string {
	if v := defaultExcludeDirs.Load(); v != nil {
		if dirs, ok := v.([]string); ok {
			return dirs
		}
	}
	return nil
}

// checkHiddenPath checks if the path contains hidden files/directories.
func (s *SecurePathResolver) checkHiddenPath(resolved string) error {
	workspace := s.effectiveBase(resolved)

	rel, err := filepath.Rel(workspace, resolved)
	if err != nil {
		return nil // Let other checks handle this
	}

	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts {
		// Allow . and .. as they are handled by path traversal check
		if part == "." || part == ".." {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return NewErrorf(ErrCodePathInvalid, "access to hidden files/directories not allowed: %s", part)
		}
	}

	return nil
}

// checkExcludedDirs checks if the path is in an excluded directory.
// 注意：与 checkHiddenPath 一致，不受 AllowCrossDir 影响——excluded 是独立的黑名单维度，
// 即使跨目录放行（cross=true）也应拒绝写入版本库元数据等敏感目录（.git/.svn/.hg）。
// 此前版本曾在 cross=true 时整体跳过本检查，导致"未来加非隐藏 ExcludeDir（如 node_modules/secrets）
// 即被 cross 绕过"的潜在回归——已移除该 short-circuit 保持维度正交。
func (s *SecurePathResolver) checkExcludedDirs(resolved string) error {
	workspace := s.effectiveBase(resolved)

	rel, err := filepath.Rel(workspace, resolved)
	if err != nil {
		return nil
	}

	relNormalized := filepath.ToSlash(rel)
	parts := strings.Split(relNormalized, "/")

	for _, excluded := range s.config.ExcludeDirs {
		for _, part := range parts {
			if part == excluded {
				return NewErrorf(ErrCodePathInvalid, "access to '%s' directory not allowed", excluded)
			}
		}
	}

	return nil
}

// ValidatePath validates a path without resolving it.
func ValidatePath(path string, config PathSecurityConfig) error {
	if len(path) > config.MaxPathLength {
		return NewErrorf(ErrCodePathInvalid, "path too long (max %d)", config.MaxPathLength)
	}

	// Check for path traversal
	if strings.Contains(path, "..") {
		// Allow .. only in safe contexts
		parts := strings.Split(filepath.ToSlash(path), "/")
		for _, part := range parts {
			if part == ".." {
				return NewErrorf(ErrCodePathEscape, "path traversal not allowed")
			}
		}
	}

	// Check for hidden files
	if !config.AllowHiddenFiles {
		parts := strings.Split(filepath.ToSlash(path), "/")
		for _, part := range parts {
			if strings.HasPrefix(part, ".") && part != "." && part != ".." {
				return NewErrorf(ErrCodePathInvalid, "access to hidden files not allowed: %s", part)
			}
		}
	}

	return nil
}
