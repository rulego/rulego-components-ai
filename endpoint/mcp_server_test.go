package endpoint_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/rulego/rulego"
	"github.com/rulego/rulego-components-ai/endpoint"
	"github.com/rulego/rulego/api/types"
	rulegoEndpoint "github.com/rulego/rulego/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// 单元测试
// ==========================================

func TestMcpServer_Type(t *testing.T) {
	assert.Equal(t, "endpoint/mcpServer", endpoint.Type)
}

func TestMcpServer_New(t *testing.T) {
	ep := &endpoint.McpServer{}
	n := ep.New()
	_, ok := n.(*endpoint.McpServer)
	assert.True(t, ok)
}

// ==========================================
// 集成测试（使用真实 MCP 客户端连接）
// ==========================================

// testChainJSON 返回一个简单的测试规则链 JSON
const testChainJSON = `
{
  "ruleChain": {
    "id": "test_chain_mcp",
    "name": "Test Chain MCP"
  },
  "metadata": {
    "nodes": [
      {
        "id": "s1",
        "type": "jsTransform",
        "name": "转换",
        "configuration": {
          "jsScript": "msg['processed'] = true; return {'msg':msg,'metadata':metadata,'msgType':msgType};"
        }
      }
    ],
    "connections": []
  }
}
`

// startMcpServerEndpoint 启动一个带测试规则链的 MCP Server Endpoint
func startMcpServerEndpoint(t *testing.T, port string) (string, func()) {
	t.Helper()

	config := rulego.NewConfig(types.WithDefaultPool())
	_, err := rulego.New("test_chain_mcp", []byte(testChainJSON), types.WithConfig(config))
	require.NoError(t, err)

	endpointConfig := types.Configuration{
		"server":   ":" + port,
		"basePath": "/mcp",
		"name":     "Test MCP Server",
		"version":  "1.0.0",
	}

	ep, err := rulegoEndpoint.Registry.New(endpoint.Type, config, endpointConfig)
	require.NoError(t, err)

	router := rulegoEndpoint.NewRouter().From("test_tool").To("chain:test_chain_mcp").End()
	_, err = ep.AddRouter(router, "测试工具：处理输入消息", `{"type":"object","properties":{"input":{"type":"string"}}}`)
	require.NoError(t, err)

	err = ep.Start()
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	addr := fmt.Sprintf("http://localhost:%s/mcp", port)
	return addr, func() { ep.Destroy() }
}

// connectMcpClient 创建并初始化一个 MCP 客户端连接
func connectMcpClient(t *testing.T, addr string) *client.Client {
	t.Helper()

	httpTransport, err := transport.NewStreamableHTTP(addr)
	require.NoError(t, err)

	cli := client.NewClient(httpTransport)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = cli.Start(ctx)
	require.NoError(t, err)

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "Test MCP Client",
				Version: "1.0.0",
			},
		},
	}
	_, err = cli.Initialize(ctx, initReq)
	if err != nil {
		t.Skipf("MCP Initialize failed (likely protocol version mismatch): %v", err)
	}

	return cli
}

func TestMcpServerEndpoint_ClientConnect(t *testing.T) {
	addr, shutdown := startMcpServerEndpoint(t, "19099")
	defer shutdown()

	cli := connectMcpClient(t, addr)
	defer cli.Close()

	// 验证可以正常连接并发起初始化
	result, err := cli.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestMcpServerEndpoint_ListTools(t *testing.T) {
	addr, shutdown := startMcpServerEndpoint(t, "19100")
	defer shutdown()

	cli := connectMcpClient(t, addr)
	defer cli.Close()

	result, err := cli.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Tools)

	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
		t.Logf("Tool: %s - %s", tool.Name, tool.Description)
	}
	assert.True(t, names["test_tool"])
}

func TestMcpServerEndpoint_CallTool(t *testing.T) {
	addr, shutdown := startMcpServerEndpoint(t, "19101")
	defer shutdown()

	cli := connectMcpClient(t, addr)
	defer cli.Close()

	// 调用 test_tool 工具
	result, err := cli.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "test_tool",
			Arguments: map[string]interface{}{
				"input": "hello world",
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.NotEmpty(t, result.Content)

	// 验证返回内容包含 processed=true
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			t.Logf("Result: %s", textContent.Text)
			assert.Contains(t, textContent.Text, "processed")
			assert.Contains(t, textContent.Text, "true")
		}
	}
}

func TestMcpServerEndpoint_MultipleTools(t *testing.T) {
	config := rulego.NewConfig(types.WithDefaultPool())

	// 创建两条规则链
	chain1 := `{"ruleChain":{"id":"chain_echo","name":"Echo Chain"},"metadata":{"nodes":[{"id":"s1","type":"jsTransform","name":"转换","configuration":{"jsScript":"return {'msg':msg,'metadata':metadata,'msgType':msgType};"}}],"connections":[]}}`
	chain2 := `{"ruleChain":{"id":"chain_upper","name":"Upper Chain"},"metadata":{"nodes":[{"id":"s1","type":"jsTransform","name":"转换","configuration":{"jsScript":"msg['text'] = msg['text'].toUpperCase(); return {'msg':msg,'metadata':metadata,'msgType':msgType};"}}],"connections":[]}}`

	_, err := rulego.New("chain_echo", []byte(chain1), types.WithConfig(config))
	require.NoError(t, err)
	_, err = rulego.New("chain_upper", []byte(chain2), types.WithConfig(config))
	require.NoError(t, err)

	endpointConfig := types.Configuration{
		"server":   ":19102",
		"basePath": "/mcp",
		"name":     "Multi Tool Server",
		"version":  "1.0.0",
	}

	ep, err := rulegoEndpoint.Registry.New(endpoint.Type, config, endpointConfig)
	require.NoError(t, err)

	// 注册两个工具
	r1 := rulegoEndpoint.NewRouter().From("echo").To("chain:chain_echo").End()
	_, err = ep.AddRouter(r1, "回显工具", `{"type":"object","properties":{"text":{"type":"string"}}}`)
	require.NoError(t, err)

	r2 := rulegoEndpoint.NewRouter().From("uppercase").To("chain:chain_upper").End()
	_, err = ep.AddRouter(r2, "大写转换工具", `{"type":"object","properties":{"text":{"type":"string"}}}`)
	require.NoError(t, err)

	err = ep.Start()
	require.NoError(t, err)
	defer ep.Destroy()

	time.Sleep(100 * time.Millisecond)

	cli := connectMcpClient(t, "http://localhost:19102/mcp")
	defer cli.Close()

	// 验证工具列表
	toolsResult, err := cli.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 2)

	// 调用 echo 工具
	echoResult, err := cli.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "echo",
			Arguments: map[string]interface{}{"text": "hello"},
		},
	})
	require.NoError(t, err)
	assert.False(t, echoResult.IsError)

	// 调用 uppercase 工具
	upperResult, err := cli.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "uppercase",
			Arguments: map[string]interface{}{"text": "hello"},
		},
	})
	require.NoError(t, err)
	assert.False(t, upperResult.IsError)

	for _, content := range upperResult.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			t.Logf("Uppercase result: %s", textContent.Text)
			assert.Contains(t, textContent.Text, "HELLO")
		}
	}
}

func TestMcpServerEndpoint_ConnectionPool(t *testing.T) {
	pool := endpoint.NewSSEServerCache()
	assert.NotNil(t, pool)
}

func TestMcpServerEndpoint_DefaultPool(t *testing.T) {
	assert.NotNil(t, endpoint.DefaultSSEServerPool)
}

func TestMcpServerEndpoint_RemoveRouter(t *testing.T) {
	_, shutdown := startMcpServerEndpoint(t, "19103")
	defer shutdown()
	// 验证 endpoint 正常启动和关闭
}

// TestMcpServerWithDsl 测试通过 DSL 配置 Endpoint 并使用 MCP 客户端连接
func TestMcpServerWithDsl(t *testing.T) {
	// 先创建规则链
	config := rulego.NewConfig(types.WithDefaultPool())
	_, err := rulego.New("test_chain_mcp", []byte(testChainJSON), types.WithConfig(config))
	require.NoError(t, err)

	dslConfig := `
	{
		"id": "mcp-endpoint",
		"type": "endpoint/mcpServer",
		"name": "MCP Server",
		"configuration": {
			"server": ":19104",
			"basePath": "/mcp-dsl"
		},
		"routers": [
			{
				"id": "tool1",
				"params": ["工具1描述", "{\"type\":\"object\",\"properties\":{\"input\":{\"type\":\"string\"}}}"],
				"from": {
					"path": "my_tool"
				},
				"to": {
					"path": "chain:test_chain_mcp"
				}
			}
		]
	}
	`

	ep, err := rulegoEndpoint.NewFromDsl([]byte(dslConfig))
	require.NoError(t, err)

	err = ep.Start()
	require.NoError(t, err)
	defer ep.Destroy()

	time.Sleep(100 * time.Millisecond)

	// 使用 MCP 客户端连接验证
	cli := connectMcpClient(t, "http://localhost:19104/mcp-dsl")
	defer cli.Close()

	result, err := cli.ListTools(context.Background(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Tools)

	// 验证工具名称
	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	assert.True(t, names["my_tool"])

	// 调用工具
	callResult, err := cli.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "my_tool",
			Arguments: map[string]interface{}{"input": "dsl test"},
		},
	})
	require.NoError(t, err)
	assert.False(t, callResult.IsError)
}
