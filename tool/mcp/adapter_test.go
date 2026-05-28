package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/rulego/rulego/api/types"
)

// mockProvider 实现 types.MCPToolProvider 接口
type mockProvider struct {
	tools       []types.MCPToolDefinition
	calls       []callRecord
	callHandler func(name string, args map[string]interface{}) (string, error)
}

type callRecord struct {
	name string
	args map[string]interface{}
}

func (p *mockProvider) ListToolDefinitions() ([]types.MCPToolDefinition, error) {
	return p.tools, nil
}

func (p *mockProvider) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	p.calls = append(p.calls, callRecord{name, args})
	if p.callHandler != nil {
		return p.callHandler(name, args)
	}
	return "", nil
}

func TestCreateToolsFromProvider_All(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save rule chain", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`)},
			{Name: "execute_rule_chain", Description: "Execute rule chain", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`)},
			{Name: "delete_rule_chain", Description: "Delete rule chain", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`)},
		},
	}

	// nil 表示加载全部
	tools, err := CreateToolsFromProvider(provider, nil)
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
}

func TestCreateToolsFromProvider_Filter(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save", InputSchema: []byte(`{}`)},
			{Name: "execute_rule_chain", Description: "Exec", InputSchema: []byte(`{}`)},
			{Name: "delete_rule_chain", Description: "Del", InputSchema: []byte(`{}`)},
		},
	}

	// 只加载 2 个
	tools, err := CreateToolsFromProvider(provider, []string{"save_rule_chain", "execute_rule_chain"})
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
}

func TestCreateToolsFromProvider_Wildcard(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save", InputSchema: []byte(`{}`)},
			{Name: "execute_rule_chain", Description: "Exec", InputSchema: []byte(`{}`)},
		},
	}

	// ["*"] 表示全部
	tools, err := CreateToolsFromProvider(provider, []string{"*"})
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
}

func TestCreateToolsFromProvider_Empty(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save", InputSchema: []byte(`{}`)},
		},
	}

	// 空过滤器也表示全部
	tools, err := CreateToolsFromProvider(provider, []string{})
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
}

func TestCreateToolsFromProvider_NoMatch(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save", InputSchema: []byte(`{}`)},
		},
	}

	tools, err := CreateToolsFromProvider(provider, []string{"non_existent"})
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestMCPToolAdapter_Info(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{
				Name:        "save_rule_chain",
				Description: "Save a rule chain",
				InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string","description":"Rule chain id"},"body":{"type":"object","description":"Rule chain definition"}},"required":["id","body"]}`),
			},
		},
	}

	tools, err := CreateToolsFromProvider(provider, nil)
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}

	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}

	if info.Name != "save_rule_chain" {
		t.Errorf("expected name 'save_rule_chain', got '%s'", info.Name)
	}
	if info.Desc != "Save a rule chain" {
		t.Errorf("expected desc 'Save a rule chain', got '%s'", info.Desc)
	}
}

func TestMCPToolAdapter_Info_EmptySchema(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "test_tool", Description: "Test", InputSchema: nil},
		},
	}

	tools, err := CreateToolsFromProvider(provider, nil)
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}

	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}

	if info.Name != "test_tool" {
		t.Errorf("expected name 'test_tool', got '%s'", info.Name)
	}
}

func TestMCPToolAdapter_InvokableRun(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save", InputSchema: []byte(`{}`)},
		},
		callHandler: func(name string, args map[string]interface{}) (string, error) {
			return "save ok", nil
		},
	}

	tools, err := CreateToolsFromProvider(provider, nil)
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}

	invokable := tools[0].(tool.InvokableTool)
	result, err := invokable.InvokableRun(context.Background(), `{"id":"test","body":{}}`)
	if err != nil {
		t.Fatalf("InvokableRun failed: %v", err)
	}

	if result != "save ok" {
		t.Errorf("expected 'save ok', got '%s'", result)
	}

	if len(provider.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(provider.calls))
	}
	if provider.calls[0].name != "save_rule_chain" {
		t.Errorf("expected call to 'save_rule_chain', got '%s'", provider.calls[0].name)
	}
	if provider.calls[0].args["id"] != "test" {
		t.Errorf("expected id='test', got '%v'", provider.calls[0].args["id"])
	}
}

func TestMCPToolAdapter_InvokableRun_EmptyArgs(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "list_rule_chains", Description: "List", InputSchema: []byte(`{}`)},
		},
		callHandler: func(name string, args map[string]interface{}) (string, error) {
			return "list result", nil
		},
	}

	tools, err := CreateToolsFromProvider(provider, nil)
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}

	// 空参数
	invokable := tools[0].(tool.InvokableTool)
	result, err := invokable.InvokableRun(context.Background(), "")
	if err != nil {
		t.Fatalf("InvokableRun failed: %v", err)
	}
	if result != "list result" {
		t.Errorf("expected 'list result', got '%s'", result)
	}
}

func TestMCPToolAdapter_InvokableRun_Error(t *testing.T) {
	provider := &mockProvider{
		tools: []types.MCPToolDefinition{
			{Name: "save_rule_chain", Description: "Save", InputSchema: []byte(`{}`)},
		},
		callHandler: func(name string, args map[string]interface{}) (string, error) {
			return "", fmt.Errorf("save failed")
		},
	}

	tools, err := CreateToolsFromProvider(provider, nil)
	if err != nil {
		t.Fatalf("CreateToolsFromProvider failed: %v", err)
	}

	invokable := tools[0].(tool.InvokableTool)
	_, err = invokable.InvokableRun(context.Background(), `{"id":"test"}`)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "save failed" {
		t.Errorf("expected 'save failed', got '%s'", err.Error())
	}
}

func TestMatchTool(t *testing.T) {
	tests := []struct {
		name   string
		filter []string
		want   bool
	}{
		{"save_rule_chain", nil, true},
		{"save_rule_chain", []string{}, true},
		{"save_rule_chain", []string{"*"}, true},
		{"save_rule_chain", []string{"save_rule_chain"}, true},
		{"save_rule_chain", []string{"save_rule_chain", "execute_rule_chain"}, true},
		{"save_rule_chain", []string{"execute_rule_chain"}, false},
		{"save_rule_chain", []string{"save"}, false},
	}

	for _, tt := range tests {
		got := MatchTool(tt.name, tt.filter)
		if got != tt.want {
			t.Errorf("MatchTool(%q, %v) = %v, want %v", tt.name, tt.filter, got, tt.want)
		}
	}
}
