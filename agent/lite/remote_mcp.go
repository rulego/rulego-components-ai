// remote_mcp.go ai/agent 轻量实现的远程 MCP 接入。
//
// server 为 http(s):// 前缀走 streamable http,其余按 stdio 命令拆分。
// 连接懒建立:首个请求需要工具表时握手并缓存 tools/list 结果,链加载
// 不依赖远程 server 可达;调用出现传输级错误即丢弃连接,下次调用重建。
package lite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/rulego/rulego/api/types"
)

// remoteMCPListTimeout 握手+拉工具表的总超时;remoteMCPCallTimeout 单次
// 工具调用超时。远程 server 挂起(连接建立后不响应)时请求不能无限等。
// var 便于测试缩短。
var (
	remoteMCPListTimeout = 15 * time.Second
	remoteMCPCallTimeout = 420 * time.Second
)

// remoteMCPProvider 一个远程 MCP server 的中立接口适配,实现
// types.MCPToolProvider,经 toolDefs/invoke 与主 provider 同一套管线消费。
type remoteMCPProvider struct {
	server string
	mu     sync.Mutex
	cli    *client.Client
	defs   []types.MCPToolDefinition // tools/list 缓存,nil=未拉取
}

func newRemoteMCPProvider(server string) *remoteMCPProvider {
	return &remoteMCPProvider{server: server}
}

// connect 懒建连并完成 initialize 握手,连接失败不残留半开状态。
// 必须在持锁状态下调用。
func (p *remoteMCPProvider) connect(ctx context.Context) (*client.Client, error) {
	if p.cli != nil {
		return p.cli, nil
	}
	var c *client.Client
	if strings.HasPrefix(p.server, "http://") || strings.HasPrefix(p.server, "https://") {
		t, err := transport.NewStreamableHTTP(p.server)
		if err != nil {
			return nil, fmt.Errorf("创建 MCP HTTP 传输失败: %w", err)
		}
		c = client.NewClient(t)
	} else {
		args := parseServerCommand(p.server)
		if len(args) == 0 {
			return nil, fmt.Errorf("无效的 MCP 命令: %s", p.server)
		}
		c = client.NewClient(transport.NewStdio(args[0], nil, args[1:]...))
	}
	if err := c.Start(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("启动 MCP 客户端失败(%s): %w", p.server, err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "RuleGo AI Agent", Version: "1.0.0"},
			Capabilities:    mcp.ClientCapabilities{},
		},
	}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("初始化 MCP 客户端失败(%s): %w", p.server, err)
	}
	p.cli = c
	return c, nil
}

// ListToolDefinitions 拉取并缓存远程工具表;失败不缓存,下次调用重试。
// 接口无 context,握手/拉取用 Background 并套总超时,server 挂起时不无限等。
func (p *remoteMCPProvider) ListToolDefinitions() ([]types.MCPToolDefinition, error) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteMCPListTimeout)
	defer cancel()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.defs != nil {
		return p.defs, nil
	}
	var result *mcp.ListToolsResult
	// 传输级失败重建连接重试一次。
	for attempt := 0; ; attempt++ {
		c, err := p.connect(ctx)
		if err != nil {
			return nil, err
		}
		r, err := c.ListTools(ctx, mcp.ListToolsRequest{})
		if err == nil {
			result = r
			break
		}
		p.closeLocked()
		if attempt >= 1 || ctx.Err() != nil {
			return nil, fmt.Errorf("获取远程 MCP 工具列表失败(%s): %w", p.server, err)
		}
	}
	defs := make([]types.MCPToolDefinition, 0, len(result.Tools))
	for _, t := range result.Tools {
		schema, _ := json.Marshal(t.InputSchema)
		defs = append(defs, types.MCPToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	p.defs = defs
	return defs, nil
}

// CallTool 调用远程工具。传输级错误(含超时)丢弃连接待下次重建;IsError
// 是工具自身的业务错误,连接保持复用。
func (p *remoteMCPProvider) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	text, _, err := p.CallToolWithImages(ctx, name, args)
	return text, err
}

// CallToolWithImages 调用远程工具,图片内容以 data URL 形式单独返回,
// 不再降级为占位文本。图片上限 4 张、总量 4MB,超出部分丢弃并在文本尾注明。
// 传输级失败重建连接重试一次;ctx 已到期或业务错误不重试。
func (p *remoteMCPProvider) CallToolWithImages(ctx context.Context, name string, args map[string]interface{}) (string, []string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, remoteMCPCallTimeout)
		defer cancel()
	}
	for attempt := 0; ; attempt++ {
		p.mu.Lock()
		c, err := p.connect(ctx)
		p.mu.Unlock()
		if err != nil {
			return "", nil, err
		}
		result, err := c.CallTool(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: name, Arguments: args},
		})
		if err == nil {
			return renderToolResult(result)
		}
		p.reset()
		if attempt >= 1 || ctx.Err() != nil {
			return "", nil, fmt.Errorf("调用远程 MCP 工具失败(%s): %w", p.server, err)
		}
	}
}

// renderToolResult 把成功传输的工具结果拆成文本与图片;IsError 是工具自身的
// 业务错误,按错误返回且不重试。
func renderToolResult(result *mcp.CallToolResult) (string, []string, error) {
	if result.IsError {
		if len(result.Content) > 0 {
			if tc, ok := result.Content[0].(mcp.TextContent); ok {
				return "", nil, fmt.Errorf("MCP 工具错误: %s", tc.Text)
			}
		}
		return "", nil, fmt.Errorf("MCP 工具错误: 未知错误")
	}
	var contents []string
	var images []string
	var totalImageBytes int
	omitted := 0
	for _, content := range result.Content {
		switch v := content.(type) {
		case mcp.TextContent:
			contents = append(contents, v.Text)
		case mcp.ImageContent:
			if len(images) < 4 && totalImageBytes+len(v.Data) < 4<<20 {
				images = append(images, "data:"+v.MIMEType+";base64,"+v.Data)
				totalImageBytes += len(v.Data)
				// 文本占位与 eino 版口径一致：多模态模型附图同时也留痕，
				// 纯文本模型至少知道结果里有图。
				contents = append(contents, fmt.Sprintf("[图片: %s]", v.MIMEType))
			} else {
				omitted++
			}
		default:
			if b, err := json.Marshal(v); err == nil {
				contents = append(contents, string(b))
			}
		}
	}
	if omitted > 0 {
		contents = append(contents, fmt.Sprintf("(另有 %d 张图片超出单次携带上限,已省略)", omitted))
	}
	return strings.Join(contents, "\n"), images, nil
}

// hasTool 工具名是否在本 server 的缓存名单里;名单未拉取或拉取失败时
// 恒为 false,该情况由调用链最终的主 provider 透传或错误兜底。
func (p *remoteMCPProvider) hasTool(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// reset 丢弃连接与工具表缓存。
func (p *remoteMCPProvider) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
	p.defs = nil
}

// Close 释放连接,链 Destroy 时调用。
func (p *remoteMCPProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
}

func (p *remoteMCPProvider) closeLocked() {
	if p.cli != nil {
		_ = p.cli.Close()
		p.cli = nil
	}
}

// parseServerCommand 按空白拆分 stdio 命令行,支持成对单双引号。
// tool/mcp 包有等价实现,该包依赖 eino,此处不引入。
func parseServerCommand(cmd string) []string {
	var result []string
	var current string
	var inQuote bool
	var quoteChar rune
	for _, r := range cmd {
		switch {
		case r == ' ' && !inQuote:
			if current != "" {
				result = append(result, current)
				current = ""
			}
		case r == '"' || r == '\'':
			if inQuote && r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			} else {
				current += string(r)
			}
		default:
			current += string(r)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
