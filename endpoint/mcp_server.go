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

// Package endpoint provides an MCP (Model Context Protocol) server implementation for RuleGo.
// It allows exposing rule chains as MCP tools, enabling LLMs to interact with RuleGo workflows.
//
// Key Features:
// - Expose RuleChains as MCP Tools automatically or manually.
// - Support SSE (Server-Sent Events) transport for MCP.
// - Dynamic tool registration based on RuleChain definition.
package endpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/api/types/endpoint"
	endpointImpl "github.com/rulego/rulego/endpoint"
	"github.com/rulego/rulego/endpoint/rest"
	"github.com/rulego/rulego/utils/dsl"
	"github.com/rulego/rulego/utils/json"
	"github.com/rulego/rulego/utils/maps"
	"github.com/rulego/rulego/utils/str"
)

const (
	// KeyInMessage is the default argument name for tools without explicit variables
	KeyInMessage = "inMessage"
	// KeyId is the metadata key for identifying the target SSE server
	KeyId = "id"
)

// Type is the component type identifier
const Type = types.EndpointTypePrefix + "mcpServer"

// Endpoint is an alias for McpServer
type Endpoint = McpServer

// Register the component
func init() {
	_ = endpointImpl.Registry.Register(&Endpoint{})
}

var DefaultSSEServerPool = NewSSEServerCache()

// SSEServerPool manages SSE servers, keyed by ID (e.g., RuleChain ID)
type SSEServerPool struct {
	sync.RWMutex
	cache map[string]*server.SSEServer
}

// NewSSEServerCache creates a new SSEServerPool
func NewSSEServerCache() *SSEServerPool {
	return &SSEServerPool{
		cache: make(map[string]*server.SSEServer),
	}
}

// Get retrieves an SSEServer by ID
func (p *SSEServerPool) Get(id string) (*server.SSEServer, bool) {
	p.RLock()
	defer p.RUnlock()
	sse, ok := p.cache[id]
	return sse, ok
}

// Add adds an SSEServer to the pool
func (p *SSEServerPool) Add(id string, server *server.SSEServer) {
	p.Lock()
	defer p.Unlock()
	p.cache[id] = server
}

// Delete removes an SSEServer from the pool
func (p *SSEServerPool) Delete(id string) {
	p.Lock()
	defer p.Unlock()
	delete(p.cache, id)
}

// Config defines the configuration for McpServer
type Config struct {
	Server     string `json:"server" label:"Server Address" desc:"Address to listen on, e.g. :8080" required:"true"`
	CertFile   string `json:"certFile" label:"Cert File" desc:"TLS certificate file path"`
	CertKeyFile string `json:"certKeyFile" label:"Cert Key File" desc:"TLS private key file path"`
	AllowCors  bool   `json:"allowCors" label:"Allow CORS" desc:"Enable Cross-Origin Resource Sharing"`
	Name       string `json:"name" label:"Server Name" desc:"MCP server name. Defaults to RuleChain name"`
	Version    string `json:"version" label:"Server Version" desc:"MCP server version. Defaults to RuleChain version"`
	BasePath   string `json:"basePath" label:"Base Path" desc:"Root path for MCP endpoints. Defaults to /api/v1/rules/{ruleChain.id}/mcp"`
}

// McpServer implements the Model Context Protocol (MCP) server endpoint.
// It wraps a REST server to provide SSE transport and manages MCP tools derived from RuleChains.
type McpServer struct {
	*rest.Rest
	// Config is the server configuration
	Config Config
	// SSEServerPool manages multiple SSE server instances
	SSEServerPool *SSEServerPool

	mcpServer *server.MCPServer
	sseServer *server.SSEServer
	// ruleChain is the associated rule chain definition (if any)
	ruleChain *types.RuleChain
	// basePath is the effective base path for routes
	basePath string
	// baseFullPath includes the host/port part (logic-wise) or just the path
	baseFullPath string
	// defaultBasePath indicates if the path was auto-generated
	defaultBasePath bool
	sseServerId     string
}

// Type returns the component type
func (s *McpServer) Type() string {
	return Type
}

// New creates a new McpServer instance
func (s *McpServer) New() types.Node {
	return &McpServer{
		Config: Config{
			Server: ":6334",
		},
	}
}

// Init initializes the McpServer with configuration
func (s *McpServer) Init(ruleConfig types.Config, configuration types.Configuration) error {
	err := maps.Map2Struct(configuration, &s.Config)
	if err != nil {
		return err
	}
	s.Rest = &rest.Rest{}
	if err = s.Rest.Init(ruleConfig, configuration); err != nil {
		return err
	}

	s.SSEServerPool = DefaultSSEServerPool
	s.ruleChain = s.GetRuleChainDefinition(configuration)

	// Set default Name and Version from RuleChain if available
	if s.ruleChain != nil {
		if s.Config.Name == "" {
			s.Config.Name = s.ruleChain.RuleChain.Name
		}
		if s.Config.Version == "" {
			if v, ok := s.ruleChain.RuleChain.GetAdditionalInfo("version"); ok {
				s.Config.Version = str.ToString(v)
			}
		}
	}
	if s.Config.Name == "" {
		s.Config.Name = "RuleGo MCP Server"
	}
	if s.Config.Version == "" {
		s.Config.Version = "1.0.0"
	}

	// Configure BasePath
	base := strings.TrimSpace(s.Config.BasePath)
	if base == "" {
		if s.ruleChain != nil {
			base = "/api/v1/rules/:id/mcp"
			s.baseFullPath = "/api/v1/rules/" + s.ruleChain.RuleChain.ID + "/mcp"
			s.defaultBasePath = true
		}
	} else {
		s.baseFullPath = base
	}

	if base == "" {
		return fmt.Errorf("basePath can not be empty")
	}
	s.basePath = base

	if !strings.HasPrefix(s.basePath, "/") {
		s.basePath = "/" + s.basePath
	}
	return err
}

// Id returns the server address
func (s *McpServer) Id() string {
	return s.Config.Server
}

// AddRouter registers a rule chain as an MCP tool.
// router: The router defining the target rule chain.
// params: Optional parameters. params[0] is description, params[1] is inputSchema (JSON string).
func (s *McpServer) AddRouter(router endpoint.Router, params ...interface{}) (id string, err error) {
	if router == nil {
		return "", errors.New("router can not nil")
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("addRouter err :%v", e)
		}
	}()

	var desc, inputSchema string
	if len(params) > 0 {
		desc = str.ToString(params[0])
	}
	if len(params) > 1 {
		inputSchema = str.ToString(params[1])
	}

	err = s.addRouter(router, desc, inputSchema)
	return router.GetId(), err
}

// RemoveRouter removes a registered tool
func (s *McpServer) RemoveRouter(routerId string, params ...interface{}) error {
	routerId = strings.TrimSpace(routerId)
	s.DeleteTools(routerId)
	return nil
}

// Printf logs messages using the rule engine logger
func (s *McpServer) Printf(format string, v ...interface{}) {
	if s.RuleConfig.Logger != nil {
		s.RuleConfig.Logger.Printf(format, v...)
	}
}

// Start starts the HTTP server and registers SSE endpoints
func (s *McpServer) Start() error {
	// Register SSE and Message endpoints
	if s.defaultBasePath && !s.Rest.HasRouter(s.basePath+"/sse") {
		s.Rest.GET(s.handlerFromPool(s.basePath + "/sse"))
		s.Rest.POST(s.handlerFromPool(s.basePath + "/message"))
	} else {
		s.Rest.GET(s.handler(s.basePath + "/sse"))
		s.Rest.POST(s.handler(s.basePath + "/message"))
	}

	if s.Rest.Started() {
		return nil
	}
	if err := s.Rest.Start(); err != nil {
		return err
	}

	// Register to SSEServerPool if using default base path with RuleChain ID
	if s.defaultBasePath && s.ruleChain != nil {
		sseServerId := s.Config.Name
		if s.ruleChain != nil && s.ruleChain.RuleChain.ID != "" {
			sseServerId = s.ruleChain.RuleChain.ID
		}
		s.sseServerId = sseServerId
		s.GetSSEServerPool().Add(s.sseServerId, s.SSEServer())
	}

	return nil
}

// Destroy cleans up resources
func (s *McpServer) Destroy() {
	_ = s.Close()
}

// Desc returns the component description
func (s *McpServer) Desc() string {
	return "Expose RuleChains as MCP tools via SSE transport. Supports dynamic tool registration, route targeting and CORS"
}

// Close stops the server and cleans up
func (s *McpServer) Close() error {
	if s.sseServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = s.sseServer.Shutdown(ctx)
	}
	if !s.defaultBasePath && s.Rest != nil {
		_ = s.Rest.RemoveRouter(s.Rest.RouterKey("GET", s.basePath+"/sse"))
		_ = s.Rest.RemoveRouter(s.Rest.RouterKey("POST", s.basePath+"/message"))
	}
	if s.Rest != nil {
		s.Rest.Destroy()
	}
	s.GetSSEServerPool().Delete(s.sseServerId)

	return nil
}

// addRouter internal implementation to add a tool
func (s *McpServer) addRouter(router endpoint.Router, desc string, inputSchema string) error {
	if router.GetId() == "" {
		router.SetId(router.FromToString())
	}

	to := router.GetFrom().GetTo().ToString()
	values := strings.Split(to, ":")
	length := len(values)
	if length == 0 {
		return errors.New("to executor is empty")
	}

	var chainId = values[0]
	var startNodeId string
	if length > 1 {
		startNodeId = values[1]
	}

	// Use router.From as tool name
	toolName := router.FromToString()
	if desc == "" {
		desc = toolName
	}

	var def types.RuleChain
	if s.ruleChain != nil {
		def = *s.ruleChain
	}

	// Try to get RuleChain definition to parse variables/schema
	if s.ruleChain != nil && def.RuleChain.ID == chainId {
		s.AddToolsFromChain(chainId, startNodeId, def, toolName, desc, inputSchema, router)
	} else if engine, ok := router.GetRuleGo(nil).Get(chainId); !ok {
		// RuleChain not found in engine, try to load if it's a file path?
		// For now, we return error or assume it will be available at runtime.
		// But AddToolsFromChain needs definition to parse vars.
		return fmt.Errorf("chainId:%s not found", chainId)
	} else {
		s.AddToolsFromChain(chainId, startNodeId, engine.Definition(), toolName, desc, inputSchema, router)
	}
	return nil
}

// handler creates a router for specific SSE server
func (s *McpServer) handler(url string) endpoint.Router {
	return endpointImpl.NewRouter().From(url).Process(func(router endpoint.Router, exchange *endpoint.Exchange) bool {
		inMsg := exchange.In.(*rest.RequestMessage)
		s.SSEServer().ServeHTTP(inMsg.Response(), inMsg.Request())
		return true
	}).End()
}

// handlerFromPool creates a router that selects SSE server from pool based on ID
func (s *McpServer) handlerFromPool(url string) endpoint.Router {
	return endpointImpl.NewRouter().From(url).Process(func(router endpoint.Router, exchange *endpoint.Exchange) bool {
		inMsg := exchange.In.(*rest.RequestMessage)
		id := inMsg.Metadata.GetValue(KeyId)
		if sseServer, ok := s.GetSSEServerPool().Get(id); ok {
			sseServer.ServeHTTP(inMsg.Response(), inMsg.Request())
		} else {
			// Fallback to default server if ID not found
			s.SSEServer().ServeHTTP(inMsg.Response(), inMsg.Request())
		}
		return true
	}).End()
}

// MCPServer returns the underlying MCPServer instance, creating it if needed
func (s *McpServer) MCPServer() *server.MCPServer {
	if s.mcpServer == nil {
		s.mcpServer = s.NewMCPServer()
	}
	return s.mcpServer
}

// SSEServer returns the underlying SSEServer instance, creating it if needed
func (s *McpServer) SSEServer() *server.SSEServer {
	if s.sseServer == nil {
		s.sseServer = s.NewSSEServer(server.WithBasePath(s.baseFullPath), server.WithHTTPServer(s.Rest.GetServer()))
	}
	return s.sseServer
}

// DeleteTools removes tools from the MCP server
func (s *McpServer) DeleteTools(names ...string) {
	if s.mcpServer != nil {
		s.mcpServer.DeleteTools(names...)
	}
}

// GetSSEServerPool returns the pool instance
func (s *McpServer) GetSSEServerPool() *SSEServerPool {
	if s.SSEServerPool == nil {
		s.SSEServerPool = DefaultSSEServerPool
	}
	return s.SSEServerPool
}

// NewMCPServer creates a configured MCPServer instance
func (s *McpServer) NewMCPServer() *server.MCPServer {
	mcpServer := server.NewMCPServer(
		s.Config.Name,
		s.Config.Version,
	)
	return mcpServer
}

// NewSSEServer creates a configured SSEServer instance
func (s *McpServer) NewSSEServer(opts ...server.SSEOption) *server.SSEServer {
	return server.NewSSEServer(s.mcpServer, opts...)
}

// AddToolsFromChain registers a rule chain as a tool
func (s *McpServer) AddToolsFromChain(chainId, startNodeId string, def types.RuleChain, toolName, toolDesc string, inputSchema string, router endpoint.Router) {
	if toolDesc == "" {
		toolDesc = def.RuleChain.Name
		if v := str.ToString(def.RuleChain.AdditionalInfo["description"]); v != "" {
			toolDesc = v
		}
	}

	if toolDesc != "" {
		var tool mcp.Tool
		// 1. Try to use provided inputSchema
		if inputSchema != "" {
			tool = mcp.NewToolWithRawSchema(toolName, toolDesc, []byte(inputSchema))
		} else if inputSchemaMap, ok := def.RuleChain.AdditionalInfo["inputSchema"]; ok {
			// 2. Try to use inputSchema from RuleChain definition
			if schema, err := json.Marshal(inputSchemaMap); err == nil {
				tool = mcp.NewToolWithRawSchema(toolName, toolDesc, schema)
			}
		} else {
			// 3. Auto-detect variables from RuleChain nodes
			var vars []string
			// Get all vars from nodes
			if startNodeId == "" {
				vars = dsl.ParseVars(types.MsgKey, def)
			} else {
				// If startNodeId is provided, parse vars from that node or sub-chain
				if dsl.IsFlowNode(def, startNodeId) {
					if engine, ok := router.GetRuleGo(nil).Get(startNodeId); !ok {
						vars = dsl.ParseVars(types.MsgKey, def, startNodeId)
					} else {
						vars = dsl.ParseVars(types.MsgKey, engine.Definition())
					}
				} else {
					vars = dsl.ParseVars(types.MsgKey, def, startNodeId)
				}
			}

			if len(vars) > 0 {
				var toolOptions []mcp.ToolOption
				for _, item := range vars {
					toolOptions = append(toolOptions, mcp.WithString(item, mcp.Required(), mcp.Description("input param: "+item)))
				}
				toolOptions = append(toolOptions, mcp.WithDescription(toolDesc))
				tool = mcp.NewTool(toolName, toolOptions...)
			} else {
				// Fallback: Create a tool with a generic 'inMessage' argument
				tool = mcp.NewTool(toolName, mcp.WithDescription(toolDesc), mcp.WithObject(KeyInMessage, mcp.Description("input message")))
			}

		}
		// Register tool handler
		s.MCPServer().AddTool(tool, s.ruleChainToolHandler(chainId, startNodeId, router.GetRuleGo(nil)))
	}
}

// ruleChainToolHandler creates the callback function for tool execution
func (s *McpServer) ruleChainToolHandler(chainId, startNodeId string, pool types.RuleEnginePool) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ruleEngine, ok := pool.Get(chainId)
		if !ok {
			return nil, fmt.Errorf("rule chain not found: %s", chainId)
		}

		// Prepare message from tool arguments
		var msg string
		arguments := request.GetArguments()
		if arguments != nil {
			if params, ok := arguments[KeyInMessage]; ok {
				msg = str.ToString(params)
			} else if v, err := json.Marshal(arguments); err != nil {
				return nil, fmt.Errorf("failed to marshal arguments: %v", err)
			} else {
				msg = string(v)
			}
		}

		// Execute RuleChain
		wg := sync.WaitGroup{}
		wg.Add(1)
		var result string
		var resultErr error

		var opts []types.RuleContextOption
		if startNodeId != "" {
			opts = append(opts, types.WithStartNode(startNodeId))
		}
		// Use WithContext to propagate context
		opts = append(opts, types.WithContext(ctx))

		opts = append(opts, types.WithOnEnd(func(ctx types.RuleContext, msg types.RuleMsg, err error, relationType string) {
			if err != nil {
				resultErr = err
			} else {
				result = msg.GetData()
			}
			wg.Done()
		}))

		// Use OnMsgAndWait if possible, but we are manually waiting with WaitGroup to capture result properly
		// Actually, ruleEngine.OnMsgAndWait blocks until OnEnd, so we can use it directly or use OnMsg + WaitGroup.
		// Using OnMsg + WaitGroup allows us to capture the result in the callback closure.
		ruleEngine.OnMsg(types.NewMsgWithJsonData(msg), opts...)

		wg.Wait()

		if resultErr != nil {
			s.Printf("Tool execution failed for chain %s: %v", chainId, resultErr)
			return mcp.NewToolResultError(fmt.Sprintf("Execution error: %v", resultErr)), nil
		}

		// Try to parse result as MCP Content if it's a valid JSON
		// This allows RuleChains to return rich content (images, resources) if they follow MCP schema
		// TODO: Implement richer content parsing if needed. For now, return as Text.
		return mcp.NewToolResultText(result), nil
	}
}
