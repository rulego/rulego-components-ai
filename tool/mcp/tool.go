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
	aitool "github.com/rulego/rulego-components-ai/tool"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const (
	// ToolName MCP 工具名称
	ToolName = "mcp_tool"
)

// Config MCP 工具配置
type Config struct {
	// Server MCP 服务器地址，可以是命令行或 HTTP URL
	// 例如: "python server.py" 或 "http://localhost:8080/mcp"
	Server string `json:"server" label:"MCP服务器" desc:"MCP 服务器地址，可以是命令行或 HTTP URL"`

	// Timeout MCP 连接超时时间（秒）
	// Default: 30
	Timeout int `json:"timeout" label:"超时时间" desc:"MCP 连接超时时间（秒）"`

	// ClientName 客户端名称
	// Default: RuleGo AI Agent
	ClientName string `json:"client_name" label:"客户端名称" desc:"MCP 客户端名称"`

	// ClientVersion 客户端版本
	// Default: 1.0.0
	ClientVersion string `json:"client_version" label:"客户端版本" desc:"MCP 客户端版本"`
}

// DefaultConfig 获取默认配置
func DefaultConfig() Config {
	return Config{
		Timeout:       30,
		ClientName:    "RuleGo AI Agent",
		ClientVersion: "1.0.0",
	}
}

type mcpTool struct {
	config Config
	client *client.Client
	mu     sync.RWMutex
	tools  []schema.ToolInfo
}

// NewTool 创建 MCP 工具
func NewTool(config Config) (tool.BaseTool, error) {
	t := &mcpTool{
		config: config,
	}

	if config.Timeout <= 0 {
		t.config.Timeout = DefaultConfig().Timeout
	}
	if t.config.ClientName == "" {
		t.config.ClientName = DefaultConfig().ClientName
	}
	if t.config.ClientVersion == "" {
		t.config.ClientVersion = DefaultConfig().ClientVersion
	}

	return t, nil
}

// Info 返回工具信息
func (t *mcpTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	props := orderedmap.New[string, *jsonschema.Schema]()
	props.Set("tool_name", &jsonschema.Schema{
		Type:        "string",
		Description: "MCP 工具名称",
	})
	props.Set("arguments", &jsonschema.Schema{
		Type:        "object",
		Description: "MCP 工具参数（键值对）",
	})

	return &schema.ToolInfo{
		Name: ToolName,
		Desc: fmt.Sprintf("调用 MCP 服务器提供的工具。连接到: %s", t.config.Server),
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"tool_name"},
		}),
	}, nil
}

// InvokableRun 执行 MCP 工具调用
func (t *mcpTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}

	toolName, ok := params["tool_name"].(string)
	if !ok || toolName == "" {
		return "", fmt.Errorf("tool_name 不能为空")
	}

	toolArgs, _ := params["arguments"].(map[string]interface{})
	if toolArgs == nil {
		toolArgs = make(map[string]interface{})
	}

	client, err := t.getClient(ctx)
	if err != nil {
		return "", err
	}

	callRequest := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: toolArgs,
		},
	}

	result, err := client.CallTool(ctx, callRequest)
	if err != nil {
		return "", fmt.Errorf("调用 MCP 工具失败: %w", err)
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
		switch c := content.(type) {
		case mcp.TextContent:
			contents = append(contents, c.Text)
		case mcp.ImageContent:
			contents = append(contents, fmt.Sprintf("[图片: %s]", c.MIMEType))
		default:
			if b, err := json.Marshal(c); err == nil {
				contents = append(contents, string(b))
			}
		}
	}

	return strings.Join(contents, "\n"), nil
}

// getClient 获取或初始化 MCP 客户端
func (t *mcpTool) getClient(ctx context.Context) (*client.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.client != nil {
		return t.client, nil
	}

	var c *client.Client

	if strings.HasPrefix(t.config.Server, "http://") || strings.HasPrefix(t.config.Server, "https://") {
		httpTransport, tErr := transport.NewStreamableHTTP(t.config.Server)
		if tErr != nil {
			return nil, fmt.Errorf("创建 HTTP 传输失败: %w", tErr)
		}
		c = client.NewClient(httpTransport)
	} else {
		args := ParseCommand(t.config.Server)
		if len(args) == 0 {
			return nil, fmt.Errorf("无效的 MCP 命令: %s", t.config.Server)
		}
		command := args[0]
		cmdArgs := args[1:]
		stdioTransport := transport.NewStdio(command, nil, cmdArgs...)
		c = client.NewClient(stdioTransport)
	}

	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("启动 MCP 客户端失败: %w", err)
	}

	initRequest := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    t.config.ClientName,
				Version: t.config.ClientVersion,
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	}

	if _, err := c.Initialize(ctx, initRequest); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("初始化 MCP 客户端失败: %w", err)
	}

	t.client = c
	return c, nil
}

// ParseCommand 解析命令字符串为命令和参数
func ParseCommand(cmd string) []string {
	result := []string{}
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
		case (r == '"' || r == '\''):
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

// Close 关闭 MCP 客户端连接
func (t *mcpTool) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.client != nil {
		err := t.client.Close()
		t.client = nil
		return err
	}
	return nil
}

// Register 手动注册 MCP 工具
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault 使用默认配置注册 MCP 工具到全局注册表
func RegisterDefault() error {
	def := aitool.ToolDefinition{
		Name:   ToolName,
		Desc:   "调用 MCP (Model Context Protocol) 服务器提供的工具",
		Config: Config{},
		Factory: func(config map[string]interface{}) (tool.BaseTool, error) {
			var cfg Config
			b, _ := json.Marshal(config)
			if err := json.Unmarshal(b, &cfg); err != nil {
				return nil, err
			}
			defaultCfg := DefaultConfig()
			if cfg.Timeout == 0 {
				cfg.Timeout = defaultCfg.Timeout
			}
			if cfg.ClientName == "" {
				cfg.ClientName = defaultCfg.ClientName
			}
			if cfg.ClientVersion == "" {
				cfg.ClientVersion = defaultCfg.ClientVersion
			}
			return NewTool(cfg)
		},
	}

	instance, err := NewTool(Config{Server: ""})
	if err != nil {
		return err
	}
	def.Instance = instance

	return aitool.Registry.RegisterDef(def)
}

func init() {
	_ = RegisterDefault()
}
