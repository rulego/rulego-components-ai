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

// MCPToolAdapter adapts the MCPToolProvider tooldefinition to eino tool.InvokableTool.
type MCPToolAdapter struct {
	def      types.MCPToolDefinition
	provider types.MCPToolProvider
}

// Info returns tool information, constructing ToolInfo from MCPToolDefinition.InputSchema.
func (a *MCPToolAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	var paramsOneOf *schema.ParamsOneOf
	if len(a.def.InputSchema) > 0 {
		var js jsonschema.Schema
		if err := json.Unmarshal(a.def.InputSchema, &js); err == nil {
			paramsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
		}
	}
	if paramsOneOf == nil {
		// When there is no schema, use an empty object
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

// InvokableRun calls the MCPToolProvider execution tool.
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

// CreateToolsFromProvider creates a list of eino tools from MCPToolProvider.
// toolNames means filter: nil or empty slice means loading all, and ["*"] also means all.
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

// MatchTool checks whether the tool name matches the filter. Filter is nil/empty/contains "*", all indicating a full match.
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
