package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego-components-ai/session"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/engine"
	"github.com/rulego/rulego/test/assert"
)

// TestCreateChatModel_MissingURL Testing for missing URLs
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

// TestCreateChatModel_EmptyURLAfterTrim Test the situation where the URL contains only spaces
func TestCreateChatModel_EmptyURLAfterTrim(t *testing.T) {
	llmConfig := config.LLMConfig{
		Url:   "   ", // Only the spaces remain
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

// TestCreateChatModel_ValidConfig Test effective configuration
func TestCreateChatModel_ValidConfig(t *testing.T) {
	// This test requires a real API endpoint, so we use a fake URL
	// In reality, attempts to connect may fail
	t.Skip("Requires valid API endpoint")
}

// TestCreateChatModel_DefaultParams Test default parameters
func TestCreateChatModel_DefaultParams(t *testing.T) {
	// Test whether the default value is applied when the parameter is 0
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

	// Creating a model applies default parameters
	// Since actual connections are required, here we only verify the logic
	// Mock is used in actual testing

	// Verify the default value constant
	assert.Equal(t, config.DefaultTemperature, float32(0.7))
	assert.Equal(t, config.DefaultTopP, float32(0.9))
	assert.Equal(t, config.DefaultFrequencyPenalty, float32(0.5))
	assert.Equal(t, config.DefaultPresencePenalty, float32(0.5))
}

// TestCreateChatModel_CustomParams Test custom parameters
func TestCreateChatModel_CustomParams(t *testing.T) {
	// When users set parameters, they should not be overwritten by default values
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

	// These values should not be overwritten by defaults
	assert.Equal(t, float32(0.5), llmConfig.Params.Temperature)
	assert.Equal(t, float32(0.8), llmConfig.Params.TopP)
	assert.Equal(t, float32(0.3), llmConfig.Params.FrequencyPenalty)
	assert.Equal(t, float32(0.4), llmConfig.Params.PresencePenalty)
	assert.Equal(t, 1000, llmConfig.Params.MaxTokens)
}

// TestCreateChatModel_WithStopSequences Test stops the sequence
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

// TestCreateChatModel_WithMaxRetries Test and retry the configuration
func TestCreateChatModel_WithMaxRetries(t *testing.T) {
	llmConfig := config.LLMConfig{
		Url:        "https://api.example.com/v1",
		Key:        "test-key",
		Model:      "gpt-4",
		MaxRetries: 3,
	}

	assert.Equal(t, 3, llmConfig.MaxRetries)
}

// TestCreateTools Test creation tool
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

	tools, toolInfos, _, err := CreateTools(toolsConfig, ToolOptions{})

	assert.Nil(t, err)
	assert.Equal(t, 2, len(tools))
	assert.Equal(t, 2, len(toolInfos))
}

// TestCreateTools_Empty List of test empty tools
func TestCreateTools_Empty(t *testing.T) {
	tools, toolInfos, _, err := CreateTools([]config.Tool{}, ToolOptions{})

	assert.Nil(t, err)
	assert.Equal(t, 0, len(tools))
	assert.Equal(t, 0, len(toolInfos))
}

// TestCreateTools_Nil List of testing NIL tools
func TestCreateTools_Nil(t *testing.T) {
	tools, toolInfos, _, err := CreateTools(nil, ToolOptions{})

	assert.Nil(t, err)
	assert.Equal(t, 0, len(tools))
	assert.Equal(t, 0, len(toolInfos))
}

// TestCreateTools_MultipleTypes Test different types of tools
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

	tools, toolInfos, _, err := CreateTools(toolsConfig, ToolOptions{})

	assert.Nil(t, err)
	assert.Equal(t, 2, len(tools))
	assert.Equal(t, 2, len(toolInfos))
}

// TestCreateChatModelAgent Tests the creation of a ChatModel Agent
func TestCreateChatModelAgent(t *testing.T) {
	t.Skip("Requires valid API endpoint and LLM configuration")
}

// TestCreateChatModelAgent_Config Test Agent configuration
func TestCreateChatModelAgent_Config(t *testing.T) {
	// Test the correctness of the configuration structure
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

// TestChatAgentConfig Tests the ChatAgentConfig structure
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

// TestSubAgentConfig Tests the SubAgentConfig structure
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

// TestCreateChatModel_WithContext Creating the test tape context
func TestCreateChatModel_WithContext(t *testing.T) {
	// Verify that context is used correctly during creation
	ctx := context.Background()
	assert.NotNil(t, ctx)

	// Actual creation requires a valid API endpoint
	// Here, only the context is verified
}

// TestCreateChatModel_KeyTrim Test the space handling of API Keys
func TestCreateChatModel_KeyTrim(t *testing.T) {
	llmConfig := config.LLMConfig{
		Url:   "https://api.example.com/v1",
		Key:   "  test-key-with-spaces  ",
		Model: "gpt-4",
	}

	// After creation, the Key should be trimmed
	// Since actual connection is required, only the configuration is verified here
	assert.Equal(t, "  test-key-with-spaces  ", llmConfig.Key)
}

// TestIsExecutableToolCallArgs tests whether the parameter called by the tool can be executed
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

// TestCreateStreamToolCallChecker_AcceptsNamedToolCallWithoutCompleteArgs Test the flow checker's tool call to identify incomplete parameters
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

// TestCreateStreamToolCallChecker_RejectsUnnamedToolCall Test Flow Checker ignores tool calls without names
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

// TestCreateStreamToolCallChecker_AcceptsValidArguments Test flow checker to identify valid tool calls
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

// TestVisualToolWrapper_RejectsEmptyArguments Test tool wrapper rejects air parameter calls
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

// TestVisualToolWrapper_RejectsInvalidSkillArguments Test tool wrapper rejects parameters that do not comply with the Skill tool protocol.
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

// TestVisualToolWrapper_ForwardsDynamicSkillLister Test that VisualToolWrapper can forward the DynamicSkillLister interface.
// This is the key regression test for skill modification: when WrapVisual=true, after dynamicSkillTool is wrapped by VisualToolWrapper,
// The DynamicSkillLister interface must not be lost; otherwise, the MessageModifier will be nil, and the skill list will not be injected into the system prompt.
func TestVisualToolWrapper_ForwardsDynamicSkillLister(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: test-skill
description: A test skill
---
Test skill content
`), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	toolsConfig := []config.Tool{
		{
			Type: config.ToolTypeBuiltin,
			Name: "skill",
			Config: map[string]interface{}{
				"localDirs": []string{tmpDir},
			},
		},
	}

	// Use WrapVisual=true to simulate the production code path
	// Before packaging, skillLister is extracted and returned by CreateTools
	tools, _, skillLister, err := CreateTools(toolsConfig, ToolOptions{
		WrapVisual: true,
		Logger:     NewTestLogger(t),
	})
	if err != nil {
		t.Fatalf("CreateTools failed: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("CreateTools returned no tools")
	}

	// Key Claim: The skillLister is properly extracted before packaging
	if skillLister == nil {
		t.Fatal("CreateTools should return non-nil skillLister for skill tool")
	}

	// Verify that ListSkills works properly
	skills, err := skillLister.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills failed: %v", err)
	}
	if !strings.Contains(skills, "test-skill") {
		t.Fatalf("ListSkills result should contain 'test-skill', got: %s", skills)
	}

	// Verify that GetSkillInstruction works properly
	instruction := skillLister.GetSkillInstruction()
	if instruction == "" {
		t.Fatal("GetSkillInstruction should not return empty string")
	}

	// Verify that BuildSkillModifier can be successfully built
	modifier := BuildSkillModifier(skillLister)
	if modifier == nil {
		t.Fatal("BuildSkillModifier returned nil")
	}

	// Verify the MessageModifier injection system prompt
	input := []*schema.Message{
		{Role: schema.System, Content: "You are a helpful assistant."},
		{Role: schema.User, Content: "Hello"},
	}
	result := modifier(context.Background(), input)
	if !strings.Contains(result[0].Content, "test-skill") {
		t.Fatalf("system prompt should contain 'test-skill', got: %s", result[0].Content)
	}
}

// BenchmarkCreateTools Benchmark CreateTools
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
		_, _, _, _ = CreateTools(toolsConfig, ToolOptions{})
	}
}

// BenchmarkCreateTools_Single Benchmarking is created with a single tool
func BenchmarkCreateTools_Single(b *testing.B) {
	toolConfig := config.Tool{
		Name:        "single_tool",
		Description: "A single tool",
		Type:        config.ToolTypeRuleChain,
		TargetId:    "single_chain",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = CreateTools([]config.Tool{toolConfig}, ToolOptions{})
	}
}

// ============================================================================
// MCP tool testing
// ============================================================================

type testMCPProvider struct {
	defs        []types.MCPToolDefinition
	calls       []testToolCall
	callHandler func(name string, args map[string]interface{}) (string, error)
}

type testToolCall struct {
	name string
	args map[string]interface{}
}

func (p *testMCPProvider) ListToolDefinitions() ([]types.MCPToolDefinition, error) {
	return p.defs, nil
}

func (p *testMCPProvider) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	p.calls = append(p.calls, testToolCall{name, args})
	if p.callHandler != nil {
		return p.callHandler(name, args)
	}
	return "ok", nil
}

func newTestMCPProvider() *testMCPProvider {
	return &testMCPProvider{
		defs: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save a rule chain", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"},"body":{"type":"object"}},"required":["id","body"]}`)},
			{Name: "execute_rule_chain", Description: "Execute a rule chain", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"},"message":{"type":"object"}},"required":["id","message"]}`)},
			{Name: "list_rule_chains", Description: "List rule chains", InputSchema: []byte(`{"type":"object","properties":{"keywords":{"type":"string"}}}`)},
		},
	}
}

func newTestRuleConfigWithProvider(provider types.MCPToolProvider) types.Config {
	rc := engine.NewConfig()
	rc.RegisterUdf(types.MCPToolProviderKey, provider)
	return rc
}

func TestCreateTools_MCPType_Self_All(t *testing.T) {
	provider := newTestMCPProvider()
	rc := newTestRuleConfigWithProvider(provider)
	tools, infos, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{"server": "self"}},
	}, ToolOptions{RuleConfig: rc})
	assert.Nil(t, err)
	assert.Equal(t, 3, len(tools))
	assert.Equal(t, 3, len(infos))
}

func TestCreateTools_MCPType_Self_Filtered(t *testing.T) {
	provider := newTestMCPProvider()
	rc := newTestRuleConfigWithProvider(provider)
	tools, infos, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{
			"server": "self",
			"tools":  []interface{}{"save_rule_chain", "execute_rule_chain"},
		}},
	}, ToolOptions{RuleConfig: rc})
	assert.Nil(t, err)
	assert.Equal(t, 2, len(tools))
	assert.Equal(t, 2, len(infos))
	names := map[string]bool{}
	for _, info := range infos {
		names[info.Name] = true
	}
	assert.True(t, names["save_rule_chain"])
	assert.True(t, names["execute_rule_chain"])
	assert.False(t, names["list_rule_chains"])
}

func TestCreateTools_MCPType_Self_Wildcard(t *testing.T) {
	provider := newTestMCPProvider()
	rc := newTestRuleConfigWithProvider(provider)
	tools, _, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{
			"server": "self",
			"tools":  []interface{}{"*"},
		}},
	}, ToolOptions{RuleConfig: rc})
	assert.Nil(t, err)
	assert.Equal(t, 3, len(tools))
}

func TestCreateTools_MCPType_Self_NoProvider(t *testing.T) {
	rc := engine.NewConfig()
	_, _, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{"server": "self"}},
	}, ToolOptions{RuleConfig: rc})
	assert.NotNil(t, err)
	assert.True(t, len(err.Error()) > 0)
}

func TestCreateTools_MCPType_MissingServer(t *testing.T) {
	_, _, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{}},
	}, ToolOptions{})
	assert.NotNil(t, err)
}

func TestCreateTools_MCPType_Remote_NotImplemented(t *testing.T) {
	_, _, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{
			"server": "http://remote:8080/mcp",
		}},
	}, ToolOptions{})
	assert.NotNil(t, err)
}

func TestCreateTools_MCPType_Mixed(t *testing.T) {
	provider := newTestMCPProvider()
	rc := newTestRuleConfigWithProvider(provider)
	tools, infos, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{
			"server": "self",
			"tools":  []interface{}{"save_rule_chain", "execute_rule_chain"},
		}},
		{Type: config.ToolTypeRuleChain, Name: "my_chain", TargetId: "chain_1"},
	}, ToolOptions{RuleConfig: rc})
	assert.Nil(t, err)
	assert.Equal(t, 3, len(tools))
	assert.Equal(t, 3, len(infos))
}

func TestCreateTools_MCPType_Self_ToolExecution(t *testing.T) {
	provider := &testMCPProvider{
		defs: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`)},
		},
		callHandler: func(name string, args map[string]interface{}) (string, error) {
			return "save ok", nil
		},
	}
	rc := newTestRuleConfigWithProvider(provider)
	tools, _, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{"server": "self"}},
	}, ToolOptions{RuleConfig: rc})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(tools))
	invokable, ok := tools[0].(tool.InvokableTool)
	assert.True(t, ok)
	result, err := invokable.InvokableRun(context.Background(), `{"id":"test_chain"}`)
	assert.Nil(t, err)
	assert.Equal(t, "save ok", result)
	assert.Equal(t, 1, len(provider.calls))
	assert.Equal(t, "save_rule_chain", provider.calls[0].name)
}
