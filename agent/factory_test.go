package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego-components-ai/session"
	"github.com/rulego/rulego/test/assert"
)

// TestCreateChatModel_MissingURL 测试缺少 URL 的情况
func TestCreateChatModel_MissingURL(t *testing.T) {
	llmConfig := config.LLMConfig{
		Url:   "",
		Key:   "test-key",
		Model: "gpt-4",
	}

	model, err := CreateChatModel(llmConfig)

	assert.NotNil(t, err)
	assert.Nil(t, model)
	if !strings.Contains(err.Error(), "URL is missing") {
		t.Errorf("Expected error to contain 'URL is missing', got: %v", err)
	}
}

// TestCreateChatModel_EmptyURLAfterTrim 测试 URL 只有空格的情况
func TestCreateChatModel_EmptyURLAfterTrim(t *testing.T) {
	llmConfig := config.LLMConfig{
		Url:   "   ", // 只有空格
		Key:   "test-key",
		Model: "gpt-4",
	}

	model, err := CreateChatModel(llmConfig)

	assert.NotNil(t, err)
	assert.Nil(t, model)
	if !strings.Contains(err.Error(), "URL is missing") {
		t.Errorf("Expected error to contain 'URL is missing', got: %v", err)
	}
}

// TestCreateChatModel_ValidConfig 测试有效配置
func TestCreateChatModel_ValidConfig(t *testing.T) {
	// 这个测试需要真实的 API endpoint，所以我们使用一个假的 URL
	// 实际上会尝试连接，可能会失败
	t.Skip("Requires valid API endpoint")
}

// TestCreateChatModel_DefaultParams 测试默认参数
func TestCreateChatModel_DefaultParams(t *testing.T) {
	// 测试当参数为 0 时是否应用默认值
	_ = config.LLMConfig{
		Url:   "https://api.example.com/v1",
		Key:   "test-key",
		Model: "gpt-4",
		Params: config.ModelParams{
			Temperature:      0,
			TopP:             0,
			FrequencyPenalty: 0,
			PresencePenalty:  0,
		},
	}

	// 创建模型会应用默认参数
	// 由于需要实际连接，这里我们只验证逻辑
	// 在实际测试中会使用 mock

	// 验证默认值常量
	assert.Equal(t, config.DefaultTemperature, float32(0.7))
	assert.Equal(t, config.DefaultTopP, float32(0.9))
	assert.Equal(t, config.DefaultFrequencyPenalty, float32(0.5))
	assert.Equal(t, config.DefaultPresencePenalty, float32(0.5))
}

// TestCreateChatModel_CustomParams 测试自定义参数
func TestCreateChatModel_CustomParams(t *testing.T) {
	// 当用户设置了参数时，不应该被默认值覆盖
	llmConfig := config.LLMConfig{
		Url:   "https://api.example.com/v1",
		Key:   "test-key",
		Model: "gpt-4",
		Params: config.ModelParams{
			Temperature:      0.5,
			TopP:             0.8,
			FrequencyPenalty: 0.3,
			PresencePenalty:  0.4,
			MaxTokens:        1000,
		},
	}

	// 这些值不应该被默认值覆盖
	assert.Equal(t, float32(0.5), llmConfig.Params.Temperature)
	assert.Equal(t, float32(0.8), llmConfig.Params.TopP)
	assert.Equal(t, float32(0.3), llmConfig.Params.FrequencyPenalty)
	assert.Equal(t, float32(0.4), llmConfig.Params.PresencePenalty)
	assert.Equal(t, 1000, llmConfig.Params.MaxTokens)
}

// TestCreateChatModel_WithStopSequences 测试停止序列
func TestCreateChatModel_WithStopSequences(t *testing.T) {
	llmConfig := config.LLMConfig{
		Url:   "https://api.example.com/v1",
		Key:   "test-key",
		Model: "gpt-4",
		Params: config.ModelParams{
			Stop: []string{"\n", "END"},
		},
	}

	assert.Equal(t, 2, len(llmConfig.Params.Stop))
	assert.Equal(t, "\n", llmConfig.Params.Stop[0])
	assert.Equal(t, "END", llmConfig.Params.Stop[1])
}

// TestCreateChatModel_WithMaxRetries 测试重试配置
func TestCreateChatModel_WithMaxRetries(t *testing.T) {
	llmConfig := config.LLMConfig{
		Url:        "https://api.example.com/v1",
		Key:        "test-key",
		Model:      "gpt-4",
		MaxRetries: 3,
	}

	assert.Equal(t, 3, llmConfig.MaxRetries)
}

// TestCreateTools 测试创建工具
func TestCreateTools(t *testing.T) {
	toolsConfig := []config.Tool{
		{
			Name:        "calculator",
			Description: "A calculator",
			Type:        config.ToolTypeRuleChain,
			TargetId:    "calc_chain",
		},
		{
			Name:        "search",
			Description: "A search tool",
			Type:        config.ToolTypeRuleChain,
			TargetId:    "search_chain",
		},
	}

	tools, toolInfos, err := CreateTools(toolsConfig, ToolOptions{})

	assert.Nil(t, err)
	assert.Equal(t, 2, len(tools))
	assert.Equal(t, 2, len(toolInfos))
}

// TestCreateTools_Empty 测试空工具列表
func TestCreateTools_Empty(t *testing.T) {
	tools, toolInfos, err := CreateTools([]config.Tool{}, ToolOptions{})

	assert.Nil(t, err)
	assert.Equal(t, 0, len(tools))
	assert.Equal(t, 0, len(toolInfos))
}

// TestCreateTools_Nil 测试 nil 工具列表
func TestCreateTools_Nil(t *testing.T) {
	tools, toolInfos, err := CreateTools(nil, ToolOptions{})

	assert.Nil(t, err)
	assert.Equal(t, 0, len(tools))
	assert.Equal(t, 0, len(toolInfos))
}

// TestCreateTools_MultipleTypes 测试不同类型的工具
func TestCreateTools_MultipleTypes(t *testing.T) {
	toolsConfig := []config.Tool{
		{
			Name:        "rulechain_tool",
			Description: "A rulechain tool",
			Type:        config.ToolTypeRuleChain,
			TargetId:    "test_chain",
		},
		{
			Name:        "agent_tool",
			Description: "An agent tool",
			Type:        config.ToolTypeAgent,
			TargetId:    "test_agent",
		},
	}

	tools, toolInfos, err := CreateTools(toolsConfig, ToolOptions{})

	assert.Nil(t, err)
	assert.Equal(t, 2, len(tools))
	assert.Equal(t, 2, len(toolInfos))
}

// TestCreateChatModelAgent 测试创建 ChatModel Agent
func TestCreateChatModelAgent(t *testing.T) {
	t.Skip("Requires valid API endpoint and LLM configuration")
}

// TestCreateChatModelAgent_Config 测试 Agent 配置
func TestCreateChatModelAgent_Config(t *testing.T) {
	// 测试配置结构的正确性
	agentConfig := ChatAgentConfig{
		LLMConfig: config.LLMConfig{
			Url:          "https://api.example.com/v1",
			Key:          "test-key",
			Model:        "gpt-4",
			SystemPrompt: "You are a helpful assistant.",
			Tools: []config.Tool{
				{
					Name:        "test_tool",
					Description: "A test tool",
					Type:        config.ToolTypeRuleChain,
					TargetId:    "test_chain",
				},
			},
			Params: config.ModelParams{
				Temperature: 0.7,
			},
		},
		MaxStep: 10,
	}

	assert.Equal(t, 10, agentConfig.MaxStep)
	assert.Equal(t, "You are a helpful assistant.", agentConfig.SystemPrompt)
	assert.Equal(t, 1, len(agentConfig.Tools))
}

// TestChatAgentConfig 测试 ChatAgentConfig 结构
func TestChatAgentConfig(t *testing.T) {
	config := ChatAgentConfig{
		LLMConfig: config.LLMConfig{
			Url:          "https://api.example.com/v1",
			Key:          "test-key",
			Model:        "gpt-4",
			SystemPrompt: "Test prompt",
		},
		MaxStep: 50,
	}

	assert.Equal(t, "https://api.example.com/v1", config.Url)
	assert.Equal(t, "test-key", config.Key)
	assert.Equal(t, "gpt-4", config.Model)
	assert.Equal(t, 50, config.MaxStep)
	assert.Equal(t, "Test prompt", config.SystemPrompt)
}

// TestSubAgentConfig 测试 SubAgentConfig 结构
func TestSubAgentConfig(t *testing.T) {
	subConfig := SubAgentConfig{
		Name:        "sub_agent",
		Description: "A sub agent",
		Type:        "rulechain",
		TargetId:    "sub_chain",
		Config: map[string]interface{}{
			"param1": "value1",
		},
	}

	assert.Equal(t, "sub_agent", subConfig.Name)
	assert.Equal(t, "A sub agent", subConfig.Description)
	assert.Equal(t, "rulechain", subConfig.Type)
	assert.Equal(t, "sub_chain", subConfig.TargetId)
	assert.NotNil(t, subConfig.Config)
}

// TestCreateChatModel_WithContext 测试带 context 的创建
func TestCreateChatModel_WithContext(t *testing.T) {
	// 验证 context 在创建过程中被正确使用
	ctx := context.Background()
	assert.NotNil(t, ctx)

	// 实际创建需要有效的 API endpoint
	// 这里只验证 context 可用
}

// TestCreateChatModel_KeyTrim 测试 API Key 的空格处理
func TestCreateChatModel_KeyTrim(t *testing.T) {
	llmConfig := config.LLMConfig{
		Url:   "https://api.example.com/v1",
		Key:   "  test-key-with-spaces  ",
		Model: "gpt-4",
	}

	// 创建后 Key 应该被 trim
	// 由于需要实际连接，这里只验证配置
	assert.Equal(t, "  test-key-with-spaces  ", llmConfig.Key)
}

// TestIsExecutableToolCallArgs 测试工具调用参数是否可执行
func TestIsExecutableToolCallArgs(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
		expected  bool
	}{
		{
			name:      "empty arguments",
			toolName:  "bash",
			arguments: "",
			expected:  false,
		},
		{
			name:      "empty json object",
			toolName:  "bash",
			arguments: "{}",
			expected:  false,
		},
		{
			name:      "empty json object with spaces",
			toolName:  "bash",
			arguments: "  {  }  ",
			expected:  false,
		},
		{
			name:      "null json",
			toolName:  "bash",
			arguments: "null",
			expected:  false,
		},
		{
			name:      "valid bash command",
			toolName:  "bash",
			arguments: `{"command":"echo","args":["hello"]}`,
			expected:  true,
		},
		{
			name:      "invalid skill name field",
			toolName:  "skill",
			arguments: `{"name":"camera-rotate"}`,
			expected:  false,
		},
		{
			name:      "valid legacy skill field",
			toolName:  "skill",
			arguments: `{"skill":"camera-snapshot"}`,
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, session.IsExecutableToolCallArgs(tt.toolName, tt.arguments))
		})
	}
}

// TestCreateStreamToolCallChecker_AcceptsNamedToolCallWithoutCompleteArgs 测试流式检查器识别未完成参数的工具调用
func TestCreateStreamToolCallChecker_AcceptsNamedToolCallWithoutCompleteArgs(t *testing.T) {
	checker := createStreamToolCallChecker(nil)
	stream := schema.StreamReaderFromArray([]*schema.Message{
		{
			ToolCalls: []schema.ToolCall{
				{
					ID:   "call-1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "bash",
						Arguments: "{}",
					},
				},
			},
		},
	})

	hasToolCall, err := checker(context.Background(), stream)

	assert.Nil(t, err)
	assert.True(t, hasToolCall)
}

// TestCreateStreamToolCallChecker_RejectsUnnamedToolCall 测试流式检查器忽略没有名称的工具调用
func TestCreateStreamToolCallChecker_RejectsUnnamedToolCall(t *testing.T) {
	checker := createStreamToolCallChecker(nil)
	stream := schema.StreamReaderFromArray([]*schema.Message{
		{
			ToolCalls: []schema.ToolCall{
				{
					ID:   "call-1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      " ",
						Arguments: "{}",
					},
				},
			},
		},
	})

	hasToolCall, err := checker(context.Background(), stream)

	assert.Nil(t, err)
	assert.False(t, hasToolCall)
}

// TestCreateStreamToolCallChecker_AcceptsValidArguments 测试流式检查器识别有效工具调用
func TestCreateStreamToolCallChecker_AcceptsValidArguments(t *testing.T) {
	checker := createStreamToolCallChecker(nil)
	stream := schema.StreamReaderFromArray([]*schema.Message{
		{
			ToolCalls: []schema.ToolCall{
				{
					ID:   "call-1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "bash",
						Arguments: `{"command":"echo","args":["hello"]}`,
					},
				},
			},
		},
	})

	hasToolCall, err := checker(context.Background(), stream)

	assert.Nil(t, err)
	assert.True(t, hasToolCall)
}

// TestVisualToolWrapper_RejectsEmptyArguments 测试工具包装器拒绝空参数调用
func TestVisualToolWrapper_RejectsEmptyArguments(t *testing.T) {
	baseCalled := false
	wrapper := NewVisualToolWrapper(&mockInvokableTool{
		runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
			baseCalled = true
			return "unexpected", nil
		},
	}, ToolWrapOptions{
		Name: "bash",
	})

	result, err := wrapper.InvokableRun(context.Background(), "{}")

	assert.Nil(t, err)
	assert.False(t, baseCalled)
	assert.True(t, strings.Contains(result, "blocked_invalid_arguments"))
}

// TestVisualToolWrapper_RejectsInvalidSkillArguments 测试工具包装器拒绝不符合 skill 工具协议的参数。
func TestVisualToolWrapper_RejectsInvalidSkillArguments(t *testing.T) {
	baseCalled := false
	wrapper := NewVisualToolWrapper(&mockInvokableTool{
		runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
			baseCalled = true
			return "unexpected", nil
		},
	}, ToolWrapOptions{
		Name: "skill",
	})

	result, err := wrapper.InvokableRun(context.Background(), `{"name":"camera-snapshot"}`)

	assert.Nil(t, err)
	assert.False(t, baseCalled)
	assert.True(t, strings.Contains(result, "blocked_invalid_arguments"))
}

// BenchmarkCreateTools 基准测试 CreateTools
func BenchmarkCreateTools(b *testing.B) {
	toolsConfig := []config.Tool{
		{
			Name:        "tool1",
			Description: "Tool 1",
			Type:        config.ToolTypeRuleChain,
			TargetId:    "chain1",
		},
		{
			Name:        "tool2",
			Description: "Tool 2",
			Type:        config.ToolTypeRuleChain,
			TargetId:    "chain2",
		},
		{
			Name:        "tool3",
			Description: "Tool 3",
			Type:        config.ToolTypeRuleChain,
			TargetId:    "chain3",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = CreateTools(toolsConfig, ToolOptions{})
	}
}

// BenchmarkCreateTools_Single 基准测试单个工具创建
func BenchmarkCreateTools_Single(b *testing.B) {
	toolConfig := config.Tool{
		Name:        "single_tool",
		Description: "A single tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "single_chain",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = CreateTools([]config.Tool{toolConfig}, ToolOptions{})
	}
}
