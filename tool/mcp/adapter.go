package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/rulego/rulego/api/types"
)

// MCPToolAdapter 将 MCPToolProvider 的工具定义适配为 eino tool.InvokableTool。
type MCPToolAdapter struct {
	def      types.MCPToolDefinition
	provider types.MCPToolProvider
}

// Info 返回工具信息，从 MCPToolDefinition.InputSchema 构造 ToolInfo。
func (a *MCPToolAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	var paramsOneOf *schema.ParamsOneOf
	if len(a.def.InputSchema) > 0 {
		var js jsonschema.Schema
		if err := json.Unmarshal(a.def.InputSchema, &js); err == nil {
			paramsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
		}
	}
	if paramsOneOf == nil {
		// 无 schema 时使用空 object
		paramsOneOf = schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: nil,
		})
	}
	return &schema.ToolInfo{
		Name:        a.def.Name,
		Desc:        a.def.Description,
		ParamsOneOf: paramsOneOf,
	}, nil
}

// InvokableRun 调用 MCPToolProvider 执行工具。
func (a *MCPToolAdapter) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var args map[string]interface{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("解析参数失败: %w", err)
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}
	return a.provider.CallTool(ctx, a.def.Name, args)
}

// CreateToolsFromProvider 从 MCPToolProvider 创建 eino 工具列表。
// toolNames 为过滤器：nil 或空切片表示加载全部，["*"] 也表示全部。
func CreateToolsFromProvider(provider types.MCPToolProvider, toolNames []string) ([]tool.BaseTool, error) {
	defs, err := provider.ListToolDefinitions()
	if err != nil {
		return nil, fmt.Errorf("列出 MCP 工具失败: %w", err)
	}
	var tools []tool.BaseTool
	for _, d := range defs {
		if !MatchTool(d.Name, toolNames) {
			continue
		}
		tools = append(tools, &MCPToolAdapter{def: d, provider: provider})
	}
	return tools, nil
}

// MatchTool 检查工具名是否匹配过滤器。filter 为 nil/空/包含 "*" 均表示全部匹配。
func MatchTool(name string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if f == "*" || f == name {
			return true
		}
	}
	return false
}
