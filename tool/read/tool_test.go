package read

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
)

func TestReadTool_GitBashPath(t *testing.T) {
	config := DefaultConfig()
	rTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := rTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// Build a path to tool.go using current working directory
	wd, err := os.Getwd()
	assert.NoError(t, err)

	toolGoPath := filepath.Join(wd, "tool.go")
	// On Windows, convert to Git Bash style path for testing
	testPath := filepath.ToSlash(toolGoPath)
	if len(testPath) > 2 && testPath[1] == ':' {
		drive := strings.ToLower(string(testPath[0]))
		testPath = "/" + drive + testPath[2:]
	}

	paramsMap := map[string]interface{}{
		"operation": "file",
		"path":      testPath,
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)
	assert.Contains(t, resultStr, "Package read provides")
	assert.Contains(t, resultStr, "tool.go")

	t.Logf("Result length: %d", len(resultStr))
}

func TestReadTool_ListGitBashPath(t *testing.T) {
	config := DefaultConfig()
	rTool, err := NewTool(config)
	assert.NoError(t, err)

	toolInstance := rTool.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})

	// Use current working directory to build a valid path
	wd, err := os.Getwd()
	assert.NoError(t, err)

	// On Windows, convert to Git Bash style path for testing
	testPath := filepath.ToSlash(wd)
	if len(testPath) > 2 && testPath[1] == ':' {
		drive := strings.ToLower(string(testPath[0]))
		testPath = "/" + drive + testPath[2:]
	}

	paramsMap := map[string]interface{}{
		"operation": "list",
		"path":      testPath,
	}
	paramsBytes, _ := json.Marshal(paramsMap)

	resultStr, err := toolInstance.InvokableRun(context.Background(), string(paramsBytes))
	assert.NoError(t, err)
	assert.Contains(t, resultStr, "tool.go")

	t.Logf("Result: %s", resultStr)
}
