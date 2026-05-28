package write

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolInfo(t *testing.T) {
	tTool, err := NewTool(DefaultConfig())
	require.NoError(t, err)

	ctx := context.Background()
	info, err := tTool.Info(ctx)
	require.NoError(t, err)

	assert.Equal(t, ToolName, info.Name)
	assert.NotEmpty(t, info.Desc)
	assert.NotNil(t, info.ParamsOneOf)

	t.Logf("Tool name: %s", info.Name)
	t.Logf("Tool desc: %s", info.Desc)
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.Equal(t, ".", config.WorkDir)

	t.Logf("Default config: WorkDir=%s", config.WorkDir)
}

func TestNewTool(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name:   "默认配置",
			config: DefaultConfig(),
		},
		{
			name: "自定义工作目录",
			config: Config{
				WorkDir: os.TempDir(),
			},
		},
		{
			name:   "空配置使用默认值",
			config: Config{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewTool(tt.config)
			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestInvalidJSON(t *testing.T) {
	config := DefaultConfig()

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	result, err := invokable.InvokableRun(ctx, `invalid json`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse params")
	t.Logf("Result: %s, Error: %v", result, err)
}

func TestEmptyPath(t *testing.T) {
	config := DefaultConfig()

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	result, err := invokable.InvokableRun(ctx, `{"operation": "file", "path": "", "content": "test"}`)
	require.NoError(t, err)
	assert.Contains(t, result, "File path cannot be empty")
}

func TestEmptyContent(t *testing.T) {
	config := DefaultConfig()

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	result, err := invokable.InvokableRun(ctx, `{"operation": "file", "path": "test.txt", "content": ""}`)
	require.NoError(t, err)
	assert.Contains(t, result, "Content cannot be empty")
}

func getTempFilePath(t *testing.T, name string) string {
	tmpFile := filepath.Join(os.TempDir(), "test_write_"+name+".txt")
	t.Cleanup(func() {
		os.Remove(tmpFile)
	})
	return tmpFile
}

func buildParams(params map[string]interface{}) string {
	b, _ := json.Marshal(params)
	return string(b)
}

func TestWriteCreateFile(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tmpFile := getTempFilePath(t, "create")

	// Test create mode
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      tmpFile,
		"content":   "hello world",
		"mode":      "create",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Created")
	assert.Contains(t, result, "test_write_create.txt")

	// Verify file content
	content, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))

	t.Logf("Create result: %s", result)
}

func TestWriteFileAlreadyExists(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tmpFile := getTempFilePath(t, "exists")

	// Create file first
	err = os.WriteFile(tmpFile, []byte("existing content"), 0644)
	require.NoError(t, err)

	// Try to create again - should fail
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      tmpFile,
		"content":   "new content",
		"mode":      "create",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "File already exists")
	assert.Contains(t, result, "overwrite or append")

	t.Logf("Exists result: %s", result)
}

func TestWriteOverwriteFile(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tmpFile := getTempFilePath(t, "overwrite")

	// Create file first
	err = os.WriteFile(tmpFile, []byte("original content"), 0644)
	require.NoError(t, err)

	// Overwrite
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      tmpFile,
		"content":   "new content",
		"mode":      "overwrite",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Overwrote")

	// Verify file content
	content, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Equal(t, "new content", string(content))

	t.Logf("Overwrite result: %s", result)
}

func TestWriteAppendFile(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tmpFile := getTempFilePath(t, "append")

	// Create file first
	err = os.WriteFile(tmpFile, []byte("original content"), 0644)
	require.NoError(t, err)

	// Append
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      tmpFile,
		"content":   "appended content",
		"mode":      "append",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Appended")

	// Verify file content contains both
	content, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "original content")
	assert.Contains(t, contentStr, "appended content")

	t.Logf("Append result: %s", result)
}

func TestWriteAppendToNewFile(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tmpFile := getTempFilePath(t, "append_new")

	// Append to non-existent file - should create it
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      tmpFile,
		"content":   "first content",
		"mode":      "append",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Appended")

	// Verify file content
	content, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "first content")

	t.Logf("Append new result: %s", result)
}

func TestWriteToSubdirectory(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Write to a subdirectory that doesn't exist yet
	subDirPath := filepath.Join("test_subdir", "test_write_sub.txt")
	tmpDir := filepath.Join(os.TempDir(), "test_subdir")
	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      subDirPath,
		"content":   "subdirectory content",
		"mode":      "create",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Created")

	// Verify file was created
	fullPath := filepath.Join(os.TempDir(), subDirPath)
	_, err = os.Stat(fullPath)
	require.NoError(t, err)

	t.Logf("Subdirectory result: %s", result)
}

func TestDefaultModeIsCreate(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tmpFile := getTempFilePath(t, "default_mode")

	// Write without specifying mode - should default to create
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      tmpFile,
		"content":   "test content",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Created")

	t.Logf("Default mode result: %s", result)
}

func TestWriteLargeContent(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tmpFile := getTempFilePath(t, "large")

	// Create a large content string
	largeContent := ""
	for i := 0; i < 1000; i++ {
		largeContent += "This is a test line for large content.\n"
	}

	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      tmpFile,
		"content":   largeContent,
		"mode":      "create",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Created")

	// Verify file content
	content, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Len(t, content, len(largeContent))

	t.Logf("Large content result length: %d", len(content))
}

func TestWriteSpecialCharacters(t *testing.T) {
	config := Config{
		WorkDir: os.TempDir(),
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tmpFile := getTempFilePath(t, "special")

	// Test with special characters
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "file",
		"path":      tmpFile,
		"content":   "特殊字符测试\n换行\t制表符",
		"mode":      "create",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Created")

	// Verify file content
	content, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "特殊字符测试")

	t.Logf("Special characters result: %s", result)
}

func TestRegister(t *testing.T) {
	config := DefaultConfig()

	err := Register(config)
	assert.NoError(t, err)
}

func TestRegisterDefault(t *testing.T) {
	err := RegisterDefault()
	assert.NoError(t, err)
}
