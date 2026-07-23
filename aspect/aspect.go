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
// Tool Calls Collector - Tool call collector
// ============================================

// toolCallsKey stores ToolCallsCollector in context
var toolCallsKey = contextx.NewKey[*ToolCallsCollector]("toolCalls")

// ToolCallsCollector The tool calls the collector
// Thread safety, used to collect tool call results during Agent execution
type ToolCallsCollector struct {
	mu    sync.Mutex
	calls []ToolCallResult
}

// NewToolCallsCollector creates a new tool call collector
func NewToolCallsCollector() *ToolCallsCollector {
	return &ToolCallsCollector{
		calls: make([]ToolCallResult, 0),
	}
}

// Add adds the tool to call the result
func (c *ToolCallsCollector) Add(call ToolCallResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

// Get all tool call results
func (c *ToolCallsCollector) Get() []ToolCallResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]ToolCallResult, len(c.calls))
	copy(result, c.calls)
	return result
}

// WithToolCallsCollector stores the tool call collector in context
func WithToolCallsCollector(ctx context.Context, collector *ToolCallsCollector) context.Context {
	return toolCallsKey.With(ctx, collector)
}

// GetToolCallsCollector Retrieves the tool call collector from the context
func GetToolCallsCollector(ctx context.Context) *ToolCallsCollector {
	c, _ := toolCallsKey.Get(ctx)
	return c
}

// AddToolCallResultToContext Adds the tool call result to the collector in the context
// This is a convenient function used to add results to the context after the tool is executed
func AddToolCallResultToContext(ctx context.Context, result *ToolCallResult) {
	collector := GetToolCallsCollector(ctx)
	if collector == nil {
		return
	}
	collector.Add(*result)
}

// GetToolCallResultsFromContext Retrieves all tool call results from the context
// This is a convenient function used to read the result of a tool call when building output
func GetToolCallResultsFromContext(ctx context.Context) []ToolCallResult {
	collector := GetToolCallsCollector(ctx)
	if collector == nil {
		return nil
	}
	return collector.Get()
}

// ============================================
// Aspect Interface - Aspect interface definition
// ============================================

// Aspect defines the base interface for implementing Aspect-Oriented Programming (AOP) for AI Agents.
// Aspect defines the basic interface for implementing Face-Facing Programming (AOP) for agents.
//
// Aspect provides cross-cutting functionality that can intercept and enhance agent execution
// without modifying the original business logic.
//
// The facet provides cross-cutting capabilities that can intercept and enhance agent execution without modifying the original business logic.
//
// Agent Aspect Categories:
// Intelligent Agent Section Categories:
//
//   - Agent Lifecycle Aspects: AgentStartAspect, AgentCompletedAspect
//     Agent lifecycle cross-section: AgentStartAspect, AgentCompletedAspect
//   - Agent Execution Aspects: AgentBeforeAspect, AgentAfterAspect, AgentAroundAspect
//     Agent execution face: AgentBeforeAspect, AgentAfterAspect, AgentAroundAspect
//   - Message Processing Aspects: MessageBeforeAspect, MessageAfterAspect
//     Message processing face: MessageBeforeAspect, MessageAfterAspect
//   - Stream Processing Aspects: StreamChunkAspect
//     Streaming processing aspect: StreamChunkAspect
//   - Tool Call Aspects: ToolCallBeforeAspect, ToolCallAfterAspect
//     Tool call aspects: ToolCallBeforeAspect, ToolCallAfterAspect
//
// Execution Order:
// Execution sequence:
//
//  1. Agent Level:
//     Agent Level:
//     AgentStart -> AgentBefore -> AgentAround -> [Agent Execution] -> AgentAfter -> AgentCompleted
//     Agent starts -> before agent executes -> agent surrounds -> [Agent executes] -> After agent executes -> agent completes
//
//  2. Inside Agent Execution Loop:
//     Inside the agent execution loop:
//     MessageBefore -> [LLM Generate/Stream] -> StreamChunk -> ToolCallBefore -> [Tool Execution] -> ToolCallAfter
//     Before message processing -> [LLM generation/stream] -> Stream block -> Before tool call -> [Tool executes] -> After tool call
type Aspect interface {
	// Order returns the execution priority of the aspect.
	// Lower values indicate higher priority and earlier execution in the aspect chain.
	//
	// Order returns the execution priority of the facet.
	// Smaller values indicate higher priority and earlier execution in the faceted chain.
	//
	// Returns:
	// Returns:
	//   - int: Priority value, lower numbers execute first
	//     int: priority value; smaller numbers are executed first
	Order() int

	// New creates a new instance of the aspect for a specific agent instance.
	// This ensures proper isolation between different agent instances.
	//
	// New creates a new instance of a facet for a specific agent instance.
	// This ensures proper isolation between different agent instances.
	//
	// Implementation Requirements:
	// Implementation requirements:
	//   - Create a completely independent instance
	//     Create completely independent instances
	//   - Copy necessary configuration
	//     Copy the necessary configurations
	//   - Ensure no shared mutable state between instances
	//     Ensure that there is no mutable state shared between instances
	//
	// Returns:
	// Returns:
	//   - Aspect: New aspect instance for the agent
	//     Aspect: A new facet example of an agent
	New() Aspect
}

// PointCut defines the interface for determining whether an aspect should be applied
// based on runtime conditions.
//
// PointCut defines an interface used to determine whether to apply a facet based on runtime conditions.
type PointCut interface {
	// PointCut determines whether this aspect should be applied to a specific execution point.
	// This method enables selective aspect application based on runtime conditions.
	//
	// PointCut determines whether this section should be applied to a specific execution point.
	// This method enables selective facet applications based on runtime conditions.
	//
	// Parameters:
	// Parameters:
	//   - ctx: Execution context
	//     ctx: Execute context
	//   - point: Agent execution point information
	//     point: Information about the agent's execution point
	//
	// Returns:
	// Returns:
	//   - bool: true to apply aspect, false to skip
	//     bool:true applies the facet, false skips
	PointCut(ctx context.Context, point *AgentPoint) bool
}

// AgentPoint represents the execution point information for aspect interception.
// It contains metadata about the current agent execution context.
//
// AgentPoint represents the execution point information for the section interception.
// It contains metadata about the current agent execution context.
type AgentPoint struct {
	AgentId     string            // Agent ID
	AgentName   string            // Agent name
	AgentType   string            // Agent type (react, deep, etc.)
	ThreadId    string            // Session thread ID
	UserId      string            // User ID
	MessageType string            // Message type
	ToolName    string            // Tool name (during tool calls)
	Metadata    map[string]string // Additional metadata
}

// AgentInput represents the input to an agent execution.
//
// AgentInput represents the input executed by the agent.
type AgentInput struct {
	Messages         []*schema.Message // Current messages
	SystemPrompt     string            // System prompt
	Context          map[string]any    // Execution context
	Metadata         map[string]string // Input metadata
	SessionKey       string            // Session key for conversation history
	HistoryMessages  []*schema.Message // Historical messages (populated by session aspect)
	OriginalMessages []*schema.Message // Original input messages (before history merge)
}

// AgentOutput represents the output from an agent execution.
//
// AgentOutput represents the output executed by the agent.
type AgentOutput struct {
	Content          string            // Response content
	Messages         []*schema.Message // Complete message history
	OriginalMessages []*schema.Message // Original input messages for session saving
	ToolCalls        []ToolCallResult  // Tool call records
	TokenUsage       TokenUsage        // Token usage statistics / Token usage statistics
	Duration         int64             // Execution duration in milliseconds
	Metadata         map[string]any    // Output metadata
	SessionKey       string            // Session key
	IsSuccess        bool              // Execution success status
	Error            error             // Error if failed
	SkippedAI        bool              // Whether AI processing was skipped (e.g., by Around aspect interception)
}

// TokenUsage represents token usage statistics for an agent execution.
//
// TokenUsage represents the token usage statistics executed by the agent.
type TokenUsage struct {
	PromptTokens     int // Input tokens
	CompletionTokens int // Output tokens
	TotalTokens      int // Total tokens
	CachedTokens     int // Cached prompt tokens
}

// ToolCallInfo represents information about a tool call before execution.
//
// ToolCallInfo represents information before the tool call is executed.
type ToolCallInfo struct {
	CallId    string    // Unique call ID
	Name      string    // Tool name
	Arguments string    // Tool arguments as JSON
	ToolType  ToolType  // Tool type: builtin/rulechain/subagent/mcp
	TargetId  string    // Target ID (rulechain ID or tool ID)
	StartTime time.Time // Call start time
}

// ToolCallResult represents the result of a tool call execution.
//
// ToolCallResult represents the result executed by the tool call.
type ToolCallResult struct {
	CallId    string    // Unique call ID
	Name      string    // Tool name
	Arguments string    // Tool call arguments (JSON)
	Result    string    // Tool execution result
	Error     error     // Error if failed
	Duration  int64     // Execution duration in milliseconds
	EndTime   time.Time // Call end time
}

// StreamChunk represents a chunk of streaming output from an agent.
//
// StreamChunk represents a stream output block from an agent.
type StreamChunk struct {
	Content    string    // Chunk content
	IsToolCall bool      // Whether this is a tool call chunk
	ToolName   string    // Tool name for tool call chunks
	ToolArgs   string    // Tool arguments for tool call chunks
	IsFinal    bool      // Whether this is the final chunk
	IsError    bool      // Whether this is an error chunk
	Timestamp  time.Time // Chunk timestamp
}

// AgentExecutor represents the agent execution function used by Around aspects.
//
// AgentExecutor represents the agent execution function used by the Around face.
type AgentExecutor func(ctx context.Context, input *AgentInput) (*AgentOutput, error)
