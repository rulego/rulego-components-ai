package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/rulego/rulego-components-ai/utils/imgsink"
)

// remoteCallTimeout 单次工具调用超时;调用方自带 deadline 时不叠加。
const remoteCallTimeout = 300 * time.Second

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

// reset 丢弃连接，下次调用重建。
func (r *remoteMCPClient) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		_ = r.client.Close()
		r.client = nil
	}
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
// 传输级失败重建连接重试一次。
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

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, remoteCallTimeout)
		defer cancel()
	}

	var result *mcp.CallToolResult
	for attempt := 0; ; attempt++ {
		cli, err := a.rc.getClient(ctx)
		if err != nil {
			return "", err
		}
		result, err = cli.CallTool(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      a.name,
				Arguments: args,
			},
		})
		if err == nil {
			break
		}
		a.rc.reset()
		if attempt >= 1 || ctx.Err() != nil {
			return "", fmt.Errorf("调用远程 MCP 工具失败: %w", err)
		}
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
			contents = append(contents, describeImageContent(ctx, v.MIMEType, v.Data))
		default:
			if b, err := json.Marshal(v); err == nil {
				contents = append(contents, string(b))
			}
		}
	}
	// structuredContent 带确认令牌等跨轮数据，附加到文本尾部供模型读取
	if result.StructuredContent != nil {
		if b, err := json.Marshal(result.StructuredContent); err == nil {
			s := string(b)
			const maxStructured = 8 << 10
			if len(s) > maxStructured {
				s = s[:maxStructured] + "…(truncated)"
			}
			contents = append(contents, "structured:", s)
		}
	}
	return strings.Join(contents, "\n"), nil
}

// CreateToolsFromRemote 连接远程 MCP 服务器，通过 tools/list 自动发现工具并创建适配器。
// toolNames 为过滤器：nil 或空切片表示加载全部，["*"] 也表示全部。
func CreateToolsFromRemote(server string, toolNames []string) ([]tool.BaseTool, error) {
	rc := newRemoteMCPClient(server)

	ctx, cancel := context.WithTimeout(context.Background(), remoteCallTimeout)
	defer cancel()

	var result *mcp.ListToolsResult
	// 传输级失败重建连接重试一次。
	for attempt := 0; ; attempt++ {
		cli, err := rc.getClient(ctx)
		if err != nil {
			return nil, err
		}
		r, lerr := cli.ListTools(ctx, mcp.ListToolsRequest{})
		if lerr == nil {
			result = r
			break
		}
		rc.reset()
		if attempt >= 1 || ctx.Err() != nil {
			return nil, fmt.Errorf("获取远程 MCP 工具列表失败: %w", lerr)
		}
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


// imageCaptionHook 工具返回图片时的识别钩子:宿主注入视觉模型调用,
// 返回一句话画面描述;nil 或失败时退回占位符。
var imageCaptionHook func(ctx context.Context, mimeType, base64Data string) (string, error)

// SetImageCaptionHook 注入图片识别钩子(视觉模型调用),由宿主在启动时设置。
func SetImageCaptionHook(fn func(ctx context.Context, mimeType, base64Data string) (string, error)) {
	imageCaptionHook = fn
}

// describeImageContent 工具图片转结果文本：ctx 挂有 imgsink 时存图留引用标记，
// 由模型装饰器还原成 user 图片消息直达多模态主模型；视觉钩子的描述（无仓时是
// 模型了解画面的唯一来源）拼在标记之后。
// 标记必须放在文本头部：结果超长被截断时保头切尾，尾部标记会被截丢导致图片漏传。
func describeImageContent(ctx context.Context, mimeType, base64Data string) string {
	placeholder := fmt.Sprintf("[图片: %s]", mimeType)
	sink := imgsink.FromContext(ctx)
	if sink != nil {
		placeholder += "\n" + imgsink.Marker(sink.Add("data:"+mimeType+";base64,"+base64Data))
	}
	if imageCaptionHook == nil {
		return placeholder
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	desc, err := imageCaptionHook(cctx, mimeType, base64Data)
	if err != nil || strings.TrimSpace(desc) == "" {
		return placeholder
	}
	return placeholder + "\n图片内容描述: " + strings.TrimSpace(desc)
}
