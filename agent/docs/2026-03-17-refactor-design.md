# Agent 模块重构方案

## 1. 概述

### 1.1 背景
当前 `ai/agent` 模块的核心文件 `react_agent_node.go` 已达 1128 行，承担了过多职责，代码可维护性和可测试性较差。

### 1.2 目标
- **拆分大文件**: 将 `react_agent_node.go` 拆分为多个职责清晰的文件
- **架构优化**: 重新设计模块结构，提取公共组件和接口
- **代码清理**: 消除重复代码，统一风格
- **提升可测试性**: 通过接口解耦，便于单元测试

### 1.3 约束
- API 兼容性：无限制（可以自由修改公开 API）
- 文件组织：单包多文件（保持 `ai/agent` 包）

---

## 2. 当前结构分析

### 2.1 现有文件
| 文件 | 行数 | 职责 |
|------|------|------|
| react_agent_node.go | 1128 | 核心Agent实现、工具包装、SSE回调、切面执行 |
| factory.go | 151 | ChatModel/Tools/Agent 工厂函数 |
| transform.go | 297 | 消息转换、输出处理 |
| retry_model.go | 227 | 重试包装器 |
| rulego_tool.go | 124 | RuleGo工具实现 |
| tool_agent.go | 102 | ToolAgent实现 |
| config.go | 24 | 配置结构体 |

### 2.2 主要问题

1. **职责混乱**: `react_agent_node.go` 包含节点实现、工具包装、SSE处理、切面集成等多种职责
2. **代码重复**: 默认LLM参数应用在多处重复
3. **内联类型过多**: `vizToolWrapper`、SSE相关类型应该独立
4. **可测试性差**: 大量逻辑耦合，难以独立测试

---

## 3. 重构后结构

### 3.1 文件组织
```
ai/agent/
├── agent.go              # 核心接口定义、基础类型 (~100行)
├── config.go             # 配置结构体（扩展，~80行）
├── react_agent.go        # ReactAgentNode 核心实现 (~300行)
├── tool_wrapper.go       # VisualToolWrapper 工具包装器 (~150行)
├── tool_rulego.go        # RuleGo 工具实现（重命名，~120行）
├── tool_agent.go         # ToolAgent 实现（保留，~100行）
├── factory.go            # 统一工厂函数（重构，~200行）
├── retry_model.go        # 重试包装器（保留，~230行）
├── transform.go          # 消息转换（保留，~300行）
├── sse.go                # SSE 回调和事件处理 (~150行)
├── aspect.go             # 切面集成逻辑 (~200行)
└── utils.go              # 通用工具函数 (~50行)
```

### 3.2 依赖关系
```
                    ┌─────────────┐
                    │  agent.go   │ (接口定义)
                    └──────┬──────┘
                           │
           ┌───────────────┼───────────────┐
           │               │               │
           ▼               ▼               ▼
    ┌────────────┐  ┌────────────┐  ┌────────────┐
    │react_agent │  │tool_wrapper│  │  factory   │
    └─────┬──────┘  └─────┬──────┘  └─────┬──────┘
          │               │               │
          ├───────────────┼───────────────┤
          │               │               │
          ▼               ▼               ▼
    ┌────────────┐  ┌────────────┐  ┌────────────┐
    │   sse.go   │  │  aspect.go │  │ transform  │
    └────────────┘  └────────────┘  └────────────┘
```

---

## 4. 详细设计

### 4.1 agent.go - 核心接口

```go
package agent

// Agent 核心智能体接口
type Agent interface {
    Name() string
    Description() string
    Tools() []*schema.ToolInfo
}

// AgentExecutor 智能体执行器接口
type AgentExecutor interface {
    Agent
    Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
    Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error)
}

// ToolWrapper 工具包装器接口
type ToolWrapper interface {
    Wrap(tool tool.InvokableTool, opts ToolWrapOptions) tool.InvokableTool
}

// ToolWrapOptions 工具包装选项
type ToolWrapOptions struct {
    Name             string
    AgentId          string
    AgentName        string
    ToolType         aspect.ToolType
    TargetId         string
    AspectManager    *aspect.AspectManager
    MaxStep          int
    Logger           types.Logger
    MetricsCollector *token.MetricsCollector
}

// 公开常量
const (
    DefaultMaxStep      = 50
    MaxToolOutputLength = 50000
    DefaultToolTimeout  = 120 // seconds
)
```

### 4.2 config.go - 配置结构体

```go
package agent

// ChatAgentConfig 扩展的 Agent 配置
type ChatAgentConfig struct {
    config.LLMConfig `json:",squash"`
    MaxStep          int                       `json:"maxStep"`
    SystemPrompt     string                    `json:"systemPrompt"`
    Tools            []config.Tool             `json:"tools"`
    Messages         []PresetMessage           `json:"messages"` // 预设消息
}

// PresetMessage 预设消息
type PresetMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

// ReactAgentNodeConfig React Agent 节点配置
type ReactAgentNodeConfig struct {
    ChatAgentConfig `json:",squash"`
}

// SubAgentConfig 子代理配置
type SubAgentConfig struct {
    Name        string              `json:"name"`
    Description string              `json:"description"`
    Type        string              `json:"type"`
    TargetId    string              `json:"targetId"`
    Config      types.Configuration `json:"config"`
}
```

### 4.3 react_agent.go - 核心节点

```go
package agent

// ReactAgentNode 实现 ReAct 模式的智能体节点
type ReactAgentNode struct {
    config             *ReactAgentNodeConfig
    agent              *react.Agent
    tools              []*schema.ToolInfo
    name               string
    description        string
    systemPromptTmpl   el.Template
    presetMsgTmpls     []ChatMessageTemplate
    hasVar             bool

    // 可组合组件
    aspectExecutor     *AgentAspectExecutor
    toolWrapper        ToolWrapper
    sseHandler         *SSEHandler
    logger             types.Logger
    tokenTracker       *token.TokenTracker
    metricsCollector   *token.MetricsCollector
    ruleEnginePool     types.RuleEnginePool
}

func (x *ReactAgentNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
    // 1. 解析配置
    if err := x.parseConfig(configuration); err != nil {
        return err
    }

    // 2. 创建聊天模型
    chatModel, err := CreateChatModel(x.config.LLMConfig, ModelOptions{
        Logger:     ruleConfig.Logger,
        WrapRetry:  true,
        MaxRetries: x.config.MaxRetries,
    })
    if err != nil {
        return err
    }

    // 3. 创建工具
    tools, toolInfos, err := CreateTools(x.config.Tools, ToolOptions{
        RuleConfig:     ruleConfig,
        RuleEnginePool: x.ruleEnginePool,
        WrapVisual:     true,
        WrapOptions:    x.buildToolWrapOptions(),
    })
    if err != nil {
        return err
    }

    // 4. 创建 React Agent
    x.agent, err = CreateReactAgent(context.Background(), chatModel, AgentOptions{
        MaxStep:     x.config.MaxStep,
        ToolsConfig: buildToolsConfig(tools),
    })
    if err != nil {
        return err
    }

    // 5. 初始化组件
    x.initComponents(ruleConfig)

    return nil
}

func (x *ReactAgentNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
    // 1. 转换输入
    input, err := ConvertRuleMsgToAgentInput(...)
    if err != nil {
        ctx.TellFailure(msg, err)
        return
    }

    // 2. 构建执行上下文
    runCtx := x.buildRunContext(ctx, msg)

    // 3. 根据模式执行
    if x.isStreamMode(msg) {
        x.executeStream(ctx, msg, runCtx, input)
    } else {
        x.executeSync(ctx, msg, runCtx, input)
    }
}
```

### 4.4 tool_wrapper.go - 工具包装器

```go
package agent

// VisualToolWrapper 可视化工具包装器
type VisualToolWrapper struct {
    base             tool.InvokableTool
    name             string
    agentId          string
    agentName        string
    toolType         aspect.ToolType
    targetId         string
    aspectManager    *aspect.AspectManager
    maxStep          int
    logger           types.Logger
    callCounter      int32
    metricsCollector *token.MetricsCollector
}

func NewVisualToolWrapper(base tool.InvokableTool, opts ToolWrapOptions) *VisualToolWrapper {
    return &VisualToolWrapper{
        base:             base,
        name:             opts.Name,
        agentId:          opts.AgentId,
        agentName:        opts.AgentName,
        toolType:         opts.ToolType,
        targetId:         opts.TargetId,
        aspectManager:    opts.AspectManager,
        maxStep:          opts.MaxStep,
        logger:           opts.Logger,
        metricsCollector: opts.MetricsCollector,
    }
}

func (w *VisualToolWrapper) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
    callId := w.generateCallId()
    startTime := time.Now()

    // 1. 发送开始事件
    w.emitStart(ctx, callId, args)

    // 2. 切面前置
    callInfo := w.beforeAspect(ctx, callId, args, startTime)

    // 3. 执行
    result, err := w.base.InvokableRun(ctx, args, opts...)

    // 4. 记录指标
    w.recordMetrics(startTime, args, result, err)

    // 5. 切面后置
    w.afterAspect(ctx, callInfo, result, err, startTime)

    // 6. 发送结果事件
    w.emitResult(ctx, callId, result, err)

    return truncateResult(result), nil
}
```

### 4.5 sse.go - SSE 事件处理

```go
package agent

type SSEEventType string

const (
    SSEEventToolStart  SSEEventType = "tool_start"
    SSEEventToolResult SSEEventType = "tool_result"
    SSEEventToolError  SSEEventType = "tool_error"
)

type SSEHandler struct {
    ctx     types.RuleContext
    msg     types.RuleMsg
    enabled bool
}

func NewSSEHandler(ctx types.RuleContext, msg types.RuleMsg) *SSEHandler {
    return &SSEHandler{
        ctx:     ctx,
        msg:     msg,
        enabled: msg.Metadata.GetValue(config.KeyStream) == config.ValueTrue,
    }
}

func (h *SSEHandler) Callback() SSECallback {
    if !h.enabled {
        return nil
    }
    return h.sendEvent
}

func (h *SSEHandler) sendEvent(toolCallId, toolName, eventType, data string, index int) {
    eventData := h.buildEventData(toolCallId, toolName, eventType, data, index)
    chunkMsg := h.msg.Copy()
    chunkMsg.SetData(string(eventData))
    chunkMsg.DataType = types.TEXT
    chunkMsg.Metadata.PutValue(config.KeyChunk, config.ValueTrue)
    chunkMsg.Metadata.PutValue(config.KeyToolCall, config.ValueTrue)
    h.ctx.TellNext(chunkMsg, types.Stream)
}

// Context 辅助函数
type sseCallbackKey struct{}

func GetSSECallback(ctx context.Context) SSECallback { ... }
func WithSSECallback(ctx context.Context, cb SSECallback) context.Context { ... }

type SSECallback func(toolCallId, toolName, eventType, data string, index int)
```

### 4.6 aspect.go - 切面集成

```go
package agent

type AgentAspectExecutor struct {
    manager *aspect.AspectManager
    logger  types.Logger
}

func NewAgentAspectExecutor(logger types.Logger) *AgentAspectExecutor {
    exec := &AgentAspectExecutor{
        manager: aspect.NewAspectManager(),
        logger:  logger,
    }
    for _, a := range aspect.GetGlobalAspects() {
        exec.manager.Register(a.New())
    }
    return exec
}

type ExecuteOptions struct {
    ChainId    string
    AgentName  string
    Msg        types.RuleMsg
    SessionKey string
}

func (e *AgentAspectExecutor) ExecuteSync(
    ctx context.Context,
    opts ExecuteOptions,
    input *aspect.AgentInput,
    adkInput *adk.AgentInput,
    executor func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error),
) (*aspect.AgentOutput, error) {
    point := e.buildPoint(opts)
    startTime := time.Now()

    // Start -> Before -> Around -> After -> Completed
    input, err := e.manager.ExecuteStart(ctx, point, input)
    if err != nil {
        e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err})
        return nil, err
    }

    input, err = e.manager.ExecuteBefore(ctx, point, input)
    if err != nil {
        e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err})
        return nil, err
    }

    messages := e.mergeMessages(input, adkInput)

    output, err := e.manager.ExecuteAround(ctx, point, input, func(ctx context.Context, in *aspect.AgentInput) (*aspect.AgentOutput, error) {
        msg, err := executor(ctx, messages)
        if err != nil {
            return nil, err
        }
        return e.buildOutput(msg, in, startTime), nil
    })

    if err != nil {
        e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err})
        return nil, err
    }

    output, _ = e.manager.ExecuteAfter(ctx, point, output)
    output.IsSuccess = true
    e.manager.ExecuteCompleted(ctx, point, output)

    return output, nil
}

func (e *AgentAspectExecutor) ExecuteStream(...) (*aspect.AgentOutput, error) { ... }
```

### 4.7 factory.go - 统一工厂

```go
package agent

// ModelOptions 模型创建选项
type ModelOptions struct {
    Logger     types.Logger
    WrapRetry  bool
    MaxRetries int
}

func CreateChatModel(cfg config.LLMConfig, opts ...ModelOptions) (model.ToolCallingChatModel, error) {
    cfg = applyDefaultParams(cfg)
    baseModel, err := createOpenAIModel(cfg)
    if err != nil {
        return nil, err
    }

    var chatModel model.ToolCallingChatModel = baseModel
    if len(opts) > 0 && opts[0].WrapRetry && opts[0].MaxRetries > 0 {
        chatModel = NewRetryChatModelWrapper(chatModel, opts[0].MaxRetries, opts[0].Logger)
    }
    return chatModel, nil
}

// ToolOptions 工具创建选项
type ToolOptions struct {
    RuleConfig     types.Config
    RuleEnginePool types.RuleEnginePool
    WrapVisual     bool
    WrapOptions    ToolWrapOptions
}

func CreateTools(toolsConfig []config.Tool, opts ToolOptions) ([]tool.BaseTool, []*schema.ToolInfo, error) {
    var tools []tool.BaseTool
    var toolInfos []*schema.ToolInfo

    for _, cfg := range toolsConfig {
        t, info, err := CreateTool(cfg, opts)
        if err != nil {
            return nil, nil, err
        }
        tools = append(tools, t)
        toolInfos = append(toolInfos, info)
    }
    return tools, toolInfos, nil
}

func CreateTool(cfg config.Tool, opts ToolOptions) (tool.BaseTool, *schema.ToolInfo, error) {
    var toolInstance tool.BaseTool
    var err error

    switch cfg.Type {
    case config.ToolTypeAgent:
        cfg = fillAgentToolInfo(cfg, opts.RuleEnginePool)
        toolInstance = NewRuleGoTool(cfg)
    case config.ToolTypeRuleChain:
        toolInstance = NewRuleGoTool(cfg)
    case config.ToolTypeBuiltin:
        toolInstance, err = createBuiltinTool(cfg, opts.RuleConfig)
        if err != nil {
            return nil, nil, err
        }
    default:
        return nil, nil, fmt.Errorf("unsupported tool type: %s", cfg.Type)
    }

    toolInfo, err := toolInstance.Info(context.Background())
    if err != nil {
        return nil, nil, err
    }

    if opts.WrapVisual {
        if invokable, ok := toolInstance.(tool.InvokableTool); ok {
            opts.WrapOptions.Name = cfg.Name
            toolInstance = NewVisualToolWrapper(invokable, opts.WrapOptions)
        }
    }

    return toolInstance, toolInfo, nil
}

// AgentOptions Agent 创建选项
type AgentOptions struct {
    Name         string
    Description  string
    SystemPrompt string
    MaxStep      int
    ToolsConfig  adk.ToolsConfig
}

func CreateReactAgent(ctx context.Context, chatModel model.ToolCallingChatModel, opts AgentOptions) (*react.Agent, error) {
    maxStep := opts.MaxStep
    if maxStep <= 0 {
        maxStep = DefaultMaxStep
    }

    cfg := &react.AgentConfig{
        ToolCallingModel:     chatModel,
        ToolsConfig:          opts.ToolsConfig,
        MaxStep:              maxStep,
        StreamToolCallChecker: createStreamToolCallChecker(),
    }

    return react.NewAgent(ctx, cfg)
}
```

### 4.8 utils.go - 工具函数

```go
package agent

func generateShortID() string {
    const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
    b := make([]byte, 6)
    for i := range b {
        b[i] = charset[rand.Intn(len(charset))]
    }
    return string(b)
}

func maskAPIKey(key string) string {
    if len(key) <= 8 {
        return "****"
    }
    return key[:4] + "****" + key[len(key)-4:]
}

func truncateResult(result string) string {
    if len(result) > MaxToolOutputLength {
        return result[:MaxToolOutputLength] + "...(truncated due to length)"
    }
    return result
}

func applyDefaultParams(cfg config.LLMConfig) config.LLMConfig {
    if cfg.Params.Temperature == 0 {
        cfg.Params.Temperature = config.DefaultTemperature
    }
    if cfg.Params.TopP == 0 {
        cfg.Params.TopP = config.DefaultTopP
    }
    if cfg.Params.FrequencyPenalty == 0 {
        cfg.Params.FrequencyPenalty = config.DefaultFrequencyPenalty
    }
    if cfg.Params.PresencePenalty == 0 {
        cfg.Params.PresencePenalty = config.DefaultPresencePenalty
    }
    return cfg
}
```

---

## 5. 重构步骤

### 5.1 阶段一：提取独立模块（低风险）
1. 创建 `agent.go`，定义核心接口
2. 创建 `sse.go`，提取 SSE 相关逻辑
3. 创建 `utils.go`，提取工具函数
4. 更新 `react_agent_node.go`，引用新模块

### 5.2 阶段二：拆分核心逻辑（中风险）
1. 创建 `tool_wrapper.go`，提取 `vizToolWrapper`
2. 创建 `aspect.go`，提取切面执行逻辑
3. 重构 `react_agent_node.go`，使用新组件

### 5.3 阶段三：统一工厂（低风险）
1. 重构 `factory.go`，统一创建逻辑
2. 更新所有引用

### 5.4 阶段四：测试和清理
1. 补充单元测试
2. 清理冗余代码
3. 更新文档

---

## 6. 风险和缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 重构引入 Bug | 高 | 分阶段重构，每阶段运行测试 |
| API 变更影响下游 | 中 | 保持主要函数签名，仅调整内部实现 |
| 性能回退 | 低 | 重构后进行性能对比测试 |

---

## 7. 预期收益

1. **可维护性提升**: 每个文件职责清晰，代码量控制在 300 行以内
2. **可测试性提升**: 通过接口解耦，可独立测试各组件
3. **可扩展性提升**: 新增 Agent 类型只需实现接口
4. **代码复用**: 工具包装、切面执行等逻辑可复用

---

## 8. 附录

### 8.1 公开 API 变更清单

| 原 API | 新 API | 变更说明 |
|--------|--------|----------|
| NewRuleGoTool | NewRuleGoTool | 保持不变 |
| CreateChatModel | CreateChatModel | 新增 Options 参数 |
| CreateTools | CreateTools | 新增 Options 参数 |
| NewToolAgent | NewToolAgent | 保持不变 |
| NewRetryChatModelWrapper | NewRetryChatModelWrapper | 保持不变 |

### 8.2 文件变更清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | agent.go | 核心接口定义 |
| 新增 | sse.go | SSE 事件处理 |
| 新增 | aspect.go | 切面集成 |
| 新增 | tool_wrapper.go | 工具包装器 |
| 新增 | utils.go | 工具函数 |
| 重构 | react_agent_node.go -> react_agent.go | 拆分精简 |
| 重构 | factory.go | 统一工厂 |
| 重命名 | rulego_tool.go -> tool_rulego.go | 命名规范化 |
| 保留 | transform.go | 消息转换 |
| 保留 | retry_model.go | 重试包装器 |
| 保留 | tool_agent.go | ToolAgent |
| 保留 | config.go | 配置结构体 |
