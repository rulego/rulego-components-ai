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

	// The directORy list (multiple roots) additionally allowed by AllowDirs:p ath is released within either workDir root or AllowDir.
	// Used to allow agents to access fixed directories outside of workDir (such as temporary system directories, global skills, media), precisely without opening the full disk.
	AllowDirs []string `json:"allowDirs"`
}

// DefaultPathSecurityConfig returns default security configuration.
func DefaultPathSecurityConfig() PathSecurityConfig {
	return PathSecurityConfig{
		AllowHiddenFiles: false,
		AllowCrossDir:    false, // Cross-directory is prohibited by default
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
	allowedDirs   []string // Additional allowed directories (after EvalSymlinks parsing), for multi-root release
}

// NewSecurePathResolver creates a new secure path resolver.
func NewSecurePathResolver(workDir string, config PathSecurityConfig) (*SecurePathResolver, error) {
	resolver, err := NewPathResolver(workDir)
	if err != nil {
		return nil, err
	}
	// Parse the symbolic links in the workspace to ensure alignment with the resolved benchmark; otherwise, REL will be mismatched due to the soft link
	ws := resolver.Workspace()
	if real, err := filepath.EvalSymlinks(ws); err == nil {
		ws = real
	}
	// Parse the extra allow directory (EvalSymlinks + Clean), used for multi-root release. No directory skipping (no errors, judged again at runtime)
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

	// The UNC path (\\server\share or //server/share) can bypass the workspace root and be directly rejected
	if strings.HasPrefix(resolved, `\\`) || strings.HasPrefix(resolved, "//") {
		return "", NewErrorf(ErrCodePathEscape, "UNC paths not allowed")
	}

	// After parsing symbolic links, perform boundary checks to prevent soft links within the workspace from pointing outside the workspace.
	// When the path does not exist, parse the parent directory's soft link to avoid escaping with the soft link when writing to a new file
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
// Multiple root determination: The path is allowed within any allowedDir OR realWorkspace main root.
func (s *SecurePathResolver) checkPathTraversal(resolved string) error {
	// If cross-directory is allowed, skip path traversal checks
	if s.config.AllowCrossDir {
		return nil
	}
	// Release within the main root workspace
	if isPathInside(resolved, s.realWorkspace) {
		return nil
	}
	// Additional permission to release within the directory (multiple files).
	for _, dir := range s.allowedDirs {
		if isPathInside(resolved, dir) {
			return nil
		}
	}
	return NewErrorf(ErrCodePathEscape, "path escapes allowed directory")
}

// isPathInside checks whether the path is in the base directory (including or equal to base):
// filepath.Rel(base, path) does not start with ".." and is not absolute, so the path is inside the base.
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

// effectiveBase returns the root to which resolved belongs (realWorkspace or the first allowedDir containing resolved),
// Provides checkHiddenPath/checkExcludedDirs Based on correct root calculation rel—avoiding workspace rel for paths inside allowedDir
// False detections of hidden/excluded directories (such as /tmp/.env, media/.thumbs are not mistakenly rejected by workspace rel).
func (s *SecurePathResolver) effectiveBase(resolved string) string {
	if isPathInside(resolved, s.realWorkspace) {
		return s.realWorkspace
	}
	for _, dir := range s.allowedDirs {
		if isPathInside(resolved, dir) {
			return dir
		}
	}
	return s.realWorkspace // Not in any root (checkPathTraversal has been rejected), use workspace as a backup
}

// defaultAllowDirs Global default to the overall-allowed directory (atomic.Value stored []string, configuring hot update concurrency security).
// The business layer is set by SetDefaultAllowDirs (startup + configuration hot update), and the buildRunContext is read and merged via GetDefaultAllowDirs.
var defaultAllowDirs atomic.Value

// SetDefaultAllowDirs sets the global default extra allowed directory (concurrency security, startup, and configuration hot updates are both called here).
func SetDefaultAllowDirs(dirs []string) {
	defaultAllowDirs.Store(dirs)
}

// GetDefaultAllowDirs reads the global default extra allowed directory (concurrency security). No setting to return nil.
func GetDefaultAllowDirs() []string {
	if v := defaultAllowDirs.Load(); v != nil {
		if dirs, ok := v.([]string); ok {
			return dirs
		}
	}
	return nil
}

// defaultAllowCross Global default "Cross-directory release" switch (atomic.Value stored bool, configuring hot update concurrency security).
// The business layer is set by SetDefaultAllowCrossDir (startup + configuration hot update), and buildRunContext is used
// GetDefaultAllowCrossDir reads, merging the per-agent configuration and injecting ctx.
var defaultAllowCross atomic.Value

// SetDefaultAllowCrossDir sets the global default "Cross-directory Release" switch (concurrent security).
// true=Allow all paths (file tool skips out-of-bounds check); false=only workDir + allowDirs whitelist.
// When not set, the default is false (tighten). The business layer explicitly sets true to enable "default all enabled."
func SetDefaultAllowCrossDir(on bool) {
	defaultAllowCross.Store(on)
}

// GetDefaultAllowCrossDir reads the global default "Cross-directory Release" switch (concurrent security). No set to return false (tighten).
func GetDefaultAllowCrossDir() bool {
	if v := defaultAllowCross.Load(); v != nil {
		if on, ok := v.(bool); ok {
			return on
		}
	}
	return false
}

// defaultDenyHidden Global default "Refuse to hide files" switch (only controls write/edit write operations).
// The business layer is set by SetDefaultDenyHidden (startup + configuration hot update); write/edit pathSecurity read.
// default false (allows writing hidden files, does not restrict agent); true=write/edit Refuses to hide files/directories.
// read/grep/glob are not affected by this toggle (read hiding is essential for code review, fixed AllowHiddenFiles=true).
var defaultDenyHidden atomic.Value

// SetDefaultDenyHidden sets the global default "Refuse to hide files" switch (Concurrency Security).
func SetDefaultDenyHidden(on bool) {
	defaultDenyHidden.Store(on)
}

// GetDefaultDenyHidden reads the global default "Refuse to hide files" switch (concurrent security). No set to return false (allowing hiding).
func GetDefaultDenyHidden() bool {
	if v := defaultDenyHidden.Load(); v != nil {
		if on, ok := v.(bool); ok {
			return on
		}
	}
	return false
}

// defaultExcludeDirs Global default "Exclude Directory" blacklist (the Resolve path validation layer for all file tools).
// The business layer is set by SetDefaultExcludeDirs; Each tool pathSecurity reads and fills in PathSecurityConfig.ExcludeDirs.
// No set to return nil (does not exclude any directory); tpclaw is set to [.git/.svn/.hg] by default to protect version repository metadata.
// Note: Effective only during single file path validation in Resolve; walk/search traverses without resolving each file, so search may still traverse the exclusion directory (handled separately as needed).
var defaultExcludeDirs atomic.Value

// SetDefaultExcludeDirs sets the global default "exclude directory" blacklist (concurrency security).
func SetDefaultExcludeDirs(dirs []string) {
	defaultExcludeDirs.Store(dirs)
}

// GetDefaultExcludeDirs reads the global default "exclusion directory" blacklist (concurrent security). No setting to return nil.
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
// Note: Consistent with checkHiddenPath, not affected by AllowCrossDir — excluded is an independent blacklist dimension,
// Even if cross-directory release (cross=true), write to sensitive directories such as repository metadata (.git/.svn/.hg) should be refused.
// In previous versions, cross=true skipped this check altogether, causing "future to add non-hidden ExcludeDir (e.g., node_modules/secrets)"
// That is, the potential regression bypassed by cross—the short-circuit has been removed to maintain orthogonal dimensions.
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
