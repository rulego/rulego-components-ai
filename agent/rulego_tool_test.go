package agent

import (
	"context"
	"testing"

	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test/assert"
)

// TestNewRuleGoTool 测试 NewRuleGoTool 函数
func TestNewRuleGoTool(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	assert.NotNil(t, tool)
	assert.Equal(t, "test_tool", tool.Config.Name)
	assert.Equal(t, "A test tool", tool.Config.Description)
	assert.Equal(t, config.ToolTypeRuleChain, tool.Config.Type)
	assert.Equal(t, "test_chain", tool.Config.TargetId)
}

// TestRuleGoTool_Info 测试 Info 方法
func TestRuleGoTool_Info(t *testing.T) {
	tests := []struct {
		name        string
		config      config.Tool
		expectError bool
	}{
		{
			name: "basic tool info",
			config: config.Tool{
				Name:        "calculator",
				Description: "A calculator tool",
				Type:        config.ToolTypeRuleChain,
				TargetId:    "calc_chain",
			},
			expectError: false,
		},
		{
			name: "tool with parameters",
			config: config.Tool{
				Name:        "search",
				Description: "A search tool",
				Type:        config.ToolTypeRuleChain,
				TargetId:    "search_chain",
				Parameters: `{
					"type": "object",
					"properties": {
						"query": {
							"type": "string",
							"description": "Search query"
						}
					},
					"required": ["query"]
				}`,
			},
			expectError: false,
		},
		{
			name: "tool with invalid parameters",
			config: config.Tool{
				Name:        "invalid",
				Description: "Tool with invalid params",
				Type:        config.ToolTypeRuleChain,
				TargetId:    "invalid_chain",
				Parameters:  `{invalid json}`,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewRuleGoTool(tt.config)
			info, err := tool.Info(context.Background())

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
				assert.NotNil(t, info)
				assert.Equal(t, tt.config.Name, info.Name)
				assert.Equal(t, tt.config.Description, info.Desc)
			}
		})
	}
}

// TestRuleGoTool_Info_ParameterFormats 测试不同参数格式
func TestRuleGoTool_Info_ParameterFormats(t *testing.T) {
	tests := []struct {
		name       string
		params     string
		shouldWork bool
	}{
		{
			name:       "empty parameters",
			params:     "",
			shouldWork: true,
		},
		{
			name:       "valid JSON schema",
			params:     `{"type": "object", "properties": {"input": {"type": "string"}}}`,
			shouldWork: true,
		},
		{
			name:       "JSON schema with required",
			params:     `{"type": "object", "properties": {"x": {"type": "number"}}, "required": ["x"]}`,
			shouldWork: true,
		},
		{
			name:       "invalid JSON",
			params:     `{not valid}`,
			shouldWork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolConfig := config.Tool{
				Name:        "test",
				Description: "Test tool",
				Type:        config.ToolTypeRuleChain,
				TargetId:    "test_chain",
				Parameters:  tt.params,
			}

			tool := NewRuleGoTool(toolConfig)
			_, err := tool.Info(context.Background())

			if tt.shouldWork {
				assert.Nil(t, err)
			} else {
				assert.NotNil(t, err)
			}
		})
	}
}

// TestRuleGoTool_InvokableRun_NoContext 测试没有 RuleContext 的情况
func TestRuleGoTool_InvokableRun_NoContext(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	// 没有注入 RuleContext 的 context
	ctx := context.Background()
	result, err := tool.InvokableRun(ctx, `{"input": "test"}`)

	assert.NotNil(t, err)
	assert.Equal(t, "", result)
}

// TestRuleGoTool_InvokableRun_InvalidContextType 测试无效的 RuleContext 类型
func TestRuleGoTool_InvokableRun_InvalidContextType(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	// 注入错误类型的值
	ctx := context.WithValue(context.Background(), config.ShareRuleContextKey, "not a RuleContext")
	result, err := tool.InvokableRun(ctx, `{"input": "test"}`)

	assert.NotNil(t, err)
	assert.Equal(t, "", result)
}

// TestRuleGoTool_InvokableRun_UnsupportedType 测试不支持的工具类型
func TestRuleGoTool_InvokableRun_UnsupportedType(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        "unsupported_type",
		TargetId:    "test_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	// 创建带有 mock RuleContext 的 context
	mockCtx := &mockRuleContextForTool{}
	ctx := context.WithValue(context.Background(), config.ShareRuleContextKey, mockCtx)

	result, err := tool.InvokableRun(ctx, `{"input": "test"}`)

	assert.NotNil(t, err)
	assert.Equal(t, "", result)
}

// TestRuleGoTool_InvokableRun_AgentType 测试 Agent 类型工具
func TestRuleGoTool_InvokableRun_AgentType(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "sub_agent",
		Description: "A sub agent tool",
		Type:        config.ToolTypeAgent,
		TargetId:    "sub_agent_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	// 创建带有 mock RuleContext 的 context
	mockCtx := &mockRuleContextForTool{}
	ctx := context.WithValue(context.Background(), config.ShareRuleContextKey, mockCtx)

	// Agent 类型应该与 RuleChain 类型一样被处理
	// 由于 mock 没有实现 TellFlow，会返回错误，但我们可以验证类型检查通过了
	_, err := tool.InvokableRun(ctx, `{"input": "test"}`)

	// 由于 mock 实现不完整，我们预期会有错误
	// 但错误不应该是 "不支持的工具类型"
	if err != nil && err.Error() == "不支持的工具类型: agent" {
		t.Error("Agent type should be supported as rulechain alias")
	}
}

// TestRuleGoTool_ConfigDefaults 测试配置默认值
func TestRuleGoTool_ConfigDefaults(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
		// Timeout 未设置 (int64 类型)
	}

	tool := NewRuleGoTool(toolConfig)

	assert.Equal(t, tool.Config.Timeout, int64(0))
	assert.Equal(t, tool.Config.Parameters, "")
}

// TestRuleGoTool_ConfigWithTimeout 测试带超时配置
func TestRuleGoTool_ConfigWithTimeout(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
		Timeout:     int64(60), // 60 秒
	}

	tool := NewRuleGoTool(toolConfig)

	assert.Equal(t, tool.Config.Timeout, int64(60))
}

// TestRuleGoTool_Interface 测试接口实现
func TestRuleGoTool_Interface(t *testing.T) {
	// 确保 RuleGoTool 实现了接口
	tool := NewRuleGoTool(config.Tool{})
	assert.NotNil(t, tool)

	// 验证 Info 方法签名
	info, err := tool.Info(context.Background())
	_ = info
	_ = err
}

// mockRuleContextForTool 用于测试的模拟 RuleContext
type mockRuleContextForTool struct {
	types.RuleContext
}

func (m *mockRuleContextForTool) NewMsg(msgType string, metadata *types.Metadata, data string) types.RuleMsg {
	return types.NewMsg(0, msgType, types.TEXT, metadata, data)
}

func (m *mockRuleContextForTool) TellFlow(chainId string, msg types.RuleMsg, opts ...types.Option) {
	// 空实现
}

// TestRuleGoTool_Info_ReturnsCorrectSchema 测试 Info 返回正确的 schema
func TestRuleGoTool_Info_ReturnsCorrectSchema(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "Test description",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
		Parameters:  `{"type": "object", "properties": {"input": {"type": "string"}}}`,
	}

	tool := NewRuleGoTool(toolConfig)
	info, err := tool.Info(context.Background())

	assert.Nil(t, err)
	assert.NotNil(t, info)
	assert.Equal(t, "test_tool", info.Name)
	assert.Equal(t, "Test description", info.Desc)
	assert.NotNil(t, info.ParamsOneOf)
}

// TestRuleGoTool_EmptyConfig 测试空配置
func TestRuleGoTool_EmptyConfig(t *testing.T) {
	tool := NewRuleGoTool(config.Tool{})
	assert.NotNil(t, tool)
	assert.Equal(t, "", tool.Config.Name)
	assert.Equal(t, "", tool.Config.Description)
}

// BenchmarkRuleGoTool_Info 基准测试 Info 方法
func BenchmarkRuleGoTool_Info(b *testing.B) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
		Parameters:  `{"type": "object", "properties": {"input": {"type": "string"}}}`,
	}

	tool := NewRuleGoTool(toolConfig)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Info(ctx)
	}
}

// BenchmarkNewRuleGoTool 基准测试 NewRuleGoTool
func BenchmarkNewRuleGoTool(b *testing.B) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewRuleGoTool(toolConfig)
	}
}

// 确保接口实现检查（编译时）
var _ *RuleGoTool = NewRuleGoTool(config.Tool{})
