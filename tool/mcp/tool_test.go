package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTool is a tool for creating MCPs
func TestNewTool(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "默认配置",
			config: Config{
				Server:        "python server.py",
				Timeout:       30,
				ClientName:    "Test Agent",
				ClientVersion: "1.0.0",
			},
			wantErr: false,
		},
		{
			name: "零超时自动设置默认值",
			config: Config{
				Server:        "node server.js",
				Timeout:       0,
				ClientName:    "",
				ClientVersion: "",
			},
			wantErr: false,
		},
		{
			name: "HTTP 服务器配置",
			config: Config{
				Server:        "http://localhost:8080/mcp",
				Timeout:       60,
				ClientName:    "HTTP Agent",
				ClientVersion: "2.0.0",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewTool(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, got)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, got)
			}
		})
	}
}

// TestInfo returns test tool information
func TestInfo(t *testing.T) {
	config := Config{
		Server: "python server.py",
	}
	tTool, err := NewTool(config)
	require.NoError(t, err)

	ctx := context.Background()
	info, err := tTool.Info(ctx)
	require.NoError(t, err)

	assert.Equal(t, ToolName, info.Name)
	assert.Contains(t, info.Desc, "MCP 服务器")
	assert.Contains(t, info.Desc, "python server.py")
	assert.NotNil(t, info.ParamsOneOf)
}

// TestDefaultConfig Tests the default configuration
func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	assert.Equal(t, 30, config.Timeout)
	assert.Equal(t, "RuleGo AI Agent", config.ClientName)
	assert.Equal(t, "1.0.0", config.ClientVersion)
}

// TestInvokableRun_MissingToolName Testing lacks tool_name parameters
func TestInvokableRun_MissingToolName(t *testing.T) {
	tTool, err := NewTool(Config{Server: "python server.py"})
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	arguments := `{
		"arguments": {"param1": "value1"}
	}`

	result, err := invokable.InvokableRun(context.Background(), arguments)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tool_name 不能为空")
	assert.Empty(t, result)
}

// TestInvokableRun_EmptyToolName Testing empty tool_name parameters
func TestInvokableRun_EmptyToolName(t *testing.T) {
	tTool, err := NewTool(Config{Server: "python server.py"})
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	arguments := `{
		"tool_name": "",
		"arguments": {"param1": "value1"}
	}`

	result, err := invokable.InvokableRun(context.Background(), arguments)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tool_name 不能为空")
	assert.Empty(t, result)
}

// TestInvokableRun_InvalidJSON Test for invalid JSON parameters
func TestInvokableRun_InvalidJSON(t *testing.T) {
	tTool, err := NewTool(Config{Server: "python server.py"})
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	arguments := `invalid json`

	result, err := invokable.InvokableRun(context.Background(), arguments)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "解析参数失败")
	assert.Empty(t, result)
}

// TestInvokableRun_ValidArguments Analyze effective parameters for testing
func TestInvokableRun_ValidArguments(t *testing.T) {
	tTool, err := NewTool(Config{Server: "python server.py"})
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	arguments := `{
		"tool_name": "test_tool",
		"arguments": {"param1": "value1", "param2": 123}
	}`

	result, err := invokable.InvokableRun(ctx, arguments)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "初始化 MCP 客户端失败")
	assert.Empty(t, result)
}

// TestParseCommand test command parsing function
func TestParseCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "简单命令",
			input:    "python server.py",
			expected: []string{"python", "server.py"},
		},
		{
			name:     "带参数的命令",
			input:    "node server.js --port 8080",
			expected: []string{"node", "server.js", "--port", "8080"},
		},
		{
			name:     "带引号的参数",
			input:    `python "path with spaces/server.py" --arg "value with spaces"`,
			expected: []string{"python", "path with spaces/server.py", "--arg", "value with spaces"},
		},
		{
			name:     "单引号",
			input:    `python 'path/to/server.py'`,
			expected: []string{"python", "path/to/server.py"},
		},
		{
			name:     "空命令",
			input:    "",
			expected: []string{},
		},
		{
			name:     "仅空格",
			input:    "   ",
			expected: []string{},
		},
		{
			name:     "混合引号",
			input:    `python "arg1" 'arg2' arg3`,
			expected: []string{"python", "arg1", "arg2", "arg3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCommand(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestRegister test tool registration
func TestRegister(t *testing.T) {
	config := Config{
		Server:        "python server.py",
		Timeout:       60,
		ClientName:    "Test Agent",
		ClientVersion: "1.0.0",
	}

	err := Register(config)
	assert.NoError(t, err)
}

// TestRegisterDefault tests are registered by default
func TestRegisterDefault(t *testing.T) {
	err := RegisterDefault()
	assert.NoError(t, err)
}

// TestInvokableRun_HTTPServer Test HTTP server configuration
func TestInvokableRun_HTTPServer(t *testing.T) {
	tTool, err := NewTool(Config{Server: "http://localhost:8080/mcp"})
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	arguments := `{
		"tool_name": "test_tool",
		"arguments": {"param": "value"}
	}`

	result, err := invokable.InvokableRun(ctx, arguments)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "初始化 MCP 客户端失败")
	assert.Empty(t, result)
}

// TestInvokableRun_InvalidCommand Test for invalid commands
func TestInvokableRun_InvalidCommand(t *testing.T) {
	tTool, err := NewTool(Config{Server: "invalid-command-that-does-not-exist-xyz123"})
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	arguments := `{
		"tool_name": "test_tool",
		"arguments": {"param": "value"}
	}`

	result, err := invokable.InvokableRun(ctx, arguments)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "启动 MCP 客户端失败")
	assert.Empty(t, result)
}

// TestInvokableRun_NoArguments tests do not have arguments parameters
func TestInvokableRun_NoArguments(t *testing.T) {
	tTool, err := NewTool(Config{Server: "python server.py"})
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	arguments := `{
		"tool_name": "test_tool"
	}`

	result, err := invokable.InvokableRun(ctx, arguments)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "初始化 MCP 客户端失败")
	assert.Empty(t, result)
}

// TestToolWithEmptyServer tests the empty server configuration
func TestToolWithEmptyServer(t *testing.T) {
	config := Config{
		Server:  "",
		Timeout: 30,
	}
	tTool, err := NewTool(config)
	require.NoError(t, err)

	ctx := context.Background()
	info, err := tTool.Info(ctx)
	require.NoError(t, err)

	assert.Equal(t, ToolName, info.Name)
	assert.Contains(t, info.Desc, "")
}

// TestToolConcurrency tests concurrency security
func TestToolConcurrency(t *testing.T) {
	tTool, err := NewTool(Config{Server: "python server.py"})
	require.NoError(t, err)

	ctx := context.Background()

	done := make(chan bool, 2)
	go func() {
		_, err := tTool.Info(ctx)
		assert.NoError(t, err)
		done <- true
	}()
	go func() {
		_, err := tTool.Info(ctx)
		assert.NoError(t, err)
		done <- true
	}()

	<-done
	<-done
}
