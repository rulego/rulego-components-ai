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

package endpoint

import (
	"context"
	"errors"
	"fmt"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rulego/rulego/endpoint/rest"
	"github.com/rulego/rulego/utils/dsl"
	"github.com/rulego/rulego/utils/json"
	"strings"
	"sync"
	"time"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/api/types/endpoint"
	endpointImpl "github.com/rulego/rulego/endpoint"
	"github.com/rulego/rulego/utils/maps"
	"github.com/rulego/rulego/utils/str"
)

const (
	KeyInMessage = "inMessage"
	KeyId        = "id"
)

// Type 组件类型
const Type = types.EndpointTypePrefix + "mcpServer"

// Endpoint 别名
type Endpoint = McpServer

// 注册组件
func init() {
	_ = endpointImpl.Registry.Register(&Endpoint{})
}

var DefaultSSEServerPool = NewSSEServerCache()

// SSEServerPool SSEServer池 key=规则链ID
type SSEServerPool struct {
	sync.RWMutex
	cache map[string]*server.SSEServer
}

func NewSSEServerCache() *SSEServerPool {
	return &SSEServerPool{
		cache: make(map[string]*server.SSEServer),
	}
}
func (p *SSEServerPool) Get(id string) (*server.SSEServer, bool) {
	p.RLock()
	defer p.RUnlock()
	sse, ok := p.cache[id]
	return sse, ok
}

func (p *SSEServerPool) Add(id string, server *server.SSEServer) {
	p.Lock()
	defer p.Unlock()
	p.cache[id] = server
}

func (p *SSEServerPool) Delete(id string) {
	p.Lock()
	defer p.Unlock()
	delete(p.cache, id)
}

// Config McpServer 服务配置
type Config struct {
	Server      string `json:"server"`
	CertFile    string `json:"certFile"`
	CertKeyFile string `json:"certKeyFile"`
	// 是否允许跨域
	AllowCors bool `json:"allowCors"`
	// Name MCP服务器名称，如果空，则从规则链`ruleChain.name`获取
	Name string `json:"name"`
	// Version MCP服务器版本，如果空，则从规则链`ruleChain.additionalInfo.version`中获取
	Version string `json:"version"`
	// BasePath sse服务根路径，如果空，则使用:/api/v1/rules/{ruleChain.id}/mcp
	BasePath string `json:"basePath"`
}

// McpServer MCP协议接收端点
// 使用规则链提供MCP协议工具
type McpServer struct {
	*rest.Rest
	//配置
	Config Config
	// SSEServerPool sse服务池
	SSEServerPool *SSEServerPool

	mcpServer *server.MCPServer
	sseServer *server.SSEServer
	//规则链定义
	ruleChain *types.RuleChain
	// BasePath sse服务根路径，如果空使用:/api/v1/rules/:id/mcp
	basePath string
	// baseFullPath sse服务根路径，如果空使用:/api/v1/rules/{ruleChain.id}/mcp
	baseFullPath string
	// 是否使用默认的basePath
	defaultBasePath bool
	sseServerId     string
}

// Type 组件类型
func (s *McpServer) Type() string {
	return Type
}

func (s *McpServer) New() types.Node {
	return &McpServer{
		Config: Config{
			Server: ":6334",
		},
	}
}

// Init 初始化
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
	} else {
		s.basePath = base
	}
	if !strings.HasPrefix(s.basePath, "/") {
		s.basePath = "/" + s.basePath
	}
	return err
}

func (s *McpServer) Id() string {
	return s.Config.Server
}

func (s *McpServer) AddRouter(router endpoint.Router, params ...interface{}) (id string, err error) {
	if router == nil {
		return "", errors.New("router can not nil")
	} else {
		defer func() {
			if e := recover(); e != nil {
				err = fmt.Errorf("addRouter err :%v", e)
			}
		}()
		var desc, inputSchema string //工具描述,和入参json schema
		if len(params) > 0 {
			desc = str.ToString(params[0])
		}
		if len(params) > 1 {
			inputSchema = str.ToString(params[1])
		}
		err = s.addRouter(router, desc, inputSchema)
		return router.GetId(), err
	}
}

func (s *McpServer) RemoveRouter(routerId string, params ...interface{}) error {
	routerId = strings.TrimSpace(routerId)
	s.DeleteTools(routerId)
	return nil
}
func (s *McpServer) Printf(format string, v ...interface{}) {
	if s.RuleConfig.Logger != nil {
		s.RuleConfig.Logger.Printf(format, v...)
	}
}

func (s *McpServer) Start() error {
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

// Destroy 销毁
func (s *McpServer) Destroy() {
	_ = s.Close()
}

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
	if desc == "" {
		desc = router.FromToString()
	}
	var def types.RuleChain
	if s.ruleChain != nil {
		def = *s.ruleChain
	}
	if s.ruleChain != nil && def.RuleChain.ID == chainId {
		s.AddToolsFromChain(chainId, startNodeId, def, router.FromToString(), desc, inputSchema, router)
	} else if engine, ok := router.GetRuleGo(nil).Get(chainId); !ok {
		return fmt.Errorf("chainId:%s not found", chainId)
	} else {
		s.AddToolsFromChain(chainId, startNodeId, engine.Definition(), router.FromToString(), desc, inputSchema, router)
	}
	return nil
}

func (s *McpServer) handler(url string) endpoint.Router {
	return endpointImpl.NewRouter().From(url).Process(func(router endpoint.Router, exchange *endpoint.Exchange) bool {
		inMsg := exchange.In.(*rest.RequestMessage)
		s.SSEServer().ServeHTTP(inMsg.Response(), inMsg.Request())
		return true
	}).End()
}

func (s *McpServer) handlerFromPool(url string) endpoint.Router {
	return endpointImpl.NewRouter().From(url).Process(func(router endpoint.Router, exchange *endpoint.Exchange) bool {
		inMsg := exchange.In.(*rest.RequestMessage)
		id := inMsg.Metadata.GetValue(KeyId)
		if sseServer, ok := s.GetSSEServerPool().Get(id); ok {
			sseServer.ServeHTTP(inMsg.Response(), inMsg.Request())
		} else {
			s.SSEServer().ServeHTTP(inMsg.Response(), inMsg.Request())
		}
		return true
	}).End()
}

func (s *McpServer) MCPServer() *server.MCPServer {
	if s.mcpServer == nil {
		s.mcpServer = s.NewMCPServer()
	}
	return s.mcpServer
}

func (s *McpServer) SSEServer() *server.SSEServer {
	if s.sseServer == nil {
		s.sseServer = s.NewSSEServer(server.WithBasePath(s.baseFullPath), server.WithHTTPServer(s.Rest.GetServer()))
	}
	return s.sseServer
}

func (s *McpServer) DeleteTools(names ...string) {
	if s.mcpServer != nil {
		s.mcpServer.DeleteTools(names...)
	}
}
func (s *McpServer) GetSSEServerPool() *SSEServerPool {
	if s.SSEServerPool == nil {
		s.SSEServerPool = DefaultSSEServerPool
	}
	return s.SSEServerPool
}

func (s *McpServer) NewMCPServer() *server.MCPServer {
	mcpServer := server.NewMCPServer(
		s.Config.Name,    // MCP服务器名称
		s.Config.Version, // MCP服务器版本
	)
	return mcpServer
}

func (s *McpServer) NewSSEServer(opts ...server.SSEOption) *server.SSEServer {
	return server.NewSSEServer(s.mcpServer, opts...)
}

func (s *McpServer) AddToolsFromChain(chainId, startNodeId string, def types.RuleChain, toolName, toolDesc string, inputSchema string, router endpoint.Router) {
	if toolDesc == "" {
		toolDesc = def.RuleChain.Name
		if v := str.ToString(def.RuleChain.AdditionalInfo["description"]); v != "" {
			toolDesc = v
		}
	}

	if toolDesc != "" {
		var tool mcp.Tool
		if inputSchema != "" {
			tool = mcp.NewToolWithRawSchema(toolName, toolDesc, []byte(inputSchema))
		} else if inputSchemaMap, ok := def.RuleChain.AdditionalInfo["inputSchema"]; ok {
			if schema, err := json.Marshal(inputSchemaMap); err == nil {
				tool = mcp.NewToolWithRawSchema(toolName, toolDesc, schema)
			}
		} else {
			var vars []string
			//自动从所有节点中获取所有变量
			if startNodeId == "" {
				vars = dsl.ParseVars(types.MsgKey, def)
			} else {
				//如果是子规则链节点，从子规则链节点定义中获取所有变量
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
				tool = mcp.NewTool(chainId, toolOptions...)
			} else {
				tool = mcp.NewTool(chainId, mcp.WithDescription(toolDesc), mcp.WithObject(KeyInMessage, mcp.Description("input message")))
			}

		}
		// 为工具添加处理器
		s.MCPServer().AddTool(tool, s.ruleChainToolHandler(chainId, startNodeId, router.GetRuleGo(nil)))
	}
}

func (s *McpServer) ruleChainToolHandler(chainId, startNodeId string, pool types.RuleEnginePool) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ruleEngine, ok := pool.Get(chainId)
		if !ok {
			return nil, errors.New("rule chain not found")
		} else {
			var msg string
			if params, ok := request.Params.Arguments[KeyInMessage]; ok {
				msg = str.ToString(params)
			} else if v, err := json.Marshal(request.Params.Arguments); err != nil {
				return nil, err
			} else {
				msg = string(v)
			}

			wg := sync.WaitGroup{}
			wg.Add(1)
			var result string
			var resultErr error
			var opts []types.RuleContextOption
			if startNodeId != "" {
				opts = append(opts, types.WithStartNode(startNodeId))
			}
			opts = append(opts, types.WithOnEnd(func(ctx types.RuleContext, msg types.RuleMsg, err error, relationType string) {
				result = msg.GetData()
				resultErr = err
				wg.Done()
			}))
			ruleEngine.OnMsgAndWait(types.NewMsgWithJsonData(msg), opts...)
			wg.Wait()
			return mcp.NewToolResultText(result), resultErr
		}

	}
}
