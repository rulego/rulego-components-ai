# 统一智能体模式设计方案

## 1. 背景分析

### 1.1 现有三种模式对比

| 特性 | ReactAgent | Supervisor | DeepAgent |
|------|------------|------------|-----------|
| **核心机制** | ReAct 循环 | 中央协调 + 子智能体 | 深度任务编排 |
| **子智能体** | ❌ 无 | ✅ 有 | ✅ 有 |
| **TODO 管理** | ❌ 无 | ❌ 无 | ✅ 内置 write_todos |
| **并行工具调用** | ✅ 支持 | ❌ 顺序调用 | ✅ 支持 |
| **决策方式** | LLM 决定工具调用 | LLM 决定调用哪个子智能体 | LLM 决定任务分解和执行 |

### 1.2 提示词机制分析

#### ReactAgent
- **无显式提示词**：依赖 LLM 的 function calling 能力
- **决策机制**：LLM 根据工具描述自动决定调用哪个工具
- **循环控制**：通过 `maxStep` 限制迭代次数

#### Supervisor
- **无显式提示词**：依赖 `AgentWithDeterministicTransferTo` 机制
- **决策机制**：Supervisor Agent 的系统提示词中包含子智能体描述
- **转移机制**：子智能体完成后自动返回 Supervisor

#### DeepAgent
- **丰富的提示词**：
  - `write_todos`：任务分解和进度跟踪
  - `task`：子智能体调度工具
  - `baseAgentInstruction`：基础行为指南
- **决策机制**：LLM 通过 `task` 工具描述选择合适的子智能体
- **核心提示词片段**：
```go
taskToolDescription = `Launch a new agent to handle complex, multi-step tasks autonomously.
Available agent types and the tools they have access to:
{other_agents}
...`
```

### 1.3 关键发现

1. **提示词驱动**：所有模式都依赖 LLM 理解工具/智能体描述来做决策
2. **工具化**：DeepAgent 将子智能体包装为 `task` 工具
3. **上下文管理**：Supervisor 使用确定性转移控制流程

---

## 2. 统一模式设计方案

### 2.1 核心理念

将三种模式统一为一个 **UnifiedAgent**，通过配置切换不同的行为模式：

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

### 2.2 配置结构设计

```go
// AgentMode 智能体模式
type AgentMode string

const (
    // ModeReact React 模式 - 简单工具调用
    ModeReact AgentMode = "react"
    // ModeSupervisor 监督者模式 - 多智能体协调
    ModeSupervisor AgentMode = "supervisor"
    // ModeDeep 深度模式 - 任务编排和跟踪
    ModeDeep AgentMode = "deep"
    // ModeAuto 自动模式 - 根据任务复杂度自动选择
    ModeAuto AgentMode = "auto"
)

// UnifiedAgentConfig 统一智能体配置
type UnifiedAgentConfig struct {
    // 基础配置
    LLMConfig      LLMConfig   `json:"llmConfig"`
    SystemPrompt   string      `json:"systemPrompt"`
    MaxStep        int         `json:"maxStep"`

    // 模式配置
    Mode           AgentMode   `json:"mode"`           // 运行模式

    // 工具配置（React 模式）
    Tools          []Tool      `json:"tools"`

    // 子智能体配置（Supervisor/Deep 模式）
    SubAgents      []SubAgentConfig `json:"subAgents"`

    // 并行配置
    ParallelToolCalls    *bool `json:"parallelToolCalls"`
    ExecuteSequentially  bool  `json:"executeSequentially"`
    ParallelSubAgents    bool  `json:"parallelSubAgents"` // 是否并行调用子智能体

    // Deep 模式特有配置
    EnableTodoManagement bool  `json:"enableTodoManagement"` // 启用 TODO 管理
    WithoutGeneralAgent  bool  `json:"withoutGeneralAgent"`  // 禁用通用子智能体

    // 自动模式配置
    AutoModeConfig *AutoModeConfig `json:"autoModeConfig"`
}

// AutoModeConfig 自动模式配置
type AutoModeConfig struct {
    // SimpleTaskThreshold 简单任务阈值
    // 当工具数量 <= 此值且无子智能体时，使用 React 模式
    SimpleTaskThreshold int `json:"simpleTaskThreshold"`
    // EnableAutoTodo 是否自动启用 TODO 管理
    EnableAutoTodo bool `json:"enableAutoTodo"`
}
```

### 2.3 提示词融合策略

#### 2.3.1 基础提示词（所有模式共用）

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

#### 2.3.2 模式特定提示词扩展

```go
// React 模式扩展
const reactExtension = `
## 工具调用模式
你可以直接调用以下工具来完成任务：
{tools_description}

当多个工具调用相互独立时，请同时调用以提高效率。
`

// Supervisor 模式扩展
const supervisorExtension = `
## 子智能体协调模式
你可以委派任务给以下专业子智能体：
{subagents_description}

作为协调者，你需要：
1. 分析任务需求，选择合适的子智能体
2. 可以同时委派多个独立任务给不同的子智能体
3. 汇总各子智能体的结果，生成最终回答
`

// Deep 模式扩展
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

### 2.4 架构设计

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

### 2.5 子智能体并行调用方案

#### 2.5.1 核心思路

将子智能体包装为"可并行调用的工具"，利用现有的 `ExecuteSequentially` 配置：

```go
// SubAgentWrapper 将子智能体包装为工具
type SubAgentWrapper struct {
    agent     adk.Agent
    name      string
    desc      string
}

// 当 ExecuteSequentially=false 时，多个子智能体调用会并行执行
```

#### 2.5.2 提示词引导

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

## 3. 实现计划

### 3.1 阶段一：配置统一（低风险）

1. 创建 `UnifiedAgentConfig` 结构体
2. 保持现有三个 Node 不变，只是配置格式统一
3. 添加配置转换逻辑

### 3.2 阶段二：提示词融合（中风险）

1. 抽取公共提示词模板
2. 实现模式特定的提示词扩展
3. 添加提示词版本管理

### 3.3 阶段三：执行器统一（高风险）

1. 创建 `UnifiedAgentNode` 统一入口
2. 实现模式选择器
3. 统一子智能体调用机制
4. 添加子智能体并行执行支持

### 3.4 阶段四：自动模式（增强功能）

1. 实现任务复杂度评估
2. 自动选择最佳模式
3. 动态调整执行策略

---

## 4. 兼容性考虑

### 4.1 向后兼容

- 现有 `ReactAgentNode`、`SupervisorNode`、`DeepAgentNode` 保持不变
- `UnifiedAgentNode` 作为新的统一入口
- 配置格式支持旧格式自动转换

### 4.2 迁移路径

```json
// 旧配置（仍然支持）
{
  "type": "ai/agent",
  "configuration": { ... }
}

// 新配置（推荐）
{
  "type": "ai/agent/unified",
  "configuration": {
    "mode": "react",
    ...
  }
}
```

---

## 5. 可观测性与事件系统调整

### 5.1 现有事件系统分析

当前系统已经实现了 AG-UI 标准的事件类型：

| 事件类型 | 用途 | 现有支持 |
|---------|------|---------|
| `RUN_STARTED/FINISHED` | 智能体运行生命周期 | ✅ 已支持 |
| `STEP_STARTED/FINISHED` | 步骤级别跟踪 | ✅ 已支持 |
| `TOOL_CALL_START/END/RESULT` | 工具调用跟踪 | ✅ 已支持 |
| `TEXT_MESSAGE_*` | 流式文本输出 | ✅ 已支持 |
| `THINKING_*` | 思考过程 | ✅ 已支持 |
| `STATE_SNAPSHOT/DELTA` | 状态管理 | ✅ 已支持 |

### 5.2 需要新增的事件类型

#### 5.2.1 子智能体调用事件

```go
// 新增事件类型
const (
    // 子智能体调用事件（类似工具调用，但针对子智能体）
    EventSubAgentCallStart  EventType = "SUB_AGENT_CALL_START"
    EventSubAgentCallEnd    EventType = "SUB_AGENT_CALL_END"
    EventSubAgentCallResult EventType = "SUB_AGENT_CALL_RESULT"

    // TODO 状态变化事件（Deep 模式）
    EventTodoCreated    EventType = "TODO_CREATED"
    EventTodoUpdated    EventType = "TODO_UPDATED"
    EventTodoCompleted  EventType = "TODO_COMPLETED"

    // 并行执行事件
    EventParallelStart  EventType = "PARALLEL_START"   // 并行执行开始
    EventParallelEnd    EventType = "PARALLEL_END"     // 并行执行结束

    // 模式切换事件（统一智能体）
    EventModeSelected   EventType = "MODE_SELECTED"    // 模式选择
)

// SubAgentCallStartEvent 子智能体调用开始事件
type SubAgentCallStartEvent struct {
    BaseEvent
    CallId       string `json:"callId"`       // 调用 ID
    AgentName    string `json:"agentName"`    // 子智能体名称
    AgentType    string `json:"agentType"`    // 子智能体类型
    ParentRunId  string `json:"parentRunId"`  // 父运行 ID
    Input        string `json:"input"`        // 输入参数
}

// SubAgentCallResultEvent 子智能体调用结果事件
type SubAgentCallResultEvent struct {
    BaseEvent
    CallId    string `json:"callId"`
    AgentName string `json:"agentName"`
    Output    string `json:"output"`
    Duration  int64  `json:"duration"` // 执行耗时（毫秒）
    IsError   bool   `json:"isError"`
}

// TodoStatusChangeEvent TODO 状态变化事件
type TodoStatusChangeEvent struct {
    BaseEvent
    TodoId      string `json:"todoId"`
    Content     string `json:"content"`
    ActiveForm  string `json:"activeForm"`
    OldStatus   string `json:"oldStatus"`   // pending/in_progress/completed
    NewStatus   string `json:"newStatus"`
}

// ParallelStartEvent 并行执行开始事件
type ParallelStartEvent struct {
    BaseEvent
    ExecutionId string   `json:"executionId"`
    TaskCount   int      `json:"taskCount"`   // 并行任务数量
    TaskTypes   []string `json:"taskTypes"`   // 任务类型列表
}

// ParallelEndEvent 并行执行结束事件
type ParallelEndEvent struct {
    BaseEvent
    ExecutionId  string `json:"executionId"`
    SuccessCount int    `json:"successCount"` // 成功数量
    FailedCount  int    `json:"failedCount"`  // 失败数量
    TotalDuration int64 `json:"totalDuration"` // 总耗时
}
```

#### 5.2.2 EventEmitter 接口扩展

```go
// EventEmitter 扩展接口
type EventEmitter interface {
    // ... 现有方法 ...

    // 子智能体调用事件（新增）
    EmitSubAgentCallStart(callId, agentName, agentType, parentRunId, input string)
    EmitSubAgentCallEnd(callId, agentName string)
    EmitSubAgentCallResult(callId, agentName, output string, duration int64, isError bool)

    // TODO 状态变化事件（新增）
    EmitTodoCreated(todoId, content, activeForm string)
    EmitTodoUpdated(todoId, content, activeForm, oldStatus, newStatus string)
    EmitTodoCompleted(todoId, content string)

    // 并行执行事件（新增）
    EmitParallelStart(executionId string, taskCount int, taskTypes []string)
    EmitParallelEnd(executionId string, successCount, failedCount int, totalDuration int64)

    // 模式选择事件（新增）
    EmitModeSelected(mode AgentMode, reason string)
}
```

### 5.3 切面系统调整

#### 5.3.1 新增切面接口

```go
// SubAgentCallBeforeAspect 子智能体调用前切面
type SubAgentCallBeforeAspect interface {
    Aspect
    PointCut
    BeforeSubAgentCall(ctx context.Context, point *AgentPoint, call *SubAgentCallInfo) (*SubAgentCallInfo, error)
}

// SubAgentCallAfterAspect 子智能体调用后切面
type SubAgentCallAfterAspect interface {
    Aspect
    PointCut
    AfterSubAgentCall(ctx context.Context, point *AgentPoint, call *SubAgentCallInfo, result *SubAgentCallResult) error
}

// TodoChangeAspect TODO 状态变化切面
type TodoChangeAspect interface {
    Aspect
    PointCut
    OnTodoChange(ctx context.Context, point *AgentPoint, todo *TodoInfo) error
}

// ParallelExecutionAspect 并行执行切面
type ParallelExecutionAspect interface {
    Aspect
    PointCut
    OnParallelStart(ctx context.Context, point *AgentPoint, execution *ParallelExecutionInfo) error
    OnParallelEnd(ctx context.Context, point *AgentPoint, execution *ParallelExecutionInfo, results []*ParallelTaskResult) error
}
```

#### 5.3.2 AgentPoint 扩展

```go
// AgentPoint 扩展字段
type AgentPoint struct {
    // ... 现有字段 ...

    // 新增字段
    SubAgentName string            // 子智能体名称（子智能体调用时）
    SubAgentType string            // 子智能体类型
    TodoId       string            // TODO ID（TODO 状态变化时）
    ParallelId   string            // 并行执行 ID（并行执行时）
    IsParallel   bool              // 是否并行执行
    Mode         AgentMode         // 智能体模式
}
```

### 5.4 可视化支持

#### 5.4.1 事件流示例

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

#### 5.4.2 前端展示建议

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

### 5.5 实现优先级

| 优先级 | 事件类型 | 原因 |
|--------|---------|------|
| P0 | `SUB_AGENT_CALL_*` | 子智能体调用是统一模式的核心 |
| P0 | `MODE_SELECTED` | 需要知道当前运行模式 |
| P1 | `TODO_*` | Deep 模式的任务跟踪 |
| P1 | `PARALLEL_*` | 并行执行可视化 |
| P2 | 切面扩展 | 高级可观测性需求 |

---

## 7. 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 提示词冲突 | 高 | 分层设计，模式隔离 |
| 性能回退 | 中 | 保留原实现，渐进迁移 |
| 配置复杂度 | 中 | 提供默认值和自动模式 |
| 子智能体并行导致状态问题 | 高 | 独立上下文，结果隔离 |

---

## 8. 总结

本方案通过以下方式统一三种智能体模式：

1. **配置统一**：一套配置支持所有模式
2. **提示词融合**：分层提示词设计，模式特定扩展
3. **执行器抽象**：统一入口，模式选择器
4. **并行增强**：支持子智能体并行调用

核心优势：
- 简化用户选择（自动模式）
- 保留灵活性（手动选择模式）
- 增强能力（子智能体并行）
- 平滑迁移（向后兼容）
