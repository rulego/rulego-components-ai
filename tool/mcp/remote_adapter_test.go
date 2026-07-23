package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// Unit testing (no real MCP server required)
// ==========================================

func TestRemoteMCPToolAdapter_Info(t *testing.T) {
	adapter := &RemoteMCPToolAdapter{
		name:        "echo",
		description: "Echoes back the input",
		inputSchema: []byte(`{"type":"object","properties":{"message":{"type":"string","description":"The message to echo"}},"required":["message"]}`),
		rc:          newRemoteMCPClient("http://localhost:0"),
	}

	info, err := adapter.Info(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "echo", info.Name)
	assert.Equal(t, "Echoes back the input", info.Desc)
	assert.NotNil(t, info.ParamsOneOf)
}

func TestRemoteMCPToolAdapter_Info_EmptySchema(t *testing.T) {
	adapter := &RemoteMCPToolAdapter{
		name:        "no_args_tool",
		description: "A tool without arguments",
		inputSchema: nil,
		rc:          newRemoteMCPClient("http://localhost:0"),
	}

	info, err := adapter.Info(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "no_args_tool", info.Name)
	assert.Equal(t, "A tool without arguments", info.Desc)
	assert.NotNil(t, info.ParamsOneOf)
}

func TestRemoteMCPToolAdapter_Info_InvalidSchema(t *testing.T) {
	adapter := &RemoteMCPToolAdapter{
		name:        "bad_schema",
		description: "Tool with bad schema",
		inputSchema: []byte(`{invalid json`),
		rc:          newRemoteMCPClient("http://localhost:0"),
	}

	info, err := adapter.Info(context.Background())
	require.NoError(t, err)

	// An invalid schema should be rolled back to an empty object
	assert.Equal(t, "bad_schema", info.Name)
	assert.NotNil(t, info.ParamsOneOf)
}

func TestRemoteMCPToolAdapter_InvokableRun_InvalidJSON(t *testing.T) {
	adapter := &RemoteMCPToolAdapter{
		name:        "echo",
		description: "Echo",
		inputSchema: nil,
		rc:          newRemoteMCPClient("http://localhost:0"),
	}

	_, err := adapter.InvokableRun(context.Background(), "not json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "解析参数失败")
}

func TestRemoteMCPToolAdapter_InvokableRun_ConnectionFailed(t *testing.T) {
	adapter := &RemoteMCPToolAdapter{
		name:        "echo",
		description: "Echo",
		inputSchema: nil,
		rc:          newRemoteMCPClient("http://localhost:1"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := adapter.InvokableRun(ctx, `{"message":"hello"}`)
	assert.Error(t, err)
}

func TestRemoteMCPToolAdapter_InvokableRun_EmptyArgs(t *testing.T) {
	adapter := &RemoteMCPToolAdapter{
		name:        "echo",
		description: "Echo",
		inputSchema: nil,
		rc:          newRemoteMCPClient("http://localhost:1"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := adapter.InvokableRun(ctx, "")
	assert.Error(t, err)
}

func TestRemoteMCPClient_InvalidCommand(t *testing.T) {
	rc := newRemoteMCPClient("nonexistent-command-xyz-12345")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rc.getClient(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "启动 MCP 客户端失败")
}

func TestRemoteMCPClient_InvalidURL(t *testing.T) {
	rc := newRemoteMCPClient("http://localhost:1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rc.getClient(ctx)
	assert.Error(t, err)
}

func TestRemoteMCPClient_ConcurrentAccess(t *testing.T) {
	rc := newRemoteMCPClient("http://localhost:1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errs := make(chan error, 2)
	go func() { _, _ = rc.getClient(ctx); errs <- nil }()
	go func() { _, _ = rc.getClient(ctx); errs <- nil }()

	<-errs
	<-errs
}

func TestCreateToolsFromRemote_InvalidServer(t *testing.T) {
	_, err := CreateToolsFromRemote("http://localhost:1", nil)
	assert.Error(t, err)
}

// ==========================================
// Integration testing (starting the local MCP server)
// ==========================================

// startTestMCPServer starts a local MCP server with testing tools, returning the URL and shutdown function.
func startTestMCPServer(t *testing.T) (string, func()) {
	t.Helper()

	s := server.NewMCPServer("test-server", "1.0.0")

	// Register for the Echo tool
	s.AddTool(mcp.Tool{
		Name:        "echo",
		Description: "Echoes back the input message",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to echo",
				},
			},
			Required: []string{"message"},
		},
	}, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := request.Params.Arguments.(map[string]interface{})
		msg, _ := args["message"].(string)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: msg},
			},
		}, nil
	})

	// Register for the add tool
	s.AddTool(mcp.Tool{
		Name:        "add",
		Description: "Adds two numbers",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"a": map[string]any{"type": "number"},
				"b": map[string]any{"type": "number"},
			},
			Required: []string{"a", "b"},
		},
	}, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := request.Params.Arguments.(map[string]interface{})
		a, _ := args["a"].(float64)
		b, _ := args["b"].(float64)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: fmt.Sprintf("%.0f", a+b)},
			},
		}, nil
	})

	// Register error_tool tool (simulation tool execution error)
	s.AddTool(mcp.Tool{
		Name:        "error_tool",
		Description: "Always returns an error",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
		},
	}, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "something went wrong"},
			},
		}, nil
	})

	httpServer := server.NewStreamableHTTPServer(s)

	// Use port 0 to have the system automatically allocate available ports
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = http.Serve(listener, httpServer)
	}()

	addr := fmt.Sprintf("http://%s/mcp", listener.Addr().String())

	return addr, func() {
		_ = httpServer.Shutdown(context.Background())
	}
}

func TestCreateToolsFromRemote_DiscoverAll(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	tools, err := CreateToolsFromRemote(addr, nil)
	require.NoError(t, err)

	assert.Len(t, tools, 3)

	names := map[string]bool{}
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		require.NoError(t, err)
		names[info.Name] = true
		assert.NotEmpty(t, info.Desc)
		assert.NotNil(t, info.ParamsOneOf)
	}
	assert.True(t, names["echo"])
	assert.True(t, names["add"])
	assert.True(t, names["error_tool"])
}

func TestCreateToolsFromRemote_Filter(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	tools, err := CreateToolsFromRemote(addr, []string{"echo", "add"})
	require.NoError(t, err)

	assert.Len(t, tools, 2)

	names := map[string]bool{}
	for _, tl := range tools {
		info, _ := tl.Info(context.Background())
		names[info.Name] = true
	}
	assert.True(t, names["echo"])
	assert.True(t, names["add"])
	assert.False(t, names["error_tool"])
}

func TestCreateToolsFromRemote_Wildcard(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	tools, err := CreateToolsFromRemote(addr, []string{"*"})
	require.NoError(t, err)
	assert.Len(t, tools, 3)
}

func TestCreateToolsFromRemote_NoMatch(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	tools, err := CreateToolsFromRemote(addr, []string{"nonexistent"})
	require.NoError(t, err)
	assert.Len(t, tools, 0)
}

func TestCreateToolsFromRemote_CallEcho(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	tools, err := CreateToolsFromRemote(addr, []string{"echo"})
	require.NoError(t, err)
	require.Len(t, tools, 1)

	invokable := tools[0].(tool.InvokableTool)
	result, err := invokable.InvokableRun(context.Background(), `{"message":"hello world"}`)
	require.NoError(t, err)
	assert.Equal(t, "hello world", result)
}

func TestCreateToolsFromRemote_CallAdd(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	tools, err := CreateToolsFromRemote(addr, []string{"add"})
	require.NoError(t, err)
	require.Len(t, tools, 1)

	invokable := tools[0].(tool.InvokableTool)
	result, err := invokable.InvokableRun(context.Background(), `{"a":3,"b":5}`)
	require.NoError(t, err)
	assert.Equal(t, "8", result)
}

func TestCreateToolsFromRemote_CallError(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	tools, err := CreateToolsFromRemote(addr, []string{"error_tool"})
	require.NoError(t, err)
	require.Len(t, tools, 1)

	invokable := tools[0].(tool.InvokableTool)
	_, err = invokable.InvokableRun(context.Background(), `{}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "something went wrong")
}

func TestCreateToolsFromRemote_SharedClient(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	// Create multiple tools that should share the same underlying MCP client connection
	tools, err := CreateToolsFromRemote(addr, nil)
	require.NoError(t, err)
	require.Len(t, tools, 3)

	// Continuously call different tools to verify that the shared connection is working properly
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		require.NoError(t, err)

		invokable := tl.(tool.InvokableTool)
		switch info.Name {
		case "echo":
			result, err := invokable.InvokableRun(context.Background(), `{"message":"test"}`)
			require.NoError(t, err)
			assert.Equal(t, "test", result)
		case "add":
			result, err := invokable.InvokableRun(context.Background(), `{"a":1,"b":2}`)
			require.NoError(t, err)
			assert.Equal(t, "3", result)
		case "error_tool":
			_, err := invokable.InvokableRun(context.Background(), `{}`)
			require.Error(t, err)
		}
	}
}
