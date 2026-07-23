# Unified Agent Model Design Scheme

## 1. Background analysis

### 1.1 Comparison of the Three Existing Models

| Features | ReactAgent | Supervisor | DeepAgent |
|------|------------|------------|-----------|
| **Core Mechanism** | ReAct Loop | Central coordination + sub-agents | Deep task orchestration |
| **Sub-agent** | ❌ None | ✅ There is | ✅ There is |
| **TODO Manage** | ❌ None | ❌ None | ✅ Built-in write_todos |
| **Parallel tool call** | ✅ Support | ❌ Sequential call | ✅ Support |
| **Decision-making Methods** | LLM Decide on the tool call | LLM Decide which sub-agent to call | LLM Decide on task breakdown and execution |

### 1.2 Analysis of Prompt Mechanism

#### ReactAgent
- **No explicit prompt**: Relies on the LLM's function-calling capability
- **Decision Mechanism**: LLM Automatically decide which tool to call based on the tool description
- **Loop Control**: Limits the number of iterations through `maxStep`

#### Supervisor
- **No explicit prompt**: Relies on the `AgentWithDeterministicTransferTo` mechanism
- **Decision Mechanism**: Supervisor Agent system prompts include descriptions of sub-agents
- **Transfer Mechanism**: After the sub-agent completes, it automatically returns to the Supervisor

#### DeepAgent
- **Rich prompts**:
  - `write_todos`: Task breakdown and progress tracking
  - `task`: Sub-agent scheduling tool
  - `baseAgentInstruction`: Basic Behavioral Guidelines
- **Decision Mechanism**: LLM Select appropriate sub-agents through `task` tool descriptions
- **Core prompt snippets**:
```go
taskToolDescription = `Launch a new agent to handle complex, multi-step tasks autonomously.
Available agent types and the tools they have access to:
{other_agents}
...`
```

### 1.3 Key Findings

1. **Prompt-driven**: All patterns rely on LLM understanding tools/agent descriptions to make decisions
2. **Toolized**: DeepAgent Package sub-agents as `task` tools
3. **Context Management**: Supervisor Use deterministic transfer control processes

---

## 2. Unified model design scheme

### 2.1 Core Philosophy

Unified three modes into a single **UnifiedAgent**, and switch between different behavior modes through configuration:

```
┌─────────────────────────────────────────────────────────┐
│                    UnifiedAgent                         │
├─────────────────────────────────────────────────────────┤
│  模式选择:                                              │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────────┐   │
│  │   React     │ │ Supervisor  │ │     Deep        │   │
│  │  (默认)     │ │  (协调模式)  │ │  (任务编排模式)  │   │
│  └─────────────┘ └─────────────┘ └─────────────────┘   │
├─────────────────────────────────────────────────────────┤
│  核心能力:                                              │
│  • 工具调用 (Tools)                                     │
│  • 子智能体调用 (SubAgents)                             │
│  • 任务管理 (TODO Management)                           │
│  • 并行执行 (Parallel Execution)                        │
└─────────────────────────────────────────────────────────┘
```

### 2.2 Configuration Structure Design

```go
// AgentMode Agent Mode
type AgentMode string

const (
    // ModeReact React Mode - Simple tool calls
    ModeReact AgentMode = "react"
    // ModeSupervisor Supervisor Mode – Multi-agent coordination
    ModeSupervisor AgentMode = "supervisor"
    // ModeDeep Deep mode – task orchestration and tracking
    ModeDeep AgentMode = "deep"
    // ModeAuto Automatic mode - automatically selects based on task complexity
    ModeAuto AgentMode = "auto"
)

// UnifiedAgentConfig Unified intelligent agent configuration
type UnifiedAgentConfig struct {
    // Basic configuration
    LLMConfig      LLMConfig   `json:"llmConfig"`
    SystemPrompt   string      `json:"systemPrompt"`
    MaxStep        int         `json:"maxStep"`

    // Mode configuration
    Mode           AgentMode   `json:"mode"`           // Operating mode

    // Tool Configuration (React Mode)
    Tools          []Tool      `json:"tools"`

    // Sub-agent configuration (Supervisor/Deep mode)
    SubAgents      []SubAgentConfig `json:"subAgents"`

    // Parallel configuration
    ParallelToolCalls    *bool `json:"parallelToolCalls"`
    ExecuteSequentially  bool  `json:"executeSequentially"`
    ParallelSubAgents    bool  `json:"parallelSubAgents"` // Whether to call the sub-agent in parallel

    // Deep Mode-specific configuration
    EnableTodoManagement bool  `json:"enableTodoManagement"` // Enable TODO management
    WithoutGeneralAgent  bool  `json:"withoutGeneralAgent"`  // Disable the use of general-purpose sub-agents

    // Automatic mode configuration
    AutoModeConfig *AutoModeConfig `json:"autoModeConfig"`
}

// AutoModeConfig Automatic mode configuration
type AutoModeConfig struct {
    // SimpleTaskThreshold Simple task threshold
    // When the number of tools < = this value and there are no child agents, use the React mode
    SimpleTaskThreshold int `json:"simpleTaskThreshold"`
    // EnableAutoTodo whether TODO management is automatically enabled
    EnableAutoTodo bool `json:"enableAutoTodo"`
}
```

### 2.3 Prompt Fusion Strategy

#### 2.3.1 Basic Prompts (Common to All Modes)

```go
const baseInstruction = `
你是一个智能助手，具备以下能力：

## 核心行为准则
- 准确理解用户需求，提供专业、客观的回答
- 使用可用工具和子智能体高效完成任务
- 对于复杂任务，进行合理分解和规划

## 工具使用策略
- 当任务需要多个独立操作时，尽可能并行调用工具
- 当操作之间有依赖关系时，按正确顺序执行
- 优先使用专业工具而非通用命令

## 专业性要求
- 提供准确的技术信息，避免过度夸张
- 保持客观中立，必要时提出不同意见
- 关注实际问题解决，而非形式化表达
`
```

#### 2.3.2 Mode-Specific Prompt Extensions

```go
// React Mode expansion
const reactExtension = `
## 工具调用模式
你可以直接调用以下工具来完成任务：
{tools_description}

当多个工具调用相互独立时，请同时调用以提高效率。
`

// Supervisor Mode expansion
const supervisorExtension = `
## 子智能体协调模式
你可以委派任务给以下专业子智能体：
{subagents_description}

作为协调者，你需要：
1. 分析任务需求，选择合适的子智能体
2. 可以同时委派多个独立任务给不同的子智能体
3. 汇总各子智能体的结果，生成最终回答
`

// Deep Mode expansion
const deepExtension = `
## 任务编排模式
你拥有任务管理能力：

### TODO 管理
使用 write_todos 工具来：
- 分解复杂任务为可执行步骤
- 跟踪任务进度（pending → in_progress → completed）
- 确保所有步骤都被完成

### 子智能体调度
使用 task 工具来：
- 委派独立任务给专业子智能体
- 可以并行调度多个子智能体
- 汇总结果并整合到主流程

{subagents_description}

### 任务执行原则
- 复杂任务先分解，再执行
- 实时更新任务状态
- 并行执行独立任务以提高效率
`
```

### 2.4 Architecture Design

```
                    ┌──────────────────────┐
                    │   UnifiedAgentNode   │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │   Mode Selector      │
                    │  (根据配置选择模式)   │
                    └──────────┬───────────┘
                               │
        ┌──────────────────────┼──────────────────────┐
        │                      │                      │
        ▼                      ▼                      ▼
┌───────────────┐    ┌─────────────────┐    ┌─────────────────┐
│ ReactExecutor │    │SupervisorExecutor│   │  DeepExecutor   │
│               │    │                 │    │                 │
│ • 工具调用    │    │ • 子智能体协调  │    │ • TODO 管理     │
│ • 并行执行    │    │ • 结果聚合      │    │ • 任务分解      │
│ • ReAct循环   │    │ • 任务委派      │    │ • 深度编排      │
└───────────────┘    └─────────────────┘    └─────────────────┘
        │                      │                      │
        └──────────────────────┼──────────────────────┘
                               │
                    ┌──────────▼───────────┐
                    │   Shared Components  │
                    │  • LLM Client        │
                    │  • Tool Executor     │
                    │  • Aspect Manager    │
                    │  • SSE Callback      │
                    └──────────────────────┘
```

### 2.5 Sub-agent Parallel Call Scheme

#### 2.5.1 Core Approach

Package sub-agents as "tools that can be called in parallel", utilizing existing `ExecuteSequentially` configurations:

```go
// SubAgentWrapper Packaging sub-agents as tools
type SubAgentWrapper struct {
    agent     adk.Agent
    name      string
    desc      string
}

// When ExecuteSequentially=false, multiple sub-agent calls are executed in parallel
```

#### 2.5.2 Prompt Guidance

```go
const parallelSubAgentPrompt = `
## 并行子智能体调用

当多个子智能体任务相互独立时，你应该同时发起多个调用：

示例：
用户: "请帮我分析这段代码的安全性和性能问题"
助手: 我会同时调用安全分析智能体和性能分析智能体来并行处理：
- 调用 security_agent 分析安全漏洞
- 调用 performance_agent 分析性能瓶颈

[同时发起两个子智能体调用]
`
```

---

## 3. Achieve the plan

### 3.1 Stage One: Unified Configuration (Low Risk)

1. Create `UnifiedAgentConfig` structures
2. Keep the three existing Node unchanged, just use the same configuration format
3. Add configuration conversion logic

### 3.2 Stage Two: Prompt Fusion (Medium Risk)

1. Extract the public prompt template
2. Implement pattern-specific prompt extensions
3. Added prompt version management

### 3.3 Phase Three: Actuator Standardization (High Risk)

1. Create a `UnifiedAgentNode` unified entry
2. Implement a mode selector
3. Unified sub-agent calling mechanism
4. Added support for parallel execution of sub-agents

### 3.4 Stage Four: Auto Mode (Enhanced Features)

1. Implement task complexity assessment
2. Automatically selects the optimal mode
3. Dynamically adjust execution strategies

---

## 4. Compatibility considerations

### 4.1 Backward Compatible

- Existing `ReactAgentNode`, `SupervisorNode`, `DeepAgentNode` remain unchanged
- `UnifiedAgentNode` as a new unified entrance
- Configuration format supports automatic conversion of old formats

### 4.2 Migration Path

```json
// Legacy Configuration (still supported)
{
  "type": "ai/agent",
  "configuration": { ... }
}

// New Configuration (Recommended)
{
  "type": "ai/agent/unified",
  "configuration": {
    "mode": "react",
    ...
  }
}
```

---

## 5. Observability and event system adjustments

### 5.1 Analysis of Existing Event System

The current system has implemented AG-UI standard event types:

| Event type | Purpose | Existing support |
|---------|------|---------|
| `RUN_STARTED/FINISHED` | Agent Operation Lifecycle | ✅ Supported |
| `STEP_STARTED/FINISHED` | Step-level tracking | ✅ Supported |
| `TOOL_CALL_START/END/RESULT` | Tool call tracking | ✅ Supported |
| `TEXT_MESSAGE_*` | Streaming text output | ✅ Supported |
| `THINKING_*` | Thought Process | ✅ Supported |
| `STATE_SNAPSHOT/DELTA` | State management | ✅ Supported |

### 5.2 Event Types to Add

#### 5.2.1 Sub-agent Calls Events

```go
// Added event types
const (
    // Sub-agent invokes events (similar to tool calls, but targeted at sub-agents)
    EventSubAgentCallStart  EventType = "SUB_AGENT_CALL_START"
    EventSubAgentCallEnd    EventType = "SUB_AGENT_CALL_END"
    EventSubAgentCallResult EventType = "SUB_AGENT_CALL_RESULT"

    // TODO State Change Events (Deep Mode)
    EventTodoCreated    EventType = "TODO_CREATED"
    EventTodoUpdated    EventType = "TODO_UPDATED"
    EventTodoCompleted  EventType = "TODO_COMPLETED"

    // Executing events in parallel
    EventParallelStart  EventType = "PARALLEL_START"   // Parallel execution begins
    EventParallelEnd    EventType = "PARALLEL_END"     // Parallel execution ended

    // Mode switching event (unified agent)
    EventModeSelected   EventType = "MODE_SELECTED"    // Mode selection
)

// SubAgentCallStartEvent The sub-agent calls the start event
type SubAgentCallStartEvent struct {
    BaseEvent
    CallId       string `json:"callId"`       // Call ID
    AgentName    string `json:"agentName"`    // Name of the sub-agent
    AgentType    string `json:"agentType"`    // Types of sub-agents
    ParentRunId  string `json:"parentRunId"`  // Father runs ID
    Input        string `json:"input"`        // Input parameters
}

// SubAgentCallResultEvent Sub-agents call result events
type SubAgentCallResultEvent struct {
    BaseEvent
    CallId    string `json:"callId"`
    AgentName string `json:"agentName"`
    Output    string `json:"output"`
    Duration  int64  `json:"duration"` // Execution time (milliseconds)
    IsError   bool   `json:"isError"`
}

// TodoStatusChangeEvent TODO State change events
type TodoStatusChangeEvent struct {
    BaseEvent
    TodoId      string `json:"todoId"`
    Content     string `json:"content"`
    ActiveForm  string `json:"activeForm"`
    OldStatus   string `json:"oldStatus"`   // pending/in_progress/completed
    NewStatus   string `json:"newStatus"`
}

// ParallelStartEvent Parallel execution of the start event
type ParallelStartEvent struct {
    BaseEvent
    ExecutionId string   `json:"executionId"`
    TaskCount   int      `json:"taskCount"`   // Number of parallel tasks
    TaskTypes   []string `json:"taskTypes"`   // List of task types
}

// ParallelEndEvent Execute the event in parallel to terminate the event
type ParallelEndEvent struct {
    BaseEvent
    ExecutionId  string `json:"executionId"`
    SuccessCount int    `json:"successCount"` // Number of successes
    FailedCount  int    `json:"failedCount"`  // Number of failures
    TotalDuration int64 `json:"totalDuration"` // Total time consumed
}
```

#### 5.2.2 EventEmitter Interface Extension

```go
// EventEmitter Expansion interface
type EventEmitter interface {
    // ... Existing methods...

    // Sub-agent invocation events (newly added)
    EmitSubAgentCallStart(callId, agentName, agentType, parentRunId, input string)
    EmitSubAgentCallEnd(callId, agentName string)
    EmitSubAgentCallResult(callId, agentName, output string, duration int64, isError bool)

    // TODO Status Change Event (Added)
    EmitTodoCreated(todoId, content, activeForm string)
    EmitTodoUpdated(todoId, content, activeForm, oldStatus, newStatus string)
    EmitTodoCompleted(todoId, content string)

    // Parallel Execution Event (New)
    EmitParallelStart(executionId string, taskCount int, taskTypes []string)
    EmitParallelEnd(executionId string, successCount, failedCount int, totalDuration int64)

    // Mode Selection Event (New)
    EmitModeSelected(mode AgentMode, reason string)
}
```

### 5.3 Section System Adjustments

#### 5.3.1 Add Faceted Interface

```go
// SubAgentCallBeforeAspect The sub-agent calls the pre-face
type SubAgentCallBeforeAspect interface {
    Aspect
    PointCut
    BeforeSubAgentCall(ctx context.Context, point *AgentPoint, call *SubAgentCallInfo) (*SubAgentCallInfo, error)
}

// SubAgentCallAfterAspect The sub-agent calls the post-cut surface
type SubAgentCallAfterAspect interface {
    Aspect
    PointCut
    AfterSubAgentCall(ctx context.Context, point *AgentPoint, call *SubAgentCallInfo, result *SubAgentCallResult) error
}

// TodoChangeAspect TODO Aspect of state change
type TodoChangeAspect interface {
    Aspect
    PointCut
    OnTodoChange(ctx context.Context, point *AgentPoint, todo *TodoInfo) error
}

// ParallelExecutionAspect Parallel execution of the face
type ParallelExecutionAspect interface {
    Aspect
    PointCut
    OnParallelStart(ctx context.Context, point *AgentPoint, execution *ParallelExecutionInfo) error
    OnParallelEnd(ctx context.Context, point *AgentPoint, execution *ParallelExecutionInfo, results []*ParallelTaskResult) error
}
```

#### 5.3.2 AgentPoint Extension

```go
// AgentPoint Expand fields
type AgentPoint struct {
    // ... Existing fields...

    // Add a new field
    SubAgentName string            // Sub-agent name (when sub-agent is called)
    SubAgentType string            // Types of sub-agents
    TodoId       string            // TODO ID (TODO When status changes)
    ParallelId   string            // Parallel execution ID (when running in parallel)
    IsParallel   bool              // Whether to execute in parallel
    Mode         AgentMode         // Agent mode
}
```

### 5.4 Visualization support

#### 5.4.1 Example of Event Stream

```
统一智能体事件流示例（Deep 模式 + 并行子智能体）：

RUN_STARTED
  ├─ MODE_SELECTED (mode=deep, reason="complex multi-step task")
  ├─ TODO_CREATED (todoId=1, content="分析需求")
  ├─ TODO_UPDATED (todoId=1, status=pending→in_progress)
  ├─ STEP_STARTED (stepName="requirement_analysis")
  │   └─ TEXT_MESSAGE_CONTENT (delta="正在分析...")
  ├─ STEP_FINISHED
  ├─ TODO_UPDATED (todoId=1, status=in_progress→completed)
  ├─ TODO_CREATED (todoId=2, content="执行安全分析")
  ├─ TODO_CREATED (todoId=3, content="执行性能分析")
  ├─ PARALLEL_START (taskCount=2)
  │   ├─ SUB_AGENT_CALL_START (agentName=security_agent)
  │   │   ├─ TOOL_CALL_START (toolName=scan)
  │   │   ├─ TOOL_CALL_RESULT
  │   │   └─ SUB_AGENT_CALL_RESULT
  │   └─ SUB_AGENT_CALL_START (agentName=performance_agent)
  │       ├─ TOOL_CALL_START (toolName=profile)
  │       ├─ TOOL_CALL_RESULT
  │       └─ SUB_AGENT_CALL_RESULT
  ├─ PARALLEL_END (successCount=2, failedCount=0)
  ├─ TODO_UPDATED (todoId=2, status=pending→completed)
  ├─ TODO_UPDATED (todoId=3, status=pending→completed)
  └─ RUN_FINISHED
```

#### 5.4.2 Frontend Display Suggestions

```
┌─────────────────────────────────────────────────────────────┐
│  统一智能体执行可视化                                        │
├─────────────────────────────────────────────────────────────┤
│  模式: Deep (自动选择)                                       │
│  耗时: 12.5s                                                │
├─────────────────────────────────────────────────────────────┤
│  TODO 列表:                                                 │
│  ✅ 分析需求 (2.1s)                                         │
│  ✅ 执行安全分析 (5.2s) [并行]                              │
│  ✅ 执行性能分析 (5.3s) [并行]                              │
│  ✅ 生成报告 (2.1s)                                         │
├─────────────────────────────────────────────────────────────┤
│  子智能体调用:                                              │
│  ┌─────────────────┐ ┌─────────────────┐                   │
│  │ security_agent  │ │ performance_agent│  ← 并行执行       │
│  │ 耗时: 5.2s      │ │ 耗时: 5.3s       │                   │
│  │ 状态: ✅ 成功    │ │ 状态: ✅ 成功     │                   │
│  └─────────────────┘ └─────────────────┘                   │
└─────────────────────────────────────────────────────────────┘
```

### 5.5 Implementing Priorities

| Priority | Event type | Reason |
|--------|---------|------|
| P0 | `SUB_AGENT_CALL_*` | Sub-agent calls are the core of the unified schema |
| P0 | `MODE_SELECTED` | You need to know the current running mode |
| P1 | `TODO_*` | Deep Mode Task Tracking |
| P1 | `PARALLEL_*` | Parallel execution visualization |
| P2 | Plane extension | Advanced observability requirements |

---

## 7. Risk assessment

| Risks | Impact | Mitigation measures |
|------|------|---------|
| Prompt word conflict | High | Layered design, mode isolation |
| Performance rollback | Medium | Keep the original implementation, gradually migrate |
| Configuration complexity | Medium | Provides default values and automatic mode |
| Sub-agents running in parallel causes state issues | High | Independent context, result isolation |

---

## 8. Summary

This solution unifies the three agent models through the following methods:

1. **Unified Configuration**: One configuration supports all modes
2. **Prompt Fusion**: Layered prompt design, with specific model expansion
3. **Actuator Abstraction**: Unified entry point, mode selector
4. **Parallel Enhancement**: Supports parallel calls by sub-agents

Core Advantages:
- Simplified user selection (automatic mode)
- Retain flexibility (manual mode selection)
- Enhanced capabilities (parallel sub-agents)
- Smooth migration (backward compatible)
