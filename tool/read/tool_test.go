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
	"github.com/stretchr/testify/require"

	"github.com/rulego/rulego-components-ai/tool/common"
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

// TestReadTool_CrossDirectory 验证 ctx 注入 allowCrossDir：
// true 放行 workDir 外路径，false 拒绝（覆盖 read 工具正确把 cross 透传到 resolver 的链路）。
func TestReadTool_CrossDirectory(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("TOPSECRET\n"), 0644))

	tt, err := NewTool(Config{WorkDir: workDir})
	require.NoError(t, err)
	ti := tt.(interface {
		InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error)
	})
	args, _ := json.Marshal(map[string]string{"operation": "file", "path": outsideFile})

	// cross=true：放行 workDir 外文件
	out, err := ti.InvokableRun(common.WithAllowCrossDir(context.Background(), true), string(args))
	assert.NoError(t, err)
	assert.Contains(t, out, "TOPSECRET")

	// cross=false：拒绝 workDir 外文件
	_, err = ti.InvokableRun(common.WithAllowCrossDir(context.Background(), false), string(args))
	assert.Error(t, err)
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
