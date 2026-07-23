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
// Agent Lifecycle Aspects
// ============================================================

// AgentStartAspect defines the interface for aspects executed when an agent starts processing.
// Similar to RuleGo's StartAspect, this is called before any agent processing begins.
//
// AgentStartAspect defines the interface of the interface executed when the agent begins processing.
// Similar to RuleGo's StartAspect, it is called before any agent starts processing.
type AgentStartAspect interface {
	Aspect
	PointCut

	// OnStart is executed when the agent starts processing a message.
	// Called before any agent processing begins.
	//
	// OnStart is executed when the agent begins processing messages.
	// Call before any agent begins processing.
	//
	// Use cases:
	// Use Cases:
	//   - Initialize session
	//   - Send start events for visualization
	//   - Validate input
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - input: Agent input
	//
	// Returns:
	// Returns:
	//   - *AgentInput: Modified input
	//   - error: Error to terminate execution, nil to continue
	OnStart(ctx context.Context, point *AgentPoint, input *AgentInput) (*AgentInput, error)
}

// AgentCompletedAspect defines the interface for aspects executed when an agent completes processing.
// Similar to RuleGo's CompletedAspect, this is called when all processing is finished.
//
// AgentCompletedAspect defines the interface executed when the agent completes processing.
// Similar to RuleGo's CompletedAspect, it is called when all processing is complete.
type AgentCompletedAspect interface {
	Aspect
	PointCut

	// OnCompleted is executed when the agent completes processing (success or failure).
	// Called after all agent processing is finished.
	//
	// OnCompleted executes when the agent completes its processing (whether successful or failed).
	// Called after all agents have finished processing.
	//
	// Use cases:
	// Use Cases:
	//   - Send completion events for visualization
	//   - Collect performance metrics
	//   - Clean up resources
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - output: Agent output
	OnCompleted(ctx context.Context, point *AgentPoint, output *AgentOutput)
}

// ============================================================
// Agent Execution Aspects
// ============================================================

// AgentBeforeAspect defines the interface for aspects executed before agent execution.
// Similar to RuleGo's BeforeAspect, this is called before the agent's main execution.
//
// AgentBeforeAspect defines the interface of the interface executed before the agent executes.
// Similar to RuleGo's BeforeAspect, it is called before the agent executes its main function.
type AgentBeforeAspect interface {
	Aspect
	PointCut

	// Before is executed before the agent's main execution.
	// The returned input will be used for agent processing.
	//
	// Before the agent executes before the main agent executes.
	// The returned input will be used for agent processing.
	//
	// Use cases:
	// Use Cases:
	//   - Load conversation history
	//   - Inject context
	//   - Modify input messages
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - input: Original agent input
	//
	// Returns:
	// Returns:
	//   - *AgentInput: Modified input for agent processing
	//   - error: Error to terminate execution, nil to continue
	Before(ctx context.Context, point *AgentPoint, input *AgentInput) (*AgentInput, error)
}

// AgentAfterAspect defines the interface for aspects executed after agent execution.
// Similar to RuleGo's AfterAspect, this is called after the agent's main execution.
//
// AgentAfterAspect defines the interface that the agent executes after execution.
// Similar to RuleGo's AfterAspect, it is called after the agent executes the main function.
type AgentAfterAspect interface {
	Aspect
	PointCut

	// After is executed after the agent's main execution completes.
	// The returned output will be used for subsequent processing.
	//
	// After: executes after the agent has completed its main execution.
	// The returned output will be used for subsequent processing.
	//
	// Use cases:
	// Use Cases:
	//   - Save conversation history
	//   - Process output
	//   - Collect metrics
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - output: Agent output from execution
	//
	// Returns:
	// Returns:
	//   - *AgentOutput: Modified output for subsequent processing
	//   - error: Error (non-terminating), nil to continue
	After(ctx context.Context, point *AgentPoint, output *AgentOutput) (*AgentOutput, error)
}

// AgentAroundAspect defines the interface for aspects that wrap around agent execution.
// Similar to RuleGo's AroundAspect, this provides complete control over agent execution.
//
// AgentAroundAspect defines the interface where the package agent executes.
// Similar to RuleGo's AroundAspect, it provides complete control over agent execution.
type AgentAroundAspect interface {
	Aspect
	PointCut

	// Around wraps the agent execution, providing complete control over the execution flow.
	// The aspect can decide whether to call the next executor or skip it.
	//
	// Around parcel agent execution, providing complete control over the execution process.
	// The facet can decide whether to call the next actuator or skip it.
	//
	// Use cases:
	// Use Cases:
	//   - Implement timeout
	//   - Implement retry logic
	//   - Cache results
	//   - Completely override execution
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - input: Agent input
	//   - next: Next executor in the chain (call to continue execution)
	//
	// Returns:
	// Returns:
	//   - *AgentOutput: Agent output
	//   - error: Execution error
	Around(ctx context.Context, point *AgentPoint, input *AgentInput, next AgentExecutor) (*AgentOutput, error)
}

// ============================================================
// Message Processing Aspects
// ============================================================

// MessageBeforeAspect defines the interface for aspects executed before LLM calls.
//
// MessageBeforeAspect defines the faceted interface executed before the LLM is called.
type MessageBeforeAspect interface {
	Aspect
	PointCut

	// BeforeLLM is executed before LLM generates a response.
	// The returned messages will be used for the LLM call.
	//
	// BeforeLLM executes before the LLM generates a response.
	// The returned message will be used for LLM calls.
	//
	// Use cases:
	// Use Cases:
	//   - Add system messages
	//   - Trim context window
	//   - Filter messages
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - messages: Original messages to send to LLM
	//
	// Returns:
	// Returns:
	//   - []*schema.Message: Modified messages for LLM
	//   - error: Error to terminate execution, nil to continue
	BeforeLLM(ctx context.Context, point *AgentPoint, messages []*schema.Message) ([]*schema.Message, error)
}

// MessageAfterAspect defines the interface for aspects executed after LLM calls.
//
// MessageAfterAspect defines the faceted interface executed after the LLM calls.
type MessageAfterAspect interface {
	Aspect
	PointCut

	// AfterLLM is executed after LLM generates a response.
	// The returned message will be used for subsequent processing.
	//
	// AfterLLM executes after the LLM generates a response.
	// The returned messages will be used for subsequent processing.
	//
	// Use cases:
	// Use Cases:
	//   - Filter or modify response
	//   - Log response
	//   - Extract metadata
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - response: LLM response message / LLM response message
	//
	// Returns:
	// Returns:
	//   - *schema.Message: Modified response message
	//   - error: Error (non-terminating), nil to continue
	AfterLLM(ctx context.Context, point *AgentPoint, response *schema.Message) (*schema.Message, error)
}

// ============================================================
// Stream Processing Aspects
// ============================================================

// StreamChunkAspect defines the interface for aspects executed on each stream chunk.
//
// StreamChunkAspect defines the interface of the facet executed on each streaming block.
type StreamChunkAspect interface {
	Aspect
	PointCut

	// OnChunk is executed for each streaming chunk from the agent.
	// This allows real-time processing of streaming output.
	//
	// OnChunk executes every stream block from the agent.
	// This allows real-time processing of streaming output.
	//
	// Use cases:
	// Use Cases:
	//   - Send visualization events
	//   - Real-time logging
	//   - Accumulate content
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - chunk: Stream chunk information
	//
	// Returns:
	// Returns:
	//   - error: Error to terminate streaming, nil to continue
	OnChunk(ctx context.Context, point *AgentPoint, chunk *StreamChunk) error
}

// ============================================================
// Tool Call Aspects
// ============================================================

// ToolCallBeforeAspect defines the interface for aspects executed before tool calls.
//
// ToolCallBeforeAspect defines the interface to be executed before the tool is called.
type ToolCallBeforeAspect interface {
	Aspect
	PointCut

	// BeforeToolCall is executed before a tool is called.
	// The returned call info will be used for the tool execution.
	//
	// BeforeToolCall is executed before the tool is called.
	// The returned call information will be used for tool execution.
	//
	// Use cases:
	// Use Cases:
	//   - Log tool calls
	//   - Send start events for visualization
	//   - Intercept or modify tool calls
	//   - Validate tool arguments
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - call: Tool call information
	//
	// Returns:
	// Returns:
	//   - *ToolCallInfo: Modified call info
	//   - error: Error to prevent tool call, nil to continue
	BeforeToolCall(ctx context.Context, point *AgentPoint, call *ToolCallInfo) (*ToolCallInfo, error)
}

// ToolCallAfterAspect defines the interface for aspects executed after tool calls.
//
// ToolCallAfterAspect defines the faceted interface executed after the tool is called.
type ToolCallAfterAspect interface {
	Aspect
	PointCut

	// AfterToolCall is executed after a tool call completes.
	// This allows processing of tool results.
	//
	// AfterToolCall is executed after the tool call is completed.
	// This allows for the processing of tool results.
	//
	// Use cases:
	// Use Cases:
	//   - Log tool results
	//   - Send completion events for visualization
	//   - Process tool errors
	//   - Collect tool call metrics
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//   - point: Agent execution point information
	//   - call: Original tool call information
	//   - result: Tool call result
	//
	// Returns:
	// Returns:
	//   - error: Error (non-terminating), nil to continue
	AfterToolCall(ctx context.Context, point *AgentPoint, call *ToolCallInfo, result *ToolCallResult) error
}
