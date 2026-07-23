# Agent Module refactoring plan

## 1. Overview

### 1.1 Background
Currently, the core file `react_agent_node.go` of the `ai/agent` module has reached 1,128 lines, taking on too many responsibilities, resulting in poor maintainability and testability.

### 1.2 Goals
- **Split Large Files**: Split `react_agent_node.go` into multiple documents with clear responsibilities
- **Architecture Optimization**: Redesign module structures to extract common components and interfaces
- **Code Cleanup**: Eliminate duplicate code and unify style
- **Improved testability**: Decoupling via interfaces facilitates unit testing

### 1.3 Constraints
- API Compatibility: Unlimited (freely modifies public API)
- File organization: multiple files in a single package (keep `ai/agent` packages)

---

## 2. Current structural analysis

### 2.1 Existing Files
| File | Number of lines | Responsibilities |
|------|------|------|
| react_agent_node.go | 1128 | Core Agent implementation, tool wrapping, SSE callbacks, and aspect execution |
| factory.go | 151 | ChatModel/Tools/Agent Factory function |
| transform.go | 297 | Message conversion and output processing |
| retry_model.go | 227 | Retest the wrapper |
| rulego_tool.go | 124 | RuleGo Tool implementation |
| tool_agent.go | 102 | ToolAgent Implement |
| config.go | 24 | Configure the structure |

### 2.2 Main Issues

1. **Confusion of Responsibilities**: `react_agent_node.go` includes node implementation, tool packaging, SSE handling, and face-to-face integration
2. **Code Duplicate**: By default, LLM parameters are applied repeatedly in multiple places
3. **Too many inline types**: `vizToolWrapper` and SSE related types should be independent
4. **Poor testability**: Extensive logical coupling, making independent testing difficult

---

## 3. Reconstructed post-structure

### 3.1 File Organization
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

### 3.2 Dependencies
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

## 4. Detailed design

### 4.1 agent.go - Core Interface

```go
package agent

// Agent Core agent interface
type Agent interface {
    Name() string
    Description() string
    Tools() []*schema.ToolInfo
}

// AgentExecutor agent executor interface
type AgentExecutor interface {
    Agent
    Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
    Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error)
}

// ToolWrapper Tool wrapper interface
type ToolWrapper interface {
    Wrap(tool tool.InvokableTool, opts ToolWrapOptions) tool.InvokableTool
}

// ToolWrapOptions tool wrapper options
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

// Public constants
const (
    DefaultMaxStep      = 50
    MaxToolOutputLength = 50000
    DefaultToolTimeout  = 120 // seconds
)
```

### 4.2 config.go - Configure Structures

```go
package agent

// ChatAgentConfig Extended Agent configuration
type ChatAgentConfig struct {
    config.LLMConfig `json:",squash"`
    MaxStep          int                       `json:"maxStep"`
    SystemPrompt     string                    `json:"systemPrompt"`
    Tools            []config.Tool             `json:"tools"`
    Messages         []PresetMessage           `json:"messages"` // Preset messages
}

// PresetMessage Preset messages
type PresetMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

// ReactAgentNodeConfig React Agent Node configuration
type ReactAgentNodeConfig struct {
    ChatAgentConfig `json:",squash"`
}

// SubAgentConfig Sub-agent configuration
type SubAgentConfig struct {
    Name        string              `json:"name"`
    Description string              `json:"description"`
    Type        string              `json:"type"`
    TargetId    string              `json:"targetId"`
    Config      types.Configuration `json:"config"`
}
```

### 4.3 react_agent.go - Core Nodes

```go
package agent

// ReactAgentNode Agent nodes that implement ReAct patterns
type ReactAgentNode struct {
    config             *ReactAgentNodeConfig
    agent              *react.Agent
    tools              []*schema.ToolInfo
    name               string
    description        string
    systemPromptTmpl   el.Template
    presetMsgTmpls     []ChatMessageTemplate
    hasVar             bool

    // Modular components
    aspectExecutor     *AgentAspectExecutor
    toolWrapper        ToolWrapper
    sseHandler         *SSEHandler
    logger             types.Logger
    tokenTracker       *token.TokenTracker
    metricsCollector   *token.MetricsCollector
    ruleEnginePool     types.RuleEnginePool
}

func (x *ReactAgentNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
    // 1. Analyze configuration
    if err := x.parseConfig(configuration); err != nil {
        return err
    }

    // 2. Create a chat model
    chatModel, err := CreateChatModel(x.config.LLMConfig, ModelOptions{
        Logger:     ruleConfig.Logger,
        WrapRetry:  true,
        MaxRetries: x.config.MaxRetries,
    })
    if err != nil {
        return err
    }

    // 3. Create tools
    tools, toolInfos, err := CreateTools(x.config.Tools, ToolOptions{
        RuleConfig:     ruleConfig,
        RuleEnginePool: x.ruleEnginePool,
        WrapVisual:     true,
        WrapOptions:    x.buildToolWrapOptions(),
    })
    if err != nil {
        return err
    }

    // 4. Create React Agent
    x.agent, err = CreateReactAgent(context.Background(), chatModel, AgentOptions{
        MaxStep:     x.config.MaxStep,
        ToolsConfig: buildToolsConfig(tools),
    })
    if err != nil {
        return err
    }

    // 5. Initialize the component
    x.initComponents(ruleConfig)

    return nil
}

func (x *ReactAgentNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
    // 1. Convert input
    input, err := ConvertRuleMsgToAgentInput(...)
    if err != nil {
        ctx.TellFailure(msg, err)
        return
    }

    // 2. Build execution context
    runCtx := x.buildRunContext(ctx, msg)

    // 3. Execute according to the pattern
    if x.isStreamMode(msg) {
        x.executeStream(ctx, msg, runCtx, input)
    } else {
        x.executeSync(ctx, msg, runCtx, input)
    }
}
```

### 4.4 tool_wrapper.go - Tool Wrapper

```go
package agent

// VisualToolWrapper Visualize tool wrappers
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

    // 1. Send the start event
    w.emitStart(ctx, callId, args)

    // 2. Cutting the front
    callInfo := w.beforeAspect(ctx, callId, args, startTime)

    // 3. Execution
    result, err := w.base.InvokableRun(ctx, args, opts...)

    // 4. Record indicators
    w.recordMetrics(startTime, args, result, err)

    // 5. Post-cutting face
    w.afterAspect(ctx, callInfo, result, err, startTime)

    // 6. Send the result event
    w.emitResult(ctx, callId, result, err)

    return truncateResult(result), nil
}
```

### 4.5 sse.go - SSE Event Handling

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

// Context Auxiliary functions
type sseCallbackKey struct{}

func GetSSECallback(ctx context.Context) SSECallback { ... }
func WithSSECallback(ctx context.Context, cb SSECallback) context.Context { ... }

type SSECallback func(toolCallId, toolName, eventType, data string, index int)
```

### 4.6 aspect.go - Facet Integration

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

### 4.7 factory.go - Unified Factory

```go
package agent

// ModelOptions Model creation option
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

// ToolOptions Tools to create options
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

// AgentOptions Agent Create options
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

### 4.8 utils.go - Utility Functions

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

## 5. Refactoring steps

### 5.1 Stage One: Extracting Independent Modules (Low Risk)
1. Create a `agent.go` and define the core interface
2. Create a `sse.go` and extract SSE-related logic
3. Create a `utils.go` to extract the utility function
4. Update `react_agent_node.go` and reference new modules

### 5.2 Stage Two: Breaking the Core Logic (Medium Risk)
1. Create `tool_wrapper.go` and extract `vizToolWrapper`
2. Create `aspect.go` to extract the facet execution logic
3. Refactoring `react_agent_node.go` with new components

### 5.3 Phase Three: Unifying Factories (Low Risk)
1. Refactor `factory.go` to unify the creation logic
2. Update all references

### 5.4 Phase Four: Testing and Cleanup
1. Supplement unit tests
2. Clean up redundant code
3. Update the documentation

---

## 6. Risk and mitigation

| Risks | Impact | Mitigation measures |
|------|------|----------|
| Refactoring introduces Bug | High | Phased refactoring, each stage running tests |
| API Change affects downstream | Medium | Keep the main function signature and only adjust the internal implementation |
| Performance rollback | Low | Performance comparison tests after refactoring |

---

## 7. Expected returns

1. **Improved maintainability**: Each document has clear responsibilities, and code is kept under 300 lines
2. **Improved testability**: Through interface decoupling, each component can be tested independently
3. **Scalability Improvement**: Added Agent types that only require implementing interfaces
4. **Code Reuse**: Tool wrappers, face-cut execution, and other logic can be reused

---

## 8. Appendix

### 8.1 Disclosure of API Change List

| Original API | New API | Change Notes |
|--------|--------|----------|
| NewRuleGoTool | NewRuleGoTool | Remain unchanged |
| CreateChatModel | CreateChatModel | Added Options parameter |
| CreateTools | CreateTools | Added Options parameter |
| NewToolAgent | NewToolAgent | Remain unchanged |
| NewRetryChatModelWrapper | NewRetryChatModelWrapper | Remain unchanged |

### 8.2 File Change Checklist

| Operation | File | Note |
|------|------|------|
| Add | agent.go | Core interface definition |
| Add | sse.go | SSE Event Handling |
| Add | aspect.go | Facet integration |
| Add | tool_wrapper.go | Tool Wrapper |
| Add | utils.go | Utility function |
| Refactor | react_agent_node.go -> react_agent.go | Break down and simplify |
| Refactor | factory.go | Unified Factory |
| Rename | rulego_tool.go -> tool_rulego.go | Naming Standardization |
| Retain | transform.go | Message conversion |
| Retain | retry_model.go | Retest the wrapper |
| Retain | tool_agent.go | ToolAgent |
| Retain | config.go | Configure the structure |
