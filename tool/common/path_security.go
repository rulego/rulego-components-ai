package common

import (
	"path/filepath"
	"strings"
)

// PathSecurityConfig holds path security configuration.
type PathSecurityConfig struct {
	// AllowHiddenFiles allows access to hidden files/directories (starting with .)
	AllowHiddenFiles bool `json:"allowHiddenFiles"`

	// AllowCrossDirectory allows access to paths outside the workspace directory
	// When true, path traversal checks are skipped, allowing absolute paths and .. paths
	AllowCrossDirectory bool `json:"allowCrossDirectory"`

	// ExcludeDirs directories to exclude from operations
	ExcludeDirs []string `json:"excludeDirs"`

	// MaxPathLength maximum allowed path length
	MaxPathLength int `json:"maxPathLength"`
}

// DefaultPathSecurityConfig returns default security configuration.
func DefaultPathSecurityConfig() PathSecurityConfig {
	return PathSecurityConfig{
		AllowHiddenFiles:    false,
		AllowCrossDirectory: true, // 默认允许跨目录
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
	resolver *PathResolver
	config   PathSecurityConfig
}

// NewSecurePathResolver creates a new secure path resolver.
func NewSecurePathResolver(workDir string, config PathSecurityConfig) (*SecurePathResolver, error) {
	resolver, err := NewPathResolver(workDir)
	if err != nil {
		return nil, err
	}
	return &SecurePathResolver{
		resolver: resolver,
		config:   config,
	}, nil
}

// Resolve resolves and validates a path.
// Returns an error if the path is unsafe.
func (s *SecurePathResolver) Resolve(path string) (string, error) {
	if len(path) > s.config.MaxPathLength {
		return "", NewErrorf(ErrCodePathInvalid, "path too long (max %d)", s.config.MaxPathLength)
	}

	resolved := s.resolver.Resolve(path)

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

// Workspace returns the workspace directory.
func (s *SecurePathResolver) Workspace() string {
	return s.resolver.Workspace()
}

// checkPathTraversal ensures the path doesn't escape the workspace.
// When AllowCrossDirectory is true, this check is skipped.
func (s *SecurePathResolver) checkPathTraversal(resolved string) error {
	// 如果允许跨目录，跳过路径遍历检查
	if s.config.AllowCrossDirectory {
		return nil
	}

	workspace := s.resolver.Workspace()

	// Get relative path
	rel, err := filepath.Rel(workspace, resolved)
	if err != nil {
		return NewErrorf(ErrCodePathEscape, "cannot determine relative path")
	}

	// Check for path traversal attempts
	// - Paths starting with ".." escape the workspace
	// - Absolute paths start with "/" on Unix or drive letter on Windows
	relNormalized := filepath.ToSlash(rel)
	if strings.HasPrefix(relNormalized, "..") {
		return NewErrorf(ErrCodePathEscape, "path escapes allowed directory")
	}

	// Check for absolute paths
	if filepath.IsAbs(rel) {
		return NewErrorf(ErrCodePathEscape, "absolute paths not allowed")
	}

	return nil
}

// checkHiddenPath checks if the path contains hidden files/directories.
func (s *SecurePathResolver) checkHiddenPath(resolved string) error {
	workspace := s.resolver.Workspace()

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
func (s *SecurePathResolver) checkExcludedDirs(resolved string) error {
	// 跨目录模式下跳过排除目录检查
	if s.config.AllowCrossDirectory {
		return nil
	}

	workspace := s.resolver.Workspace()

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
