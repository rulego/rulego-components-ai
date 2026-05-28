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

	"github.com/cloudwego/eino/schema"
)

// ============================================================
// Agent Lifecycle Aspects / 智能体生命周期切面
// ============================================================

// AgentStartAspect defines the interface for aspects executed when an agent starts processing.
// Similar to RuleGo's StartAspect, this is called before any agent processing begins.
//
// AgentStartAspect 定义智能体开始处理时执行的切面接口。
// 类似于 RuleGo 的 StartAspect，在任何智能体处理开始之前调用。
type AgentStartAspect interface {
	Aspect
	PointCut

	// OnStart is executed when the agent starts processing a message.
	// Called before any agent processing begins.
	//
	// OnStart 在智能体开始处理消息时执行。
	// 在任何智能体处理开始之前调用。
	//
	// Use cases:
	// 用例：
	//   - Initialize session / 初始化会话
	//   - Send start events for visualization / 发送可视化开始事件
	//   - Validate input / 验证输入
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - input: Agent input / 智能体输入
	//
	// Returns:
	// 返回：
	//   - *AgentInput: Modified input / 修改后的输入
	//   - error: Error to terminate execution, nil to continue / 终止执行的错误，nil 表示继续
	OnStart(ctx context.Context, point *AgentPoint, input *AgentInput) (*AgentInput, error)
}

// AgentCompletedAspect defines the interface for aspects executed when an agent completes processing.
// Similar to RuleGo's CompletedAspect, this is called when all processing is finished.
//
// AgentCompletedAspect 定义智能体完成处理时执行的切面接口。
// 类似于 RuleGo 的 CompletedAspect，在所有处理完成时调用。
type AgentCompletedAspect interface {
	Aspect
	PointCut

	// OnCompleted is executed when the agent completes processing (success or failure).
	// Called after all agent processing is finished.
	//
	// OnCompleted 在智能体完成处理时执行（无论成功或失败）。
	// 在所有智能体处理完成后调用。
	//
	// Use cases:
	// 用例：
	//   - Send completion events for visualization / 发送可视化完成事件
	//   - Collect performance metrics / 收集性能指标
	//   - Clean up resources / 清理资源
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - output: Agent output / 智能体输出
	OnCompleted(ctx context.Context, point *AgentPoint, output *AgentOutput)
}

// ============================================================
// Agent Execution Aspects / 智能体执行切面
// ============================================================

// AgentBeforeAspect defines the interface for aspects executed before agent execution.
// Similar to RuleGo's BeforeAspect, this is called before the agent's main execution.
//
// AgentBeforeAspect 定义智能体执行前执行的切面接口。
// 类似于 RuleGo 的 BeforeAspect，在智能体主执行之前调用。
type AgentBeforeAspect interface {
	Aspect
	PointCut

	// Before is executed before the agent's main execution.
	// The returned input will be used for agent processing.
	//
	// Before 在智能体主执行之前执行。
	// 返回的输入将用于智能体处理。
	//
	// Use cases:
	// 用例：
	//   - Load conversation history / 加载会话历史
	//   - Inject context / 注入上下文
	//   - Modify input messages / 修改输入消息
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - input: Original agent input / 原始智能体输入
	//
	// Returns:
	// 返回：
	//   - *AgentInput: Modified input for agent processing / 用于智能体处理的修改后输入
	//   - error: Error to terminate execution, nil to continue / 终止执行的错误，nil 表示继续
	Before(ctx context.Context, point *AgentPoint, input *AgentInput) (*AgentInput, error)
}

// AgentAfterAspect defines the interface for aspects executed after agent execution.
// Similar to RuleGo's AfterAspect, this is called after the agent's main execution.
//
// AgentAfterAspect 定义智能体执行后执行的切面接口。
// 类似于 RuleGo 的 AfterAspect，在智能体主执行之后调用。
type AgentAfterAspect interface {
	Aspect
	PointCut

	// After is executed after the agent's main execution completes.
	// The returned output will be used for subsequent processing.
	//
	// After 在智能体主执行完成后执行。
	// 返回的输出将用于后续处理。
	//
	// Use cases:
	// 用例：
	//   - Save conversation history / 保存会话历史
	//   - Process output / 处理输出
	//   - Collect metrics / 收集指标
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - output: Agent output from execution / 来自执行的智能体输出
	//
	// Returns:
	// 返回：
	//   - *AgentOutput: Modified output for subsequent processing / 用于后续处理的修改后输出
	//   - error: Error (non-terminating), nil to continue / 错误（非终止），nil 表示继续
	After(ctx context.Context, point *AgentPoint, output *AgentOutput) (*AgentOutput, error)
}

// AgentAroundAspect defines the interface for aspects that wrap around agent execution.
// Similar to RuleGo's AroundAspect, this provides complete control over agent execution.
//
// AgentAroundAspect 定义包裹智能体执行的切面接口。
// 类似于 RuleGo 的 AroundAspect，提供对智能体执行的完全控制。
type AgentAroundAspect interface {
	Aspect
	PointCut

	// Around wraps the agent execution, providing complete control over the execution flow.
	// The aspect can decide whether to call the next executor or skip it.
	//
	// Around 包裹智能体执行，提供对执行流程的完全控制。
	// 切面可以决定是否调用下一个执行器或跳过它。
	//
	// Use cases:
	// 用例：
	//   - Implement timeout / 实现超时
	//   - Implement retry logic / 实现重试逻辑
	//   - Cache results / 缓存结果
	//   - Completely override execution / 完全覆盖执行
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - input: Agent input / 智能体输入
	//   - next: Next executor in the chain (call to continue execution) / 链中的下一个执行器（调用以继续执行）
	//
	// Returns:
	// 返回：
	//   - *AgentOutput: Agent output / 智能体输出
	//   - error: Execution error / 执行错误
	Around(ctx context.Context, point *AgentPoint, input *AgentInput, next AgentExecutor) (*AgentOutput, error)
}

// ============================================================
// Message Processing Aspects / 消息处理切面
// ============================================================

// MessageBeforeAspect defines the interface for aspects executed before LLM calls.
//
// MessageBeforeAspect 定义 LLM 调用前执行的切面接口。
type MessageBeforeAspect interface {
	Aspect
	PointCut

	// BeforeLLM is executed before LLM generates a response.
	// The returned messages will be used for the LLM call.
	//
	// BeforeLLM 在 LLM 生成响应之前执行。
	// 返回的消息将用于 LLM 调用。
	//
	// Use cases:
	// 用例：
	//   - Add system messages / 添加系统消息
	//   - Trim context window / 修剪上下文窗口
	//   - Filter messages / 过滤消息
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - messages: Original messages to send to LLM / 发送到 LLM 的原始消息
	//
	// Returns:
	// 返回：
	//   - []*schema.Message: Modified messages for LLM / 用于 LLM 的修改后消息
	//   - error: Error to terminate execution, nil to continue / 终止执行的错误，nil 表示继续
	BeforeLLM(ctx context.Context, point *AgentPoint, messages []*schema.Message) ([]*schema.Message, error)
}

// MessageAfterAspect defines the interface for aspects executed after LLM calls.
//
// MessageAfterAspect 定义 LLM 调用后执行的切面接口。
type MessageAfterAspect interface {
	Aspect
	PointCut

	// AfterLLM is executed after LLM generates a response.
	// The returned message will be used for subsequent processing.
	//
	// AfterLLM 在 LLM 生成响应之后执行。
	// 返回的消息将用于后续处理。
	//
	// Use cases:
	// 用例：
	//   - Filter or modify response / 过滤或修改响应
	//   - Log response / 记录响应
	//   - Extract metadata / 提取元数据
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - response: LLM response message / LLM 响应消息
	//
	// Returns:
	// 返回：
	//   - *schema.Message: Modified response message / 修改后的响应消息
	//   - error: Error (non-terminating), nil to continue / 错误（非终止），nil 表示继续
	AfterLLM(ctx context.Context, point *AgentPoint, response *schema.Message) (*schema.Message, error)
}

// ============================================================
// Stream Processing Aspects / 流式处理切面
// ============================================================

// StreamChunkAspect defines the interface for aspects executed on each stream chunk.
//
// StreamChunkAspect 定义在每个流式块上执行的切面接口。
type StreamChunkAspect interface {
	Aspect
	PointCut

	// OnChunk is executed for each streaming chunk from the agent.
	// This allows real-time processing of streaming output.
	//
	// OnChunk 对来自智能体的每个流式块执行。
	// 这允许实时处理流式输出。
	//
	// Use cases:
	// 用例：
	//   - Send visualization events / 发送可视化事件
	//   - Real-time logging / 实时日志记录
	//   - Accumulate content / 累积内容
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - chunk: Stream chunk information / 流式块信息
	//
	// Returns:
	// 返回：
	//   - error: Error to terminate streaming, nil to continue / 终止流式输出的错误，nil 表示继续
	OnChunk(ctx context.Context, point *AgentPoint, chunk *StreamChunk) error
}

// ============================================================
// Tool Call Aspects / 工具调用切面
// ============================================================

// ToolCallBeforeAspect defines the interface for aspects executed before tool calls.
//
// ToolCallBeforeAspect 定义工具调用前执行的切面接口。
type ToolCallBeforeAspect interface {
	Aspect
	PointCut

	// BeforeToolCall is executed before a tool is called.
	// The returned call info will be used for the tool execution.
	//
	// BeforeToolCall 在工具调用之前执行。
	// 返回的调用信息将用于工具执行。
	//
	// Use cases:
	// 用例：
	//   - Log tool calls / 记录工具调用
	//   - Send start events for visualization / 发送可视化开始事件
	//   - Intercept or modify tool calls / 拦截或修改工具调用
	//   - Validate tool arguments / 验证工具参数
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - call: Tool call information / 工具调用信息
	//
	// Returns:
	// 返回：
	//   - *ToolCallInfo: Modified call info / 修改后的调用信息
	//   - error: Error to prevent tool call, nil to continue / 阻止工具调用的错误，nil 表示继续
	BeforeToolCall(ctx context.Context, point *AgentPoint, call *ToolCallInfo) (*ToolCallInfo, error)
}

// ToolCallAfterAspect defines the interface for aspects executed after tool calls.
//
// ToolCallAfterAspect 定义工具调用后执行的切面接口。
type ToolCallAfterAspect interface {
	Aspect
	PointCut

	// AfterToolCall is executed after a tool call completes.
	// This allows processing of tool results.
	//
	// AfterToolCall 在工具调用完成后执行。
	// 这允许处理工具结果。
	//
	// Use cases:
	// 用例：
	//   - Log tool results / 记录工具结果
	//   - Send completion events for visualization / 发送可视化完成事件
	//   - Process tool errors / 处理工具错误
	//   - Collect tool call metrics / 收集工具调用指标
	//
	// Parameters:
	// 参数：
	//   - ctx: Execution context / 执行上下文
	//   - point: Agent execution point information / 智能体执行点信息
	//   - call: Original tool call information / 原始工具调用信息
	//   - result: Tool call result / 工具调用结果
	//
	// Returns:
	// 返回：
	//   - error: Error (non-terminating), nil to continue / 错误（非终止），nil 表示继续
	AfterToolCall(ctx context.Context, point *AgentPoint, call *ToolCallInfo, result *ToolCallResult) error
}
