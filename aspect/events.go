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
	"time"

	"github.com/rulego/rulego-components-ai/utils/contextx"
)

// =============================================================================
// AG-UI Standard Event Types (https://docs.ag-ui.com/concepts/events)
// =============================================================================

// EventType AG-UI standard event type
type EventType string

const (
	// Lifecycle Events (Required)
	EventRunStarted   EventType = "RUN_STARTED"
	EventRunFinished  EventType = "RUN_FINISHED"
	EventRunError     EventType = "RUN_ERROR"
	EventStepStarted  EventType = "STEP_STARTED"
	EventStepFinished EventType = "STEP_FINISHED"

	// Text message event (streaming)
	EventTextMessageStart   EventType = "TEXT_MESSAGE_START"
	EventTextMessageContent EventType = "TEXT_MESSAGE_CONTENT"
	EventTextMessageEnd     EventType = "TEXT_MESSAGE_END"

	// Tool calls events
	EventToolCallStart  EventType = "TOOL_CALL_START"
	EventToolCallArgs   EventType = "TOOL_CALL_ARGS"
	EventToolCallEnd    EventType = "TOOL_CALL_END"
	EventToolCallResult EventType = "TOOL_CALL_RESULT"

	// Thought Process Events (optional)
	EventThinkingStart              EventType = "THINKING_START"
	EventThinkingEnd                EventType = "THINKING_END"
	EventThinkingTextMessageStart   EventType = "THINKING_TEXT_MESSAGE_START"
	EventThinkingTextMessageContent EventType = "THINKING_TEXT_MESSAGE_CONTENT"
	EventThinkingTextMessageEnd     EventType = "THINKING_TEXT_MESSAGE_END"

	// State management events
	EventStateSnapshot    EventType = "STATE_SNAPSHOT"
	EventStateDelta       EventType = "STATE_DELTA"
	EventMessagesSnapshot EventType = "MESSAGES_SNAPSHOT"
)

// =============================================================================
// Tool type: constant
// =============================================================================

// ToolType Tool/Call Type
type ToolType string

const (
	// ToolTypeBuiltin built-in tool
	ToolTypeBuiltin ToolType = "builtin"
	// ToolTypeRuleChain Rule chain tool
	ToolTypeRuleChain ToolType = "rulechain"
	// ToolTypeSubAgent sub-agent
	ToolTypeSubAgent ToolType = "subagent"
	// ToolTypeMCP MCP tool
	ToolTypeMCP ToolType = "mcp"
	// ToolTypeUnknown Unknown type
	ToolTypeUnknown ToolType = "unknown"
)

// =============================================================================
// AG-UI standard event structure
// =============================================================================

// Event: The common interface for all events
type Event interface {
	GetType() EventType
	GetTimestamp() int64
}

// BaseEvent is a common property of all events
type BaseEvent struct {
	Type      EventType `json:"type"`
	Timestamp int64     `json:"timestamp"`
}

func (e *BaseEvent) GetType() EventType   { return e.Type }
func (e *BaseEvent) GetTimestamp() int64  { return e.Timestamp }
func (e *BaseEvent) SetTimestamp(t int64) { e.Timestamp = t }

// RunStartedEvent RUN_STARTED Event - Indicates the start of the Agent run
type RunStartedEvent struct {
	BaseEvent
	ThreadId    string                 `json:"threadId"`
	RunId       string                 `json:"runId"`
	ParentRunId string                 `json:"parentRunId,omitempty"`
	Input       map[string]interface{} `json:"input,omitempty"`
}

// RunFinishedEvent RUN_FINISHED Event - Indicates the agent has successfully completed its run
type RunFinishedEvent struct {
	BaseEvent
	ThreadId string      `json:"threadId"`
	RunId    string      `json:"runId"`
	Result   interface{} `json:"result,omitempty"`
}

// RunErrorEvent RUN_ERROR Event - indicates an error in the Agent run
type RunErrorEvent struct {
	BaseEvent
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// StepStartedEvent STEP_STARTED Event - Indicates the start of the step
type StepStartedEvent struct {
	BaseEvent
	StepName string `json:"stepName"`
}

// StepFinishedEvent STEP_FINISHED Event - Indicates the step is complete
type StepFinishedEvent struct {
	BaseEvent
	StepName string `json:"stepName"`
}

// TextMessageStartEvent TEXT_MESSAGE_START Event - Indicates the start of a text message
type TextMessageStartEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
	Role      string `json:"role"` // "assistant" | "user" | "system" | "developer" | "tool"
}

// TextMessageContentEvent TEXT_MESSAGE_CONTENT Event - Represents incremental content of a text message
type TextMessageContentEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
	Delta     string `json:"delta"`
}

// TextMessageEndEvent TEXT_MESSAGE_END Event - Indicates the end of a text message
type TextMessageEndEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
}

// ToolCallStartEvent TOOL_CALL_START Event - Indicates the start of the tool call
type ToolCallStartEvent struct {
	BaseEvent
	ToolCallId      string   `json:"toolCallId"`
	ToolCallName    string   `json:"toolCallName"`
	ToolType        ToolType `json:"toolType,omitempty"` // Tool type: builtin/rulechain/subagent/mcp
	TargetId        string   `json:"targetId,omitempty"` // Target ID (Rule Chain ID or Tool ID)
	ParentMessageId string   `json:"parentMessageId,omitempty"`
}

// ToolCallArgsEvent TOOL_CALL_ARGS Event - Indicates the incremental parameter of the tool call
type ToolCallArgsEvent struct {
	BaseEvent
	ToolCallId string `json:"toolCallId"`
	Delta      string `json:"delta"`
}

// ToolCallEndEvent TOOL_CALL_END Event - Indicates that the tool call parameters have been transferred
type ToolCallEndEvent struct {
	BaseEvent
	ToolCallId string `json:"toolCallId"`
}

// ToolCallResultEvent TOOL_CALL_RESULT Event - Indicates the result of the tool call
type ToolCallResultEvent struct {
	BaseEvent
	ToolCallId string `json:"toolCallId"`
	Content    string `json:"content"`
	MessageId  string `json:"messageId,omitempty"`
	Role       string `json:"role,omitempty"`
}

// ThinkingStartEvent THINKING_START Event - Indicates the start of the thinking process
type ThinkingStartEvent struct {
	BaseEvent
}

// ThinkingEndEvent THINKING_END Event - Indicates the end of the thinking process
type ThinkingEndEvent struct {
	BaseEvent
}

// ThinkingTextMessageStartEvent THINKING_TEXT_MESSAGE_START event
type ThinkingTextMessageStartEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
}

// ThinkingTextMessageContentEvent THINKING_TEXT_MESSAGE_CONTENT event
type ThinkingTextMessageContentEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
	Delta     string `json:"delta"`
}

// ThinkingTextMessageEndEvent THINKING_TEXT_MESSAGE_END event
type ThinkingTextMessageEndEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
}

// StateSnapshotEvent STATE_SNAPSHOT Event - Represents a complete state snapshot
type StateSnapshotEvent struct {
	BaseEvent
	Snapshot interface{} `json:"snapshot"`
}

// StateDeltaEvent STATE_DELTA Event - Indicates state incremental updates (JSON Patch RFC 6902)
type StateDeltaEvent struct {
	BaseEvent
	Delta []JsonPatchOperation `json:"delta"`
}

// MessagesSnapshotEvent MESSAGES_SNAPSHOT Event - Represents a message list snapshot
type MessagesSnapshotEvent struct {
	BaseEvent
	Messages []MessageState `json:"messages"`
}

// =============================================================================
// Auxiliary data structures
// =============================================================================

// JsonPatchOperation JSON Patch Operation (RFC 6902)
type JsonPatchOperation struct {
	Op    string      `json:"op"`              // "add" | "remove" | "replace" | "move" | "copy" | "test"
	Path  string      `json:"path"`            // JSON Pointer path
	Value interface{} `json:"value,omitempty"` // The value of the operation
	From  string      `json:"from,omitempty"`  // The source path of the move/copy operation
}

// MessageState message status
type MessageState struct {
	Id        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"createdAt"`
}

// =============================================================================
// EventEmitter interface
// =============================================================================

// EventEmitter AG-UI standard event transmitter interface
type EventEmitter interface {
	// Lifecycle events
	EmitRunStarted(threadId, runId string, parentRunId string, input map[string]interface{})
	EmitRunFinished(threadId, runId string, result interface{})
	EmitRunError(message string, code string)

	// Step events
	EmitStepStarted(stepName string)
	EmitStepFinished(stepName string)

	// Text message event (streaming)
	EmitTextMessageStart(messageId, role string)
	EmitTextMessageContent(messageId, delta string)
	EmitTextMessageEnd(messageId string)

	// Tool calls events
	EmitToolCallStart(toolCallId, toolCallName string, toolType ToolType, targetId, parentMessageId string)
	EmitToolCallArgs(toolCallId, delta string)
	EmitToolCallEnd(toolCallId string)
	EmitToolCallResult(toolCallId, content, messageId string)

	// Thinking about the process of events
	EmitThinkingStart()
	EmitThinkingContent(messageId, delta string)
	EmitThinkingEnd()

	// State management events
	EmitStateSnapshot(snapshot interface{})
	EmitStateDelta(delta []JsonPatchOperation)
}

// =============================================================================
// Context Utility Function - Uses generic Keys
// =============================================================================

// emitterKey stores EventEmitter in context
var emitterKey = contextx.NewKey[EventEmitter]("emitter")

// GetEmitter retrieves the event emitter from Context
func GetEmitter(ctx context.Context) (EventEmitter, bool) {
	return emitterKey.Get(ctx)
}

// WithEmitter adds an event emitter to the Context
func WithEmitter(ctx context.Context, emitter EventEmitter) context.Context {
	return emitterKey.With(ctx, emitter)
}

// messageIDKey stores message ID in context
var messageIDKey = contextx.NewKey[string]("messageId")

// GetMessageID retrieves the current message ID from the context
func GetMessageID(ctx context.Context) string {
	msgId, _ := messageIDKey.Get(ctx)
	return msgId
}

// WithMessageID Adds the current message ID to the Context
func WithMessageID(ctx context.Context, msgId string) context.Context {
	return messageIDKey.With(ctx, msgId)
}

// =============================================================================
// Auxiliary function
// =============================================================================

// NewBaseEvent creates a base event
func NewBaseEvent(eventType EventType) BaseEvent {
	return BaseEvent{
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewRunStartedEvent creates RUN_STARTED events
func NewRunStartedEvent(threadId, runId, parentRunId string, input map[string]interface{}) *RunStartedEvent {
	return &RunStartedEvent{
		BaseEvent:   NewBaseEvent(EventRunStarted),
		ThreadId:    threadId,
		RunId:       runId,
		ParentRunId: parentRunId,
		Input:       input,
	}
}

// NewRunFinishedEvent creates RUN_FINISHED events
func NewRunFinishedEvent(threadId, runId string, result interface{}) *RunFinishedEvent {
	return &RunFinishedEvent{
		BaseEvent: NewBaseEvent(EventRunFinished),
		ThreadId:  threadId,
		RunId:     runId,
		Result:    result,
	}
}

// NewRunErrorEvent creates RUN_ERROR events
func NewRunErrorEvent(message, code string) *RunErrorEvent {
	return &RunErrorEvent{
		BaseEvent: NewBaseEvent(EventRunError),
		Message:   message,
		Code:      code,
	}
}

// NewStepStartedEvent creates STEP_STARTED events
func NewStepStartedEvent(stepName string) *StepStartedEvent {
	return &StepStartedEvent{
		BaseEvent: NewBaseEvent(EventStepStarted),
		StepName:  stepName,
	}
}

// NewStepFinishedEvent creates STEP_FINISHED events
func NewStepFinishedEvent(stepName string) *StepFinishedEvent {
	return &StepFinishedEvent{
		BaseEvent: NewBaseEvent(EventStepFinished),
		StepName:  stepName,
	}
}

// NewTextMessageStartEvent creates TEXT_MESSAGE_START events
func NewTextMessageStartEvent(messageId, role string) *TextMessageStartEvent {
	return &TextMessageStartEvent{
		BaseEvent: NewBaseEvent(EventTextMessageStart),
		MessageId: messageId,
		Role:      role,
	}
}

// NewTextMessageContentEvent creates TEXT_MESSAGE_CONTENT events
func NewTextMessageContentEvent(messageId, delta string) *TextMessageContentEvent {
	return &TextMessageContentEvent{
		BaseEvent: NewBaseEvent(EventTextMessageContent),
		MessageId: messageId,
		Delta:     delta,
	}
}

// NewTextMessageEndEvent creates TEXT_MESSAGE_END events
func NewTextMessageEndEvent(messageId string) *TextMessageEndEvent {
	return &TextMessageEndEvent{
		BaseEvent: NewBaseEvent(EventTextMessageEnd),
		MessageId: messageId,
	}
}

// NewToolCallStartEvent creates TOOL_CALL_START events
func NewToolCallStartEvent(toolCallId, toolCallName string, toolType ToolType, targetId, parentMessageId string) *ToolCallStartEvent {
	return &ToolCallStartEvent{
		BaseEvent:       NewBaseEvent(EventToolCallStart),
		ToolCallId:      toolCallId,
		ToolCallName:    toolCallName,
		ToolType:        toolType,
		TargetId:        targetId,
		ParentMessageId: parentMessageId,
	}
}

// NewToolCallArgsEvent creates TOOL_CALL_ARGS events
func NewToolCallArgsEvent(toolCallId, delta string) *ToolCallArgsEvent {
	return &ToolCallArgsEvent{
		BaseEvent:  NewBaseEvent(EventToolCallArgs),
		ToolCallId: toolCallId,
		Delta:      delta,
	}
}

// NewToolCallEndEvent creates TOOL_CALL_END events
func NewToolCallEndEvent(toolCallId string) *ToolCallEndEvent {
	return &ToolCallEndEvent{
		BaseEvent:  NewBaseEvent(EventToolCallEnd),
		ToolCallId: toolCallId,
	}
}

// NewToolCallResultEvent creates TOOL_CALL_RESULT events
func NewToolCallResultEvent(toolCallId, content, messageId string) *ToolCallResultEvent {
	return &ToolCallResultEvent{
		BaseEvent:  NewBaseEvent(EventToolCallResult),
		ToolCallId: toolCallId,
		Content:    content,
		MessageId:  messageId,
	}
}

// NewThinkingStartEvent creates THINKING_START events
func NewThinkingStartEvent() *ThinkingStartEvent {
	return &ThinkingStartEvent{
		BaseEvent: NewBaseEvent(EventThinkingStart),
	}
}

// NewThinkingEndEvent creates THINKING_END events
func NewThinkingEndEvent() *ThinkingEndEvent {
	return &ThinkingEndEvent{
		BaseEvent: NewBaseEvent(EventThinkingEnd),
	}
}

// NewStateSnapshotEvent creates STATE_SNAPSHOT events
func NewStateSnapshotEvent(snapshot interface{}) *StateSnapshotEvent {
	return &StateSnapshotEvent{
		BaseEvent: NewBaseEvent(EventStateSnapshot),
		Snapshot:  snapshot,
	}
}

// NewStateDeltaEvent creates STATE_DELTA events
func NewStateDeltaEvent(delta []JsonPatchOperation) *StateDeltaEvent {
	return &StateDeltaEvent{
		BaseEvent: NewBaseEvent(EventStateDelta),
		Delta:     delta,
	}
}
