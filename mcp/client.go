/*
 * Copyright 2023 The RuleGo Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package MCP provides MCP (Model Context Protocol) client components.
// Connect to the remote MCP server, call the remote MCP tool, and write the result into the message body to pass to the next node.
// At the same time, it registers as an MCPToolProvider in RuleConfig.Udf, allowing the agent to call remote tools in "self" mode.
package mcp

// Example of rule chain node configuration:
// {
//   "id": "s1",
//   "type": "ai/mcpClient",
//   "name": "MCP客户端",
//   "configuration": {
//     "server": "http://localhost:8080/mcp",
//     "toolName": "get_weather",
//     "args": "{\"city\": \"${msg.city}\", \"unit\": \"celsius\"}"
//   }
// }
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego/utils/el"
	"github.com/rulego/rulego/utils/maps"

	mcptool "github.com/rulego/rulego-components-ai/tool/mcp"
)

// init registers the component to rulego
func init() {
	_ = rulego.Registry.Register(&Client{})
}

const (
	// nodeType is the component type identifier
	nodeType = "ai/mcpClient"
	// defaultClientName The default client name
	defaultClientName = "RuleGo MCP Client"
	// defaultClientVersion The default client version
	defaultClientVersion = "1.0.0"
)

// ClientConfiguration MCP client configuration
type ClientConfiguration struct {
	// Server MCP server address, which can be an HTTP URL or a stdio command
	Server string `json:"server" label:"MCP Server" desc:"MCP server address, supports HTTP URL or stdio command" required:"true"`

	// ToolName is the name of the remote MCP tool called by ToolName. Supports ${} expressions.
	// If empty, it is retrieved from the mcpToolName field of the message metadata.
	ToolName string `json:"toolName" label:"Tool Name" desc:"Remote MCP tool name to call. Supports ${metadata.xxx} and ${msg.xxx} expressions"`

	// Args custom tool parameter JSON template. Supports ${} expressions.
	// For example: '{"city": "${msg.city}", "unit": "celsius"}'
	// If it is empty, the message body JSON is used as the tool parameter by default.
	Args string `json:"args" label:"Arguments" desc:"Tool arguments JSON template. Supports ${msg.xxx} and ${metadata.xxx} expressions. Falls back to message body"`

	// ToolFilter tool filter, only affects MCPToolProvider registration
	ToolFilter []string `json:"toolFilter" label:"Tool Filter" desc:"Filter tools for ToolProvider registration. Empty means all. Supports * wildcard"`
}

// Desc returns the component description
func (ClientConfiguration) Desc() string {
	return "Connect to a remote MCP server, call MCP tools, and write results to the message body. Routes to Success/Failure"
}

// Client connects to the remote MCP server and calls the remote MCP tool.
type Client struct {
	Config     ClientConfiguration
	RuleConfig types.Config

	mu           sync.RWMutex
	cli          *client.Client
	toolDefs     []types.MCPToolDefinition
	toolHandlers map[string]func(ctx context.Context, args map[string]interface{}) (string, error)
	started      bool

	toolNameTpl   el.Template
	argsTpl       el.Template
	argsTplHasVar bool
}

// New creates a new instance of Client
func (c *Client) New() types.Node {
	return &Client{}
}

// Type returns the type of the component
func (c *Client) Type() string { return nodeType }

// Init initializes the component
func (c *Client) Init(ruleConfig types.Config, configuration types.Configuration) error {
	if err := maps.Map2Struct(configuration, &c.Config); err != nil {
		return err
	}
	c.RuleConfig = ruleConfig
	if c.Config.Server == "" {
		return fmt.Errorf("mcpClient server config is empty")
	}

	// Precompile toolName expression templates
	if c.Config.ToolName != "" {
		tpl, err := el.NewTemplate(c.Config.ToolName)
		if err != nil {
			return fmt.Errorf("mcpClient: invalid toolName template: %w", err)
		}
		c.toolNameTpl = tpl
	}

	// Pre-compile args JSON templates
	if c.Config.Args != "" {
		tpl, err := el.NewTemplate(c.Config.Args)
		if err != nil {
			return fmt.Errorf("mcpClient: invalid args template: %w", err)
		}
		c.argsTpl = tpl
		c.argsTplHasVar = tpl.HasVar()
	}

	return nil
}

// OnMsg processes messages: parses tool names and parameters, calls remote MCP tools, and writes results into the message body.
func (c *Client) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	toolName := c.resolveToolName(ctx, msg)
	if toolName == "" {
		ctx.TellFailure(msg, fmt.Errorf("mcpClient: toolName is empty, set config.toolName or metadata.mcpToolName"))
		return
	}

	args := c.resolveArgs(ctx, msg)

	result, err := c.CallTool(ctx.GetContext(), toolName, args)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}

	msg.SetData(result)
	ctx.TellSuccess(msg)
}

// resolveToolName parses the tool name, supports ${} expressions
func (c *Client) resolveToolName(ctx types.RuleContext, msg types.RuleMsg) string {
	if c.toolNameTpl != nil {
		if c.toolNameTpl.HasVar() {
			env := base.NodeUtils.GetEvnAndMetadata(ctx, msg)
			return c.toolNameTpl.ExecuteAsString(env)
		}
		return c.toolNameTpl.ExecuteAsString(nil)
	}
	return msg.Metadata.GetValue("mcpToolName")
}

// resolveArgs parsing tool parameters, supports ${} expression JSON templates, default uses message bodies
func (c *Client) resolveArgs(ctx types.RuleContext, msg types.RuleMsg) map[string]interface{} {
	if c.argsTpl != nil {
		var env map[string]interface{}
		if c.argsTplHasVar {
			env = base.NodeUtils.GetEvnAndMetadata(ctx, msg)
		}
		argsStr := c.argsTpl.ExecuteAsString(env)
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
			return map[string]interface{}{"message": argsStr}
		}
		return args
	}

	// By default, the message body JSON is used as a parameter
	var args map[string]interface{}
	data := msg.GetData()
	if data != "" {
		if err := json.Unmarshal([]byte(data), &args); err != nil {
			args = map[string]interface{}{"message": data}
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}
	return args
}

// Destroy to close the connection
func (c *Client) Destroy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cli != nil {
		_ = c.cli.Close()
		c.cli = nil
	}
	c.started = false
}

// Start to connect to the remote MCP server, discover the tool, and register as MCPToolProvider
func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return nil
	}

	ctx := context.Background()
	cli, err := c.connect(ctx)
	if err != nil {
		return fmt.Errorf("MCP client connect failed: %w", err)
	}

	result, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		_ = cli.Close()
		return fmt.Errorf("MCP tool discovery failed: %w", err)
	}

	c.cli = cli
	c.toolDefs = make([]types.MCPToolDefinition, 0, len(result.Tools))
	c.toolHandlers = make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	for _, t := range result.Tools {
		if !mcptool.MatchTool(t.Name, c.Config.ToolFilter) {
			continue
		}
		schema, _ := json.Marshal(t.InputSchema)
		toolName := t.Name
		c.toolDefs = append(c.toolDefs, types.MCPToolDefinition{
			Name:        toolName,
			Description: t.Description,
			InputSchema: schema,
		})
		c.toolHandlers[toolName] = func(ctx context.Context, args map[string]interface{}) (string, error) {
			return c.callTool(ctx, toolName, args)
		}
	}
	c.started = true

	if c.RuleConfig.Udf == nil {
		c.RuleConfig.Udf = make(map[string]interface{})
	}
	// Register using MCPToolProviderKey as the default key (maintain backward compatibility)
	// Note: If there are multiple MCP Client instances under the same RuleConfig, the one registered later will override the one registered first.
	// If a unique identifier is needed, MCPToolProviderKey + ":" + server address can be used.
	c.RuleConfig.Udf[types.MCPToolProviderKey] = c

	return nil
}

// ListToolDefinitions implementation types.MCPToolProvider interface
func (c *Client) ListToolDefinitions() ([]types.MCPToolDefinition, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	defs := make([]types.MCPToolDefinition, len(c.toolDefs))
	copy(defs, c.toolDefs)
	return defs, nil
}

// CallTool implements types.MCPToolProvider interface
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	c.mu.RLock()
	handler, ok := c.toolHandlers[toolName]
	c.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("MCP tool not found: %s", toolName)
	}
	return handler(ctx, args)
}

// connect: establish an MCP client connection
func (c *Client) connect(ctx context.Context) (*client.Client, error) {
	var cli *client.Client

	if strings.HasPrefix(c.Config.Server, "http://") || strings.HasPrefix(c.Config.Server, "https://") {
		httpTransport, err := transport.NewStreamableHTTP(c.Config.Server)
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 传输失败: %w", err)
		}
		cli = client.NewClient(httpTransport)
	} else {
		args := mcptool.ParseCommand(c.Config.Server)
		if len(args) == 0 {
			return nil, fmt.Errorf("无效的 MCP 命令: %s", c.Config.Server)
		}
		stdioTransport := transport.NewStdio(args[0], nil, args[1:]...)
		cli = client.NewClient(stdioTransport)
	}

	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("启动 MCP 客户端失败: %w", err)
	}

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    defaultClientName,
				Version: defaultClientVersion,
			},
		},
	}
	if _, err := cli.Initialize(ctx, initReq); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("初始化 MCP 客户端失败: %w", err)
	}

	return cli, nil
}

// callTool calls remote MCP tools
func (c *Client) callTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	c.mu.RLock()
	cli := c.cli
	if cli == nil {
		c.mu.RUnlock()
		return "", fmt.Errorf("MCP client not connected")
	}

	result, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	})
	c.mu.RUnlock()
	if err != nil {
		return "", fmt.Errorf("调用远程 MCP 工具失败: %w", err)
	}

	if result.IsError {
		if len(result.Content) > 0 {
			if textContent, ok := result.Content[0].(mcp.TextContent); ok {
				return "", fmt.Errorf("MCP 工具错误: %s", textContent.Text)
			}
		}
		return "", fmt.Errorf("MCP 工具错误: 未知错误")
	}

	var contents []string
	for _, content := range result.Content {
		switch v := content.(type) {
		case mcp.TextContent:
			contents = append(contents, v.Text)
		default:
			if b, err := json.Marshal(v); err == nil {
				contents = append(contents, string(b))
			}
		}
	}
	return strings.Join(contents, "\n"), nil
}
