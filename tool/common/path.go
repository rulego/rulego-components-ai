package common

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PathResolver handles path resolution and normalization.
type PathResolver struct {
	workspace string
}

// NewPathResolver creates a new PathResolver with the given workspace.
func NewPathResolver(workspace string) (*PathResolver, error) {
	resolved, err := NormalizeWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	return &PathResolver{workspace: resolved}, nil
}

// Workspace returns the normalized workspace path.
func (p *PathResolver) Workspace() string {
	return p.workspace
}

// Resolve resolves a path relative to the workspace.
// If the path is absolute, it returns the path as-is.
// It also cleans the path to handle double backslashes and trailing spaces.
// On Windows, it also converts Git Bash style paths (/d/path) to Windows paths (D:/path).
func (p *PathResolver) Resolve(path string) string {
	// Trim spaces and trailing backslashes
	path = strings.TrimSpace(path)
	path = strings.TrimRight(path, "\\")

	// Convert Git Bash style path on Windows
	path = ConvertGitBashPath(path)

	// Clean the path to normalize separators and handle ..
	path = filepath.Clean(path)

	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(p.workspace, path)
}

// gitBashPathRegex matches Git Bash style paths like /d/path, /c/Users/, etc.
// Pattern: /<single-letter>/<rest-of-path>
var gitBashPathRegex = regexp.MustCompile(`^/([a-zA-Z])/(.*)$`)

// ConvertGitBashPath converts Git Bash style paths (/d/path) to Windows paths (D:/path).
// On non-Windows systems, it returns the path unchanged.
// Examples:
//   - /d/github/project -> D:/github/project (on Windows)
//   - /c/Users/admin -> C:/Users/admin (on Windows)
//   - /home/user -> /home/user (no match, not a drive letter pattern)
func ConvertGitBashPath(path string) string {
	// Only process on Windows
	if filepath.Separator != '\\' {
		return path
	}

	// Check if path matches Git Bash pattern /<drive-letter>/...
	matches := gitBashPathRegex.FindStringSubmatch(path)
	if matches == nil {
		return path
	}

	driveLetter := strings.ToUpper(matches[1])
	restPath := matches[2]

	// Convert to Windows path: D:/path or D:\path
	return driveLetter + ":/" + restPath
}

// NormalizeWorkspace normalizes the workspace path.
// It handles empty paths, ~ expansion, and converts to absolute path.
func NormalizeWorkspace(workspace string) (string, error) {
	if workspace == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		workspace = filepath.Join(home, DefaultWorkspace)
	}

	// Expand ~ path
	if strings.HasPrefix(workspace, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		workspace = filepath.Join(home, workspace[2:])
	}

	// Get absolute path
	absPath, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	return absPath, nil
}

// ExpandHomeDir expands ~ to the user's home directory.
func ExpandHomeDir(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// EnsureDir ensures the directory exists, creating it if necessary.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// EnsureParentDir ensures the parent directory of the given path exists.
// Returns a clear error if a path component is a file instead of a directory.
func EnsureParentDir(path string) error {
	parentDir := filepath.Dir(path)

	// Check if any parent path component is a file (not a directory)
	// This prevents confusing errors like "mkdir AGENTS.md: not a directory"
	currentPath := parentDir
	for {
		info, err := os.Stat(currentPath)
		if err == nil {
			// Path exists - check if it's a directory
			if !info.IsDir() {
				return fmt.Errorf("cannot create file: %q is a file, not a directory", currentPath)
			}
			// It's a directory, we can create subdirectories under it
			break
		}
		if !os.IsNotExist(err) {
			// Some other error
			return fmt.Errorf("check path %q: %w", currentPath, err)
		}

		// Path doesn't exist, check parent
		parent := filepath.Dir(currentPath)
		if parent == currentPath {
			// Reached root
			break
		}
		currentPath = parent
	}

	return EnsureDir(parentDir)
}

// ReadFileIfExists reads a file if it exists, returns error if file doesn't exist.
func ReadFileIfExists(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes data to a file with default permissions.
func WriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
