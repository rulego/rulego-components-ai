package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/engine"
	"github.com/rulego/rulego/test/assert"
)

// testMCPProvider 测试用 MCP 工具提供者
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

// newTestMCPProvider 创建带默认工具定义的测试 provider
func newTestMCPProvider() *testMCPProvider {
	return &testMCPProvider{
		defs: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save a rule chain", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"},"body":{"type":"object"}},"required":["id","body"]}`)},
			{Name: "execute_rule_chain", Description: "Execute a rule chain", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"},"message":{"type":"object"}},"required":["id","message"]}`)},
			{Name: "list_rule_chains", Description: "List rule chains", InputSchema: []byte(`{"type":"object","properties":{"keywords":{"type":"string"}}}`)},
		},
	}
}

// newTestRuleConfigWithProvider 创建注册了 MCPToolProvider 的 RuleConfig
func newTestRuleConfigWithProvider(provider types.MCPToolProvider) types.Config {
	rc := engine.NewConfig()
	rc.RegisterUdf(types.MCPToolProviderKey, provider)
	return rc
}

// TestCreateTools_MCPType_Self_All 测试 self 模式加载全部工具
func TestCreateTools_MCPType_Self_All(t *testing.T) {
	provider := newTestMCPProvider()
	rc := newTestRuleConfigWithProvider(provider)

	tools, infos, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{"server": "self"}},
	}, ToolOptions{RuleConfig: rc})

	assert.Nil(t, err)
	assert.Equal(t, 3, len(tools))
	assert.Equal(t, 3, len(infos))
}

// TestCreateTools_MCPType_Self_Filtered 测试 self 模式按名称过滤工具
func TestCreateTools_MCPType_Self_Filtered(t *testing.T) {
	provider := newTestMCPProvider()
	rc := newTestRuleConfigWithProvider(provider)

	tools, infos, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{
			"server": "self",
			"tools":  []interface{}{"save_rule_chain", "execute_rule_chain"},
		}},
	}, ToolOptions{RuleConfig: rc})

	assert.Nil(t, err)
	assert.Equal(t, 2, len(tools))
	assert.Equal(t, 2, len(infos))

	// 验证返回的工具名称
	names := map[string]bool{}
	for _, info := range infos {
		names[info.Name] = true
	}
	assert.True(t, names["save_rule_chain"])
	assert.True(t, names["execute_rule_chain"])
	assert.False(t, names["list_rule_chains"])
}

// TestCreateTools_MCPType_Self_Wildcard 测试通配符加载全部工具
func TestCreateTools_MCPType_Self_Wildcard(t *testing.T) {
	provider := newTestMCPProvider()
	rc := newTestRuleConfigWithProvider(provider)

	tools, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{
			"server": "self",
			"tools":  []interface{}{"*"},
		}},
	}, ToolOptions{RuleConfig: rc})

	assert.Nil(t, err)
	assert.Equal(t, 3, len(tools))
}

// TestCreateTools_MCPType_Self_NoProvider 测试未注册 provider 时报错
func TestCreateTools_MCPType_Self_NoProvider(t *testing.T) {
	rc := engine.NewConfig()

	_, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{"server": "self"}},
	}, ToolOptions{RuleConfig: rc})

	assert.NotNil(t, err)
	assert.True(t, len(err.Error()) > 0)
}

// TestCreateTools_MCPType_MissingServer 测试缺少 server 字段时报错
func TestCreateTools_MCPType_MissingServer(t *testing.T) {
	_, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{}},
	}, ToolOptions{})

	assert.NotNil(t, err)
}

// TestCreateTools_MCPType_Remote_NotImplemented 测试远程模式未实现
func TestCreateTools_MCPType_Remote_NotImplemented(t *testing.T) {
	_, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{
			"server": "http://remote:8080/mcp",
		}},
	}, ToolOptions{})

	assert.NotNil(t, err)
}

// TestCreateTools_MCPType_Mixed 测试 MCP 和 rulechain 混合类型
func TestCreateTools_MCPType_Mixed(t *testing.T) {
	provider := newTestMCPProvider()
	rc := newTestRuleConfigWithProvider(provider)

	tools, infos, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{
			"server": "self",
			"tools":  []interface{}{"save_rule_chain", "execute_rule_chain"},
		}},
		{Type: config.ToolTypeRuleChain, Name: "my_chain", TargetId: "chain_1"},
	}, ToolOptions{RuleConfig: rc})

	assert.Nil(t, err)
	assert.Equal(t, 3, len(tools)) // 2 MCP + 1 rulechain
	assert.Equal(t, 3, len(infos))
}

// TestCreateTools_MCPType_Self_ToolExecution 测试通过 adapter 实际调用工具
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

	tools, _, err := CreateTools([]config.Tool{
		{Type: config.ToolTypeMCP, Config: map[string]interface{}{"server": "self"}},
	}, ToolOptions{RuleConfig: rc})

	assert.Nil(t, err)
	assert.Equal(t, 1, len(tools))

	// 调用工具
	invokable, ok := tools[0].(tool.InvokableTool)
	assert.True(t, ok)
	result, err := invokable.InvokableRun(context.Background(), `{"id":"test_chain"}`)

	assert.Nil(t, err)
	assert.Equal(t, "save ok", result)
	// 验证 provider 收到了调用
	assert.Equal(t, 1, len(provider.calls))
	assert.Equal(t, "save_rule_chain", provider.calls[0].name)
}
