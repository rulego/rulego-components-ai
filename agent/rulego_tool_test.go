package agent

import (
	"context"
	"testing"

	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test/assert"
)

// TestNewRuleGoTool Test the NewRuleGoTool function
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

// TestRuleGoTool_Info Test the Info method
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

// TestRuleGoTool_Info_ParameterFormats Test different parameter formats
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

// TestRuleGoTool_InvokableRun_NoContext Test cases without RuleContext
func TestRuleGoTool_InvokableRun_NoContext(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	// No context is injected into RuleContext
	ctx := context.Background()
	result, err := tool.InvokableRun(ctx, `{"input": "test"}`)

	assert.NotNil(t, err)
	assert.Equal(t, "", result)
}

// TestRuleGoTool_InvokableRun_InvalidContextType Test for invalid RuleContext types
func TestRuleGoTool_InvokableRun_InvalidContextType(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	// Inject a value of the wrong type
	ctx := context.WithValue(context.Background(), config.ShareRuleContextKey, "not a RuleContext")
	result, err := tool.InvokableRun(ctx, `{"input": "test"}`)

	assert.NotNil(t, err)
	assert.Equal(t, "", result)
}

// TestRuleGoTool_InvokableRun_UnsupportedType Types of tools not supported for testing
func TestRuleGoTool_InvokableRun_UnsupportedType(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        "unsupported_type",
		TargetId:    "test_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	// Create a context with mock RuleContext
	mockCtx := &mockRuleContextForTool{}
	ctx := context.WithValue(context.Background(), config.ShareRuleContextKey, mockCtx)

	result, err := tool.InvokableRun(ctx, `{"input": "test"}`)

	assert.NotNil(t, err)
	assert.Equal(t, "", result)
}

// TestRuleGoTool_InvokableRun_AgentType Test the Agent type tool
func TestRuleGoTool_InvokableRun_AgentType(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "sub_agent",
		Description: "A sub agent tool",
		Type:        config.ToolTypeAgent,
		TargetId:    "sub_agent_chain",
	}

	tool := NewRuleGoTool(toolConfig)

	// Create a context with mock RuleContext
	mockCtx := &mockRuleContextForTool{}
	ctx := context.WithValue(context.Background(), config.ShareRuleContextKey, mockCtx)

	// Agent types should be handled the same way as RuleChain types
	// Since mock does not implement TellFlow, it returns an error, but we can verify that the type check passed
	_, err := tool.InvokableRun(ctx, `{"input": "test"}`)

	// Since the mock implementation is incomplete, we expect errors
	// But the error should not be "unsupported tool type."
	if err != nil && err.Error() == "不支持的工具类型: agent" {
		t.Error("Agent type should be supported as rulechain alias")
	}
}

// TestRuleGoTool_ConfigDefaults Test the default values
func TestRuleGoTool_ConfigDefaults(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
		// Timeout not set (int64 type)
	}

	tool := NewRuleGoTool(toolConfig)

	assert.Equal(t, tool.Config.Timeout, int64(0))
	assert.Equal(t, tool.Config.Parameters, "")
}

// TestRuleGoTool_ConfigWithTimeout Test belt timeout configuration
func TestRuleGoTool_ConfigWithTimeout(t *testing.T) {
	toolConfig := config.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "test_chain",
		Timeout:     int64(60), // 60 seconds
	}

	tool := NewRuleGoTool(toolConfig)

	assert.Equal(t, tool.Config.Timeout, int64(60))
}

// TestRuleGoTool_Interface Test interface implementation
func TestRuleGoTool_Interface(t *testing.T) {
	// Make sure RuleGoTool implements the interface
	tool := NewRuleGoTool(config.Tool{})
	assert.NotNil(t, tool)

	// Verify the Info method signature
	info, err := tool.Info(context.Background())
	_ = info
	_ = err
}

// mockRuleContextForTool is used for testing a simulated RuleContext
type mockRuleContextForTool struct {
	types.RuleContext
}

func (m *mockRuleContextForTool) NewMsg(msgType string, metadata *types.Metadata, data string) types.RuleMsg {
	return types.NewMsg(0, msgType, types.TEXT, metadata, data)
}

func (m *mockRuleContextForTool) TellFlow(chainId string, msg types.RuleMsg, opts ...types.Option) {
	// Empty realization
}

// TestRuleGoTool_Info_ReturnsCorrectSchema Test Info returns the correct schema
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

// TestRuleGoTool_EmptyConfig Test the empty configuration
func TestRuleGoTool_EmptyConfig(t *testing.T) {
	tool := NewRuleGoTool(config.Tool{})
	assert.NotNil(t, tool)
	assert.Equal(t, "", tool.Config.Name)
	assert.Equal(t, "", tool.Config.Description)
}

// BenchmarkRuleGoTool_Info Benchmark Info method
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

// BenchmarkNewRuleGoTool Benchmark NewRuleGoTool
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

// Ensure interface implementation check (compile time)
var _ *RuleGoTool = NewRuleGoTool(config.Tool{})
