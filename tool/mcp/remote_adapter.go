package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// remoteMCPClient 管理到远程 MCP 服务器的客户端连接。
// 同一个 server 的多个 RemoteMCPToolAdapter 共享同一个 remoteMCPClient，
// 避免重复建连。
type remoteMCPClient struct {
	server string
	client *client.Client
	mu     sync.Mutex
}

func newRemoteMCPClient(server string) *remoteMCPClient {
	return &remoteMCPClient{server: server}
}

func (r *remoteMCPClient) getClient(ctx context.Context) (*client.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.client != nil {
		return r.client, nil
	}

	var c *client.Client

	if strings.HasPrefix(r.server, "http://") || strings.HasPrefix(r.server, "https://") {
		httpTransport, err := transport.NewStreamableHTTP(r.server)
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 传输失败: %w", err)
		}
		c = client.NewClient(httpTransport)
	} else {
		args := ParseCommand(r.server)
		if len(args) == 0 {
			return nil, fmt.Errorf("无效的 MCP 命令: %s", r.server)
		}
		stdioTransport := transport.NewStdio(args[0], nil, args[1:]...)
		c = client.NewClient(stdioTransport)
	}

	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("启动 MCP 客户端失败: %w", err)
	}

	initRequest := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "RuleGo AI Agent",
				Version: "1.0.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	}

	if _, err := c.Initialize(ctx, initRequest); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("初始化 MCP 客户端失败: %w", err)
	}

	r.client = c
	return c, nil
}

// RemoteMCPToolAdapter 将远程 MCP 服务器的单个工具适配为 eino tool.InvokableTool。
// 多个 adapter 共享同一个 remoteMCPClient。
type RemoteMCPToolAdapter struct {
	name        string
	description string
	inputSchema json.RawMessage
	rc          *remoteMCPClient
}

// Info 返回工具信息。
func (a *RemoteMCPToolAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	var paramsOneOf *schema.ParamsOneOf
	if len(a.inputSchema) > 0 {
		var js jsonschema.Schema
		if err := json.Unmarshal(a.inputSchema, &js); err == nil {
			paramsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
		}
	}
	if paramsOneOf == nil {
		paramsOneOf = schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: nil,
		})
	}
	return &schema.ToolInfo{
		Name:        a.name,
		Desc:        a.description,
		ParamsOneOf: paramsOneOf,
	}, nil
}

// InvokableRun 调用远程 MCP 工具。
func (a *RemoteMCPToolAdapter) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var args map[string]interface{}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("解析参数失败: %w", err)
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	cli, err := a.rc.getClient(ctx)
	if err != nil {
		return "", err
	}

	result, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      a.name,
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
		case mcp.ImageContent:
			contents = append(contents, fmt.Sprintf("[图片: %s]", v.MIMEType))
		default:
			if b, err := json.Marshal(v); err == nil {
				contents = append(contents, string(b))
			}
		}
	}
	return strings.Join(contents, "\n"), nil
}

// CreateToolsFromRemote 连接远程 MCP 服务器，通过 tools/list 自动发现工具并创建适配器。
// toolNames 为过滤器：nil 或空切片表示加载全部，["*"] 也表示全部。
func CreateToolsFromRemote(server string, toolNames []string) ([]tool.BaseTool, error) {
	rc := newRemoteMCPClient(server)

	ctx := context.Background()
	cli, err := rc.getClient(ctx)
	if err != nil {
		return nil, err
	}

	result, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("获取远程 MCP 工具列表失败: %w", err)
	}

	var tools []tool.BaseTool
	for _, t := range result.Tools {
		if !MatchTool(t.Name, toolNames) {
			continue
		}

		inputSchema, _ := json.Marshal(t.InputSchema)

		tools = append(tools, &RemoteMCPToolAdapter{
			name:        t.Name,
			description: t.Description,
			inputSchema: inputSchema,
			rc:          rc,
		})
	}

	return tools, nil
}
