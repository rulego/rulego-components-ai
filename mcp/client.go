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

// Package mcp 提供 MCP（Model Context Protocol）客户端组件。
// 连接远程 MCP 服务器，调用远程 MCP 工具，将结果写入消息体传递给下一个节点。
// 同时作为 MCPToolProvider 注册到 RuleConfig.Udf，供 agent 的 "self" 模式调用远程工具。
package mcp

// 规则链节点配置示例：
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
	// defaultClientName 默认客户端名称
	defaultClientName = "RuleGo MCP Client"
	// defaultClientVersion 默认客户端版本
	defaultClientVersion = "1.0.0"
)

// ClientConfiguration MCP 客户端配置
type ClientConfiguration struct {
	// Server MCP 服务器地址，可以是 HTTP URL 或 stdio 命令
	Server string `json:"server" label:"MCP Server" desc:"MCP server address, supports HTTP URL or stdio command" required:"true"`

	// ToolName 调用的远程 MCP 工具名称。支持 ${} 表达式。
	// 如果为空，则从消息 metadata 的 mcpToolName 字段获取。
	ToolName string `json:"toolName" label:"Tool Name" desc:"Remote MCP tool name to call. Supports ${metadata.xxx} and ${msg.xxx} expressions"`

	// Args 自定义工具参数 JSON 模板。支持 ${} 表达式。
	// 例如: '{"city": "${msg.city}", "unit": "celsius"}'
	// 如果为空，默认使用消息体 JSON 作为工具参数。
	Args string `json:"args" label:"Arguments" desc:"Tool arguments JSON template. Supports ${msg.xxx} and ${metadata.xxx} expressions. Falls back to message body"`

	// ToolFilter 工具过滤器，仅影响 MCPToolProvider 注册
	ToolFilter []string `json:"toolFilter" label:"Tool Filter" desc:"Filter tools for ToolProvider registration. Empty means all. Supports * wildcard"`
}

// Desc returns the component description
func (ClientConfiguration) Desc() string {
	return "Connect to a remote MCP server, call MCP tools, and write results to the message body. Routes to Success/Failure"
}

// Client 连接远程 MCP 服务器，调用远程 MCP 工具。
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

	// 预编译 toolName 表达式模板
	if c.Config.ToolName != "" {
		tpl, err := el.NewTemplate(c.Config.ToolName)
		if err != nil {
			return fmt.Errorf("mcpClient: invalid toolName template: %w", err)
		}
		c.toolNameTpl = tpl
	}

	// 预编译 args JSON 模板
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

// OnMsg 处理消息：解析工具名和参数，调用远程 MCP 工具，将结果写入消息体。
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

// resolveToolName 解析工具名，支持 ${} 表达式
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

// resolveArgs 解析工具参数，支持 ${} 表达式 JSON 模板，默认使用消息体
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

	// 默认使用消息体 JSON 作为参数
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

// Destroy 关闭连接
func (c *Client) Destroy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cli != nil {
		_ = c.cli.Close()
		c.cli = nil
	}
	c.started = false
}

// Start 连接远程 MCP 服务器，发现工具，注册为 MCPToolProvider
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
	c.RuleConfig.Udf[types.MCPToolProviderKey] = c

	return nil
}

// ListToolDefinitions 实现 types.MCPToolProvider 接口
func (c *Client) ListToolDefinitions() ([]types.MCPToolDefinition, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	defs := make([]types.MCPToolDefinition, len(c.toolDefs))
	copy(defs, c.toolDefs)
	return defs, nil
}

// CallTool 实现 types.MCPToolProvider 接口
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	c.mu.RLock()
	handler, ok := c.toolHandlers[toolName]
	c.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("MCP tool not found: %s", toolName)
	}
	return handler(ctx, args)
}

// connect 建立 MCP 客户端连接
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

// callTool 调用远程 MCP 工具
func (c *Client) callTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	c.mu.RLock()
	cli := c.cli
	c.mu.RUnlock()
	if cli == nil {
		return "", fmt.Errorf("MCP client not connected")
	}

	result, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	})
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
