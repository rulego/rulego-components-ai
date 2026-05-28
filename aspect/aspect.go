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

package aspect

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/utils/contextx"
)

// ============================================
// Tool Calls Collector - 工具调用收集器
// ============================================

// toolCallsKey stores ToolCallsCollector in context
var toolCallsKey = contextx.NewKey[*ToolCallsCollector]("toolCalls")

// ToolCallsCollector 工具调用收集器
// 线程安全，用于在 Agent 执行过程中收集工具调用结果
type ToolCallsCollector struct {
	mu    sync.Mutex
	calls []ToolCallResult
}

// NewToolCallsCollector 创建新的工具调用收集器
func NewToolCallsCollector() *ToolCallsCollector {
	return &ToolCallsCollector{
		calls: make([]ToolCallResult, 0),
	}
}

// Add 添加工具调用结果
func (c *ToolCallsCollector) Add(call ToolCallResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

// Get 获取所有工具调用结果
func (c *ToolCallsCollector) Get() []ToolCallResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]ToolCallResult, len(c.calls))
	copy(result, c.calls)
	return result
}

// WithToolCallsCollector 将工具调用收集器存入 context
func WithToolCallsCollector(ctx context.Context, collector *ToolCallsCollector) context.Context {
	return toolCallsKey.With(ctx, collector)
}

// GetToolCallsCollector 从 context 获取工具调用收集器
func GetToolCallsCollector(ctx context.Context) *ToolCallsCollector {
	c, _ := toolCallsKey.Get(ctx)
	return c
}

// AddToolCallResultToContext 添加工具调用结果到 context 中的收集器
// 这是一个便捷函数，用于在工具执行后将结果添加到 context
func AddToolCallResultToContext(ctx context.Context, result *ToolCallResult) {
	collector := GetToolCallsCollector(ctx)
	if collector == nil {
		return
	}
	collector.Add(*result)
}

// GetToolCallResultsFromContext 从 context 获取所有工具调用结果
// 这是一个便捷函数，用于在构建输出时读取工具调用结果
func GetToolCallResultsFromContext(ctx context.Context) []ToolCallResult {
	collector := GetToolCallsCollector(ctx)
	if collector == nil {
		return nil
	}
	return collector.Get()
}

// ============================================
// Aspect Interface - 切面接口定义
// ============================================

// Aspect defines the base interface for implementing Aspect-Oriented Programming (AOP) for AI Agents.
// Aspect 定义用于实现智能体面向切面编程（AOP）的基础接口。
//
// Aspect provides cross-cutting functionality that can intercept and enhance agent execution
// without modifying the original business logic.
//
// 切面提供可以拦截和增强智能体执行的横切功能，而无需修改原始业务逻辑。
//
// Agent Aspect Categories:
// 智能体切面类别：
//
//   - Agent Lifecycle Aspects: AgentStartAspect, AgentCompletedAspect
//     智能体生命周期切面：AgentStartAspect、AgentCompletedAspect
//   - Agent Execution Aspects: AgentBeforeAspect, AgentAfterAspect, AgentAroundAspect
//     智能体执行切面：AgentBeforeAspect、AgentAfterAspect、AgentAroundAspect
//   - Message Processing Aspects: MessageBeforeAspect, MessageAfterAspect
//     消息处理切面：MessageBeforeAspect、MessageAfterAspect
//   - Stream Processing Aspects: StreamChunkAspect
//     流式处理切面：StreamChunkAspect
//   - Tool Call Aspects: ToolCallBeforeAspect, ToolCallAfterAspect
//     工具调用切面：ToolCallBeforeAspect、ToolCallAfterAspect
//
// Execution Order:
// 执行顺序：
//
//  1. Agent Level:
//     智能体级别：
//     AgentStart -> AgentBefore -> AgentAround -> [Agent Execution] -> AgentAfter -> AgentCompleted
//     智能体开始 -> 智能体执行前 -> 智能体环绕 -> [智能体执行] -> 智能体执行后 -> 智能体完成
//
//  2. Inside Agent Execution Loop:
//     智能体执行循环内部：
//     MessageBefore -> [LLM Generate/Stream] -> StreamChunk -> ToolCallBefore -> [Tool Execution] -> ToolCallAfter
//     消息处理前 -> [LLM 生成/流式] -> 流式块 -> 工具调用前 -> [工具执行] -> 工具调用后
type Aspect interface {
	// Order returns the execution priority of the aspect.
	// Lower values indicate higher priority and earlier execution in the aspect chain.
	//
	// Order 返回切面的执行优先级。
	// 较小的值表示更高的优先级和在切面链中更早的执行。
	//
	// Returns:
	// 返回：
	//   - int: Priority value, lower numbers execute first
	//     int：优先级值，较小的数字先执行
	Order() int

	// New creates a new instance of the aspect for a specific agent instance.
	// This ensures proper isolation between different agent instances.
	//
	// New 为特定的智能体实例创建切面的新实例。
	// 这确保了不同智能体实例之间的适当隔离。
	//
	// Implementation Requirements:
	// 实现要求：
	//   - Create a completely independent instance
	//     创建完全独立的实例
	//   - Copy necessary configuration
	//     复制必要的配置
	//   - Ensure no shared mutable state between instances
	//     确保实例之间没有共享的可变状态
	//
	// Returns:
	// 返回：
	//   - Aspect: New aspect instance for the agent
	//     Aspect：智能体的新切面实例
	New() Aspect
}

// PointCut defines the interface for determining whether an aspect should be applied
// based on runtime conditions.
//
// PointCut 定义用于基于运行时条件确定是否应用切面的接口。
type PointCut interface {
	// PointCut determines whether this aspect should be applied to a specific execution point.
	// This method enables selective aspect application based on runtime conditions.
	//
	// PointCut 确定此切面是否应应用于特定的执行点。
	// 此方法基于运行时条件启用选择性切面应用。
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context
	//     ctx：执行上下文
	//   - point: Agent execution point information
	//     point：智能体执行点信息
	//
	// Returns:
	// 返回：
	//   - bool: true to apply aspect, false to skip
	//     bool：true 应用切面，false 跳过
	PointCut(ctx context.Context, point *AgentPoint) bool
}

// AgentPoint represents the execution point information for aspect interception.
// It contains metadata about the current agent execution context.
//
// AgentPoint 表示切面拦截的执行点信息。
// 它包含关于当前智能体执行上下文的元数据。
type AgentPoint struct {
	AgentId     string            // Agent ID / 智能体 ID
	AgentName   string            // Agent name / 智能体名称
	AgentType   string            // Agent type (react, deep, etc.) / 智能体类型
	ThreadId    string            // Session thread ID / 会话线程 ID
	UserId      string            // User ID / 用户 ID
	MessageType string            // Message type / 消息类型
	ToolName    string            // Tool name (during tool calls) / 工具名称（工具调用时）
	Metadata    map[string]string // Additional metadata / 额外元数据
}

// AgentInput represents the input to an agent execution.
//
// AgentInput 表示智能体执行的输入。
type AgentInput struct {
	Messages         []*schema.Message // Current messages / 当前消息
	SystemPrompt     string            // System prompt / 系统提示词
	Context          map[string]any    // Execution context / 执行上下文
	Metadata         map[string]string // Input metadata / 输入元数据
	SessionKey       string            // Session key for conversation history / 会话键
	HistoryMessages  []*schema.Message // Historical messages (populated by session aspect) / 历史消息（由会话切面填充）
	OriginalMessages []*schema.Message // Original input messages (before history merge) / 原始输入消息（历史合并前）
}

// AgentOutput represents the output from an agent execution.
//
// AgentOutput 表示智能体执行的输出。
type AgentOutput struct {
	Content          string            // Response content / 响应内容
	Messages         []*schema.Message // Complete message history / 完整消息历史
	OriginalMessages []*schema.Message // Original input messages for session saving / 原始输入消息（用于会话保存）
	ToolCalls        []ToolCallResult  // Tool call records / 工具调用记录
	TokenUsage       TokenUsage        // Token usage statistics / Token 使用统计
	Duration         int64             // Execution duration in milliseconds / 执行耗时（毫秒）
	Metadata         map[string]any    // Output metadata / 输出元数据
	SessionKey       string            // Session key / 会话键
	IsSuccess        bool              // Execution success status / 执行成功状态
	Error            error             // Error if failed / 错误信息（失败时）
	SkippedAI        bool              // Whether AI processing was skipped (e.g., by Around aspect interception) / 是否跳过了 AI 处理（如 Around 切面拦截）
}

// TokenUsage represents token usage statistics for an agent execution.
//
// TokenUsage 表示智能体执行的 token 使用统计。
type TokenUsage struct {
	PromptTokens     int // Input tokens / 输入 tokens
	CompletionTokens int // Output tokens / 输出 tokens
	TotalTokens      int // Total tokens / 总 tokens
}

// ToolCallInfo represents information about a tool call before execution.
//
// ToolCallInfo 表示工具调用执行前的信息。
type ToolCallInfo struct {
	CallId    string    // Unique call ID / 唯一调用 ID
	Name      string    // Tool name / 工具名称
	Arguments string    // Tool arguments as JSON / 工具参数（JSON 格式）
	ToolType  ToolType  // Tool type: builtin/rulechain/subagent/mcp / 工具类型
	TargetId  string    // Target ID (rulechain ID or tool ID) / 目标 ID
	StartTime time.Time // Call start time / 调用开始时间
}

// ToolCallResult represents the result of a tool call execution.
//
// ToolCallResult 表示工具调用执行的结果。
type ToolCallResult struct {
	CallId    string    // Unique call ID / 唯一调用 ID
	Name      string    // Tool name / 工具名称
	Arguments string    // Tool call arguments (JSON) / 工具调用参数（JSON格式）
	Result    string    // Tool execution result / 工具执行结果
	Error     error     // Error if failed / 错误（失败时）
	Duration  int64     // Execution duration in milliseconds / 执行耗时（毫秒）
	EndTime   time.Time // Call end time / 调用结束时间
}

// StreamChunk represents a chunk of streaming output from an agent.
//
// StreamChunk 表示来自智能体的流式输出块。
type StreamChunk struct {
	Content    string    // Chunk content / 块内容
	IsToolCall bool      // Whether this is a tool call chunk / 是否是工具调用块
	ToolName   string    // Tool name for tool call chunks / 工具名称（工具调用时）
	ToolArgs   string    // Tool arguments for tool call chunks / 工具参数（工具调用时）
	IsFinal    bool      // Whether this is the final chunk / 是否是最后一个块
	IsError    bool      // Whether this is an error chunk / 是否是错误块
	Timestamp  time.Time // Chunk timestamp / 块时间戳
}

// AgentExecutor represents the agent execution function used by Around aspects.
//
// AgentExecutor 表示 Around 切面使用的智能体执行函数。
type AgentExecutor func(ctx context.Context, input *AgentInput) (*AgentOutput, error)
