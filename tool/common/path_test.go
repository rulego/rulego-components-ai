package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPathResolver_Resolve_HomeExpansion 验证 ~/xxx 展开为用户主目录（治 ~/.tpclaw/xxx 被当字面 ~ 目录）。
func TestPathResolver_Resolve_HomeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	assert.NoError(t, err)

	resolver, err := NewPathResolver(t.TempDir())
	assert.NoError(t, err)

	result := resolver.Resolve("~/.tpclaw/config.json")
	assert.Equal(t, filepath.Join(home, ".tpclaw/config.json"), result)
}

func TestConvertGitBashPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Git Bash D drive", "/d/github/project", "D:/github/project"},
		{"Git Bash C drive", "/c/Users/admin", "C:/Users/admin"},
		{"Git Bash lowercase", "/e/workspace/code", "E:/workspace/code"},
		{"Regular Unix path", "/home/user/documents", "/home/user/documents"},
		{"Windows path unchanged", "C:/Users/admin", "C:/Users/admin"},
		{"Relative path unchanged", "relative/path", "relative/path"},
		{"Empty path", "", ""},
		{"Root only", "/", "/"},
		{"Single letter drive only", "/d", "/d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertGitBashPath(tt.input)
			// On non-Windows, path should be unchanged
			if filepath.Separator != '\\' {
				assert.Equal(t, tt.input, result)
			} else {
				// On Windows, check conversion
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestPathResolver_Resolve_GitBashPath(t *testing.T) {
	// Skip on non-Windows
	if filepath.Separator != '\\' {
		t.Skip("Skipping Windows-specific test")
	}

	resolver, err := NewPathResolver("d:/github/rulego-project")
	assert.NoError(t, err)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Git Bash path", "/d/github/project/file.txt", "D:\\github\\project\\file.txt"},
		{"Git Bash C drive", "/c/Users/test", "C:\\Users\\test"},
		{"Relative path", "subdir/file.txt", "d:\\github\\rulego-project\\subdir\\file.txt"},
		{"Windows absolute", "E:/workspace/code", "E:\\workspace\\code"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolver.Resolve(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
