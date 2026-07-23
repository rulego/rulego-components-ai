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

package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestMCPServer starts the local MCP server with testing tools, returns the URL, and closes the function
func startTestMCPServer(t *testing.T) (string, func()) {
	t.Helper()

	s := server.NewMCPServer("test-server", "1.0.0")

	// Echo tool
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

	// add tools
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

	// error_tool tools
	s.AddTool(mcp.Tool{
		Name:        "error_tool",
		Description: "Always returns an error",
		InputSchema: mcp.ToolInputSchema{Type: "object"},
	}, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "something went wrong"},
			},
		}, nil
	})

	httpServer := server.NewStreamableHTTPServer(s)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = http.Serve(listener, httpServer) }()

	addr := fmt.Sprintf("http://%s/mcp", listener.Addr().String())
	return addr, func() { _ = httpServer.Shutdown(context.Background()) }
}

// ==========================================
// Unit testing
// ==========================================

func TestClient_Type(t *testing.T) {
	c := &Client{}
	assert.Equal(t, "ai/mcpClient", c.Type())
}

func TestClient_New(t *testing.T) {
	c := &Client{}
	n := c.New()
	assert.NotNil(t, n)
	_, ok := n.(*Client)
	assert.True(t, ok)
}

func TestClient_Init_EmptyServer(t *testing.T) {
	c := &Client{}
	err := c.Init(types.NewConfig(), types.Configuration{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server config is empty")
}

func TestClient_Init_ValidConfig(t *testing.T) {
	c := &Client{}
	err := c.Init(types.NewConfig(), types.Configuration{
		"server":   "http://localhost:8080/mcp",
		"toolName": "echo",
		"args":     `{"message":"hello"}`,
	})
	assert.NoError(t, err)
	assert.Equal(t, "http://localhost:8080/mcp", c.Config.Server)
	assert.Equal(t, "echo", c.Config.ToolName)
}

// ==========================================
// Integration testing (starting the local MCP server)
// ==========================================

func TestClient_StartAndDiscover(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	defs, err := c.ListToolDefinitions()
	require.NoError(t, err)
	assert.Len(t, defs, 3)

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	assert.True(t, names["echo"])
	assert.True(t, names["add"])
	assert.True(t, names["error_tool"])
}

func TestClient_StartWithToolFilter(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server":     addr,
		"toolFilter": []string{"echo"},
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	defs, err := c.ListToolDefinitions()
	require.NoError(t, err)
	assert.Len(t, defs, 1)
	assert.Equal(t, "echo", defs[0].Name)
}

func TestClient_StartWithWildcardFilter(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server":     addr,
		"toolFilter": []string{"*"},
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	defs, err := c.ListToolDefinitions()
	require.NoError(t, err)
	assert.Len(t, defs, 3)
}

func TestClient_CallTool_Echo(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	result, err := c.CallTool(context.Background(), "echo", map[string]interface{}{
		"message": "hello world",
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", result)
}

func TestClient_CallTool_Add(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	result, err := c.CallTool(context.Background(), "add", map[string]interface{}{
		"a": float64(3),
		"b": float64(5),
	})
	require.NoError(t, err)
	assert.Equal(t, "8", result)
}

func TestClient_CallTool_Error(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	_, err = c.CallTool(context.Background(), "error_tool", map[string]interface{}{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "something went wrong")
}

func TestClient_CallTool_NotFound(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	_, err = c.CallTool(context.Background(), "nonexistent", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tool not found")
}

func TestClient_OnMsg_StaticToolName(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server":   addr,
		"toolName": "echo",
		"args":     `{"message":"${msg.text}"}`,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		assert.Equal(t, types.Success, relationType)
		assert.NoError(t, err)
		assert.Equal(t, "hello from rulego", msg.GetData())
	})

	metaData := types.NewMetadata()
	msg := ctx.NewMsg("TEST_MSG", metaData, `{"text":"hello from rulego"}`)
	c.OnMsg(ctx, msg)
}

func TestClient_OnMsg_UseBodyAsArgs(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server":   addr,
		"toolName": "add",
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		assert.Equal(t, types.Success, relationType)
		assert.NoError(t, err)
		assert.Equal(t, "42", msg.GetData())
	})

	metaData := types.NewMetadata()
	msg := ctx.NewMsg("TEST_MSG", metaData, `{"a":20,"b":22}`)
	c.OnMsg(ctx, msg)
}

func TestClient_OnMsg_ToolNameFromMetadata(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		assert.Equal(t, types.Success, relationType)
		assert.NoError(t, err)
		assert.Equal(t, "hello", msg.GetData())
	})

	metaData := types.NewMetadata()
	metaData.PutValue("mcpToolName", "echo")
	msg := ctx.NewMsg("TEST_MSG", metaData, `{"message":"hello"}`)
	c.OnMsg(ctx, msg)
}

func TestClient_OnMsg_EmptyToolName(t *testing.T) {
	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": "http://localhost:9999/mcp",
	})
	require.NoError(t, err)

	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		assert.Equal(t, types.Failure, relationType)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "toolName is empty")
	})

	metaData := types.NewMetadata()
	msg := ctx.NewMsg("TEST_MSG", metaData, `{}`)
	c.OnMsg(ctx, msg)
}

func TestClient_OnMsg_Failure(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server":   addr,
		"toolName": "error_tool",
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		assert.Equal(t, types.Failure, relationType)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "something went wrong")
	})

	metaData := types.NewMetadata()
	msg := ctx.NewMsg("TEST_MSG", metaData, `{}`)
	c.OnMsg(ctx, msg)
}

func TestClient_Destroy(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)

	c.Destroy()
	assert.False(t, c.started)
	assert.Nil(t, c.cli)
}

func TestClient_Destroy_NotStarted(t *testing.T) {
	c := &Client{}
	c.Destroy() // Don't panic
}

func TestClient_Start_Idempotent(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)

	// Repeatedly calling Start should not cause errors
	err = c.Start()
	require.NoError(t, err)

	c.Destroy()
}

func TestClient_Start_InvalidServer(t *testing.T) {
	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": "http://localhost:1/mcp",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = ctx // Start uses context.Background()
	err = c.Start()
	assert.Error(t, err)
}

func TestClient_AsMCPToolProvider(t *testing.T) {
	addr, shutdown := startTestMCPServer(t)
	defer shutdown()

	config := types.NewConfig()
	c := &Client{}
	err := c.Init(config, types.Configuration{
		"server": addr,
	})
	require.NoError(t, err)

	err = c.Start()
	require.NoError(t, err)
	defer c.Destroy()

	// The client itself implements the MCPToolProvider interface
	var provider types.MCPToolProvider = c

	defs, err := provider.ListToolDefinitions()
	require.NoError(t, err)
	assert.NotEmpty(t, defs)

	// Calling the tool through the MCPToolProvider interface
	result, err := provider.CallTool(context.Background(), "echo", map[string]interface{}{
		"message": "via provider",
	})
	require.NoError(t, err)
	assert.Equal(t, "via provider", result)
}
