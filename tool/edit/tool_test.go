package edit

import (
	"context"
	"encoding/json"
	"fmt"
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

	assert.Equal(t, 10, config.MaxHistory)

	t.Logf("Default config: MaxHistory=%d", config.MaxHistory)
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
			name: "自定义历史数",
			config: Config{
				MaxHistory: 20,
			},
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

	result, err := invokable.InvokableRun(ctx, `{"operation": "line", "path": ""}`)
	require.NoError(t, err)
	assert.Contains(t, result, "File path cannot be empty")
}

func TestFileNotExist(t *testing.T) {
	// 用临时工作目录内的相对路径触发“文件不存在”分支。避免写死绝对路径：
	// Linux 上绝对路径（/nonexistent/...）会被路径安全检查判为逃逸工作区而提前报错，
	// 到不了文件存在性检查；Windows 上绝对路径无盘符不触发，导致本地通过、CI 失败。
	config := Config{WorkDir: t.TempDir()}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "line",
		"path":      "nonexistent_file.txt",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "File not found")
}

func TestUnsupportedOperation(t *testing.T) {
	config := Config{WorkDir: t.TempDir()}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Create a temp file for testing
	tmpFile := createTestFile(t, config.WorkDir, "test content")

	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "invalid_op",
		"path":      tmpFile,
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Operation not supported")
}

func createTestFile(t *testing.T, workspace, content string) string {
	tmpFile := filepath.Join(workspace, "test_edit_"+filepath.Base(t.Name())+".txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0644))
	t.Cleanup(func() {
		os.Remove(tmpFile)
	})
	rel, err := filepath.Rel(workspace, tmpFile)
	require.NoError(t, err)
	return rel
}

func buildParams(params map[string]interface{}) string {
	b, _ := json.Marshal(params)
	return string(b)
}

func TestEditLine(t *testing.T) {
	config := Config{WorkDir: t.TempDir()}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Create test file
	tmpFile := createTestFile(t, config.WorkDir, "line1\nline2\nline3\n")

	tests := []struct {
		name        string
		params      map[string]interface{}
		checkResult func(result string)
	}{
		{
			name: "修改第2行",
			params: map[string]interface{}{
				"operation":   "line",
				"path":        tmpFile,
				"line_number": 2,
				"new_content": "modified line2",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Success")
			},
		},
		{
			name: "行号为0",
			params: map[string]interface{}{
				"operation":   "line",
				"path":        tmpFile,
				"line_number": 0,
				"new_content": "test",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Line number out of range")
			},
		},
		{
			name: "行号超出范围",
			params: map[string]interface{}{
				"operation":   "line",
				"path":        tmpFile,
				"line_number": 100,
				"new_content": "test",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Line number out of range")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := invokable.InvokableRun(ctx, buildParams(tt.params))
			require.NoError(t, err)
			tt.checkResult(result)
			t.Logf("Result: %s", result)
		})
	}
}

func TestEditSearch(t *testing.T) {
	config := Config{WorkDir: t.TempDir()}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tests := []struct {
		name        string
		content     string
		params      map[string]interface{}
		checkResult func(result string)
	}{
		{
			name:    "搜索替换单个",
			content: "hello world\nhello universe",
			params: map[string]interface{}{
				"operation": "search",
				"search":    "hello",
				"replace":   "hi",
				"global":    false,
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Success")
			},
		},
		{
			name:    "搜索替换全部",
			content: "hello world\nhello universe",
			params: map[string]interface{}{
				"operation": "search",
				"search":    "hello",
				"replace":   "hi",
				"global":    true,
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Replaced 2")
			},
		},
		{
			name:    "搜索内容为空",
			content: "test content",
			params: map[string]interface{}{
				"operation": "search",
				"search":    "",
				"replace":   "new",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Search content cannot be empty")
			},
		},
		{
			name:    "未找到匹配",
			content: "test content",
			params: map[string]interface{}{
				"operation": "search",
				"search":    "notfound",
				"replace":   "new",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "No matches found")
			},
		},
		{
			name:    "正则替换单个",
			content: "hello123 world456",
			params: map[string]interface{}{
				"operation": "search",
				"search":    `[0-9]+`,
				"replace":   "NUM",
				"use_regex": true,
				"global":    false,
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Replaced 1")
			},
		},
		{
			name:    "正则替换全部",
			content: "hello123 world456",
			params: map[string]interface{}{
				"operation": "search",
				"search":    `[0-9]+`,
				"replace":   "NUM",
				"use_regex": true,
				"global":    true,
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Replaced 2")
			},
		},
		{
			name:    "正则表达式编译失败",
			content: "test content",
			params: map[string]interface{}{
				"operation": "search",
				"search":    `[invalid(regex`,
				"replace":   "new",
				"use_regex": true,
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Invalid regex")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := createTestFile(t, config.WorkDir, tt.content)
			tt.params["path"] = tmpFile

			result, err := invokable.InvokableRun(ctx, buildParams(tt.params))
			require.NoError(t, err)
			tt.checkResult(result)
			t.Logf("Result: %s", result)
		})
	}
}

func TestEditInsert(t *testing.T) {
	config := Config{WorkDir: t.TempDir()}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tests := []struct {
		name        string
		content     string
		params      map[string]interface{}
		checkResult func(result string)
	}{
		{
			name:    "在内容后插入",
			content: "line1\nline2\n",
			params: map[string]interface{}{
				"operation":    "insert",
				"new_content":  "inserted line",
				"insert_after": "line1",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Success")
			},
		},
		{
			name:    "在内容前插入",
			content: "line1\nline2\n",
			params: map[string]interface{}{
				"operation":     "insert",
				"new_content":   "inserted line",
				"insert_before": "line2",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Success")
			},
		},
		{
			name:    "插入内容为空",
			content: "test content",
			params: map[string]interface{}{
				"operation":   "insert",
				"new_content": "",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Content cannot be empty")
			},
		},
		{
			name:    "未指定插入位置",
			content: "test content",
			params: map[string]interface{}{
				"operation":   "insert",
				"new_content": "test",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "insert_after or insert_before")
			},
		},
		{
			name:    "未找到插入位置",
			content: "test content",
			params: map[string]interface{}{
				"operation":    "insert",
				"new_content":  "test",
				"insert_after": "notfound",
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Insert position not found")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := createTestFile(t, config.WorkDir, tt.content)
			tt.params["path"] = tmpFile

			result, err := invokable.InvokableRun(ctx, buildParams(tt.params))
			require.NoError(t, err)
			tt.checkResult(result)
			t.Logf("Result: %s", result)
		})
	}
}

func TestEditDelete(t *testing.T) {
	config := Config{WorkDir: t.TempDir()}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	tests := []struct {
		name        string
		content     string
		params      map[string]interface{}
		checkResult func(result string)
	}{
		{
			name:    "删除单行",
			content: "line1\nline2\nline3\n",
			params: map[string]interface{}{
				"operation":    "delete",
				"delete_lines": []int{2},
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Success")
			},
		},
		{
			name:    "删除多行",
			content: "line1\nline2\nline3\nline4\n",
			params: map[string]interface{}{
				"operation":    "delete",
				"delete_lines": []int{2, 3},
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Deleted 2")
			},
		},
		{
			name:    "删除行号为空",
			content: "test content",
			params: map[string]interface{}{
				"operation":    "delete",
				"delete_lines": []int{},
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "Delete line numbers cannot be empty")
			},
		},
		{
			name:    "无效的删除行号",
			content: "test content",
			params: map[string]interface{}{
				"operation":    "delete",
				"delete_lines": []int{100, 200},
			},
			checkResult: func(result string) {
				assert.Contains(t, result, "No valid line numbers")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := createTestFile(t, config.WorkDir, tt.content)
			tt.params["path"] = tmpFile

			result, err := invokable.InvokableRun(ctx, buildParams(tt.params))
			require.NoError(t, err)
			tt.checkResult(result)
			t.Logf("Result: %s", result)
		})
	}
}

func TestBackup(t *testing.T) {
	// Create temp workspace
	tmpWorkspace := filepath.Join(os.TempDir(), "test_edit_backup")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	config := Config{
		WorkDir:    tmpWorkspace,
		MaxHistory: 3,
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Create test file
	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("original content"), 0644)
	require.NoError(t, err)

	// Edit the file multiple times
	for i := 1; i <= 4; i++ {
		result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
			"operation":   "line",
			"path":        "test.txt",
			"line_number": 1,
			"new_content": fmt.Sprintf("modified content %d", i),
		}))
		require.NoError(t, err)
		assert.Contains(t, result, "Success")
		assert.Contains(t, result, fmt.Sprintf(`(v%d)`, i))
		t.Logf("Edit %d result: %s", i, result)
	}

	// List backups
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "list_backups",
		"path":      "test.txt",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Backup")
	t.Logf("List backups result: %s", result)

	// Restore to version 2
	// Note: Backup v2 was created BEFORE edit 2, so it contains "modified content 1"
	result, err = invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "restore",
		"path":      "test.txt",
		"version":   2,
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Success")
	assert.Contains(t, result, "v2")
	t.Logf("Restore result: %s", result)

	// Verify restored content
	// Backup v2 = content before edit 2 = "modified content 1"
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "modified content 1")
}

func TestRestoreInvalidVersion(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_edit_restore_invalid")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	config := Config{
		WorkDir:    tmpWorkspace,
		MaxHistory: 3,
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Test restore with invalid version
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "restore",
		"path":      "test.txt",
		"version":   0,
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "valid version")

	// Test restore with non-existent version
	result, err = invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "restore",
		"path":      "test.txt",
		"version":   999,
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Failed")
}

func TestListBackupsEmpty(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_edit_list_empty")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	config := Config{
		WorkDir:    tmpWorkspace,
		MaxHistory: 3,
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// List backups for file with no backups
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "list_backups",
		"path":      "nonexistent.txt",
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "No backups")
}

func TestMaxHistoryCleanup(t *testing.T) {
	tmpWorkspace := filepath.Join(os.TempDir(), "test_edit_max_history")
	err := os.MkdirAll(tmpWorkspace, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(tmpWorkspace)

	maxHistory := 2
	config := Config{
		WorkDir:    tmpWorkspace,
		MaxHistory: maxHistory,
	}

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Create test file
	testFile := filepath.Join(tmpWorkspace, "test.txt")
	err = os.WriteFile(testFile, []byte("original"), 0644)
	require.NoError(t, err)

	// Create more backups than MaxHistory
	for i := 1; i <= 4; i++ {
		_, err = invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
			"operation":   "line",
			"path":        "test.txt",
			"line_number": 1,
			"new_content": fmt.Sprintf("content %d", i),
		}))
		require.NoError(t, err)
	}

	// List backups - should only have MaxHistory backups
	result, err := invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "list_backups",
		"path":      "test.txt",
	}))
	require.NoError(t, err)

	// Count the number of backups in result
	// Should be 2 (MaxHistory), not 4
	assert.Contains(t, result, "Backup")

	// Try to restore to version 1 (should fail - cleaned up)
	result, err = invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "restore",
		"path":      "test.txt",
		"version":   1,
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Failed")

	// Try to restore to version 3 (should succeed)
	result, err = invokable.InvokableRun(ctx, buildParams(map[string]interface{}{
		"operation": "restore",
		"path":      "test.txt",
		"version":   3,
	}))
	require.NoError(t, err)
	assert.Contains(t, result, "Success")
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
