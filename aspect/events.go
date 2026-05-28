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
// AG-UI 标准事件类型 (https://docs.ag-ui.com/concepts/events)
// =============================================================================

// EventType AG-UI 标准事件类型
type EventType string

const (
	// 生命周期事件（必需）
	EventRunStarted   EventType = "RUN_STARTED"
	EventRunFinished  EventType = "RUN_FINISHED"
	EventRunError     EventType = "RUN_ERROR"
	EventStepStarted  EventType = "STEP_STARTED"
	EventStepFinished EventType = "STEP_FINISHED"

	// 文本消息事件（流式）
	EventTextMessageStart   EventType = "TEXT_MESSAGE_START"
	EventTextMessageContent EventType = "TEXT_MESSAGE_CONTENT"
	EventTextMessageEnd     EventType = "TEXT_MESSAGE_END"

	// 工具调用事件
	EventToolCallStart  EventType = "TOOL_CALL_START"
	EventToolCallArgs   EventType = "TOOL_CALL_ARGS"
	EventToolCallEnd    EventType = "TOOL_CALL_END"
	EventToolCallResult EventType = "TOOL_CALL_RESULT"

	// 思考过程事件（可选）
	EventThinkingStart              EventType = "THINKING_START"
	EventThinkingEnd                EventType = "THINKING_END"
	EventThinkingTextMessageStart   EventType = "THINKING_TEXT_MESSAGE_START"
	EventThinkingTextMessageContent EventType = "THINKING_TEXT_MESSAGE_CONTENT"
	EventThinkingTextMessageEnd     EventType = "THINKING_TEXT_MESSAGE_END"

	// 状态管理事件
	EventStateSnapshot    EventType = "STATE_SNAPSHOT"
	EventStateDelta       EventType = "STATE_DELTA"
	EventMessagesSnapshot EventType = "MESSAGES_SNAPSHOT"
)

// =============================================================================
// 工具类型常量
// =============================================================================

// ToolType 工具/调用类型
type ToolType string

const (
	// ToolTypeBuiltin 内置工具
	ToolTypeBuiltin ToolType = "builtin"
	// ToolTypeRuleChain 规则链工具
	ToolTypeRuleChain ToolType = "rulechain"
	// ToolTypeSubAgent 子智能体
	ToolTypeSubAgent ToolType = "subagent"
	// ToolTypeMCP MCP 工具
	ToolTypeMCP ToolType = "mcp"
	// ToolTypeUnknown 未知类型
	ToolTypeUnknown ToolType = "unknown"
)

// =============================================================================
// AG-UI 标准事件结构
// =============================================================================

// Event 所有事件的公共接口
type Event interface {
	GetType() EventType
	GetTimestamp() int64
}

// BaseEvent 所有事件的公共属性
type BaseEvent struct {
	Type      EventType `json:"type"`
	Timestamp int64     `json:"timestamp"`
}

func (e *BaseEvent) GetType() EventType   { return e.Type }
func (e *BaseEvent) GetTimestamp() int64  { return e.Timestamp }
func (e *BaseEvent) SetTimestamp(t int64) { e.Timestamp = t }

// RunStartedEvent RUN_STARTED 事件 - 表示 Agent 运行开始
type RunStartedEvent struct {
	BaseEvent
	ThreadId    string                 `json:"threadId"`
	RunId       string                 `json:"runId"`
	ParentRunId string                 `json:"parentRunId,omitempty"`
	Input       map[string]interface{} `json:"input,omitempty"`
}

// RunFinishedEvent RUN_FINISHED 事件 - 表示 Agent 运行成功完成
type RunFinishedEvent struct {
	BaseEvent
	ThreadId string      `json:"threadId"`
	RunId    string      `json:"runId"`
	Result   interface{} `json:"result,omitempty"`
}

// RunErrorEvent RUN_ERROR 事件 - 表示 Agent 运行出错
type RunErrorEvent struct {
	BaseEvent
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// StepStartedEvent STEP_STARTED 事件 - 表示步骤开始
type StepStartedEvent struct {
	BaseEvent
	StepName string `json:"stepName"`
}

// StepFinishedEvent STEP_FINISHED 事件 - 表示步骤完成
type StepFinishedEvent struct {
	BaseEvent
	StepName string `json:"stepName"`
}

// TextMessageStartEvent TEXT_MESSAGE_START 事件 - 表示文本消息开始
type TextMessageStartEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
	Role      string `json:"role"` // "assistant" | "user" | "system" | "developer" | "tool"
}

// TextMessageContentEvent TEXT_MESSAGE_CONTENT 事件 - 表示文本消息增量内容
type TextMessageContentEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
	Delta     string `json:"delta"`
}

// TextMessageEndEvent TEXT_MESSAGE_END 事件 - 表示文本消息结束
type TextMessageEndEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
}

// ToolCallStartEvent TOOL_CALL_START 事件 - 表示工具调用开始
type ToolCallStartEvent struct {
	BaseEvent
	ToolCallId      string   `json:"toolCallId"`
	ToolCallName    string   `json:"toolCallName"`
	ToolType        ToolType `json:"toolType,omitempty"` // 工具类型：builtin/rulechain/subagent/mcp
	TargetId        string   `json:"targetId,omitempty"` // 目标 ID（规则链 ID 或工具 ID）
	ParentMessageId string   `json:"parentMessageId,omitempty"`
}

// ToolCallArgsEvent TOOL_CALL_ARGS 事件 - 表示工具调用参数增量
type ToolCallArgsEvent struct {
	BaseEvent
	ToolCallId string `json:"toolCallId"`
	Delta      string `json:"delta"`
}

// ToolCallEndEvent TOOL_CALL_END 事件 - 表示工具调用参数传输完成
type ToolCallEndEvent struct {
	BaseEvent
	ToolCallId string `json:"toolCallId"`
}

// ToolCallResultEvent TOOL_CALL_RESULT 事件 - 表示工具调用结果
type ToolCallResultEvent struct {
	BaseEvent
	ToolCallId string `json:"toolCallId"`
	Content    string `json:"content"`
	MessageId  string `json:"messageId,omitempty"`
	Role       string `json:"role,omitempty"`
}

// ThinkingStartEvent THINKING_START 事件 - 表示思考过程开始
type ThinkingStartEvent struct {
	BaseEvent
}

// ThinkingEndEvent THINKING_END 事件 - 表示思考过程结束
type ThinkingEndEvent struct {
	BaseEvent
}

// ThinkingTextMessageStartEvent THINKING_TEXT_MESSAGE_START 事件
type ThinkingTextMessageStartEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
}

// ThinkingTextMessageContentEvent THINKING_TEXT_MESSAGE_CONTENT 事件
type ThinkingTextMessageContentEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
	Delta     string `json:"delta"`
}

// ThinkingTextMessageEndEvent THINKING_TEXT_MESSAGE_END 事件
type ThinkingTextMessageEndEvent struct {
	BaseEvent
	MessageId string `json:"messageId"`
}

// StateSnapshotEvent STATE_SNAPSHOT 事件 - 表示完整状态快照
type StateSnapshotEvent struct {
	BaseEvent
	Snapshot interface{} `json:"snapshot"`
}

// StateDeltaEvent STATE_DELTA 事件 - 表示状态增量更新 (JSON Patch RFC 6902)
type StateDeltaEvent struct {
	BaseEvent
	Delta []JsonPatchOperation `json:"delta"`
}

// MessagesSnapshotEvent MESSAGES_SNAPSHOT 事件 - 表示消息列表快照
type MessagesSnapshotEvent struct {
	BaseEvent
	Messages []MessageState `json:"messages"`
}

// =============================================================================
// 辅助数据结构
// =============================================================================

// JsonPatchOperation JSON Patch 操作 (RFC 6902)
type JsonPatchOperation struct {
	Op    string      `json:"op"`              // "add" | "remove" | "replace" | "move" | "copy" | "test"
	Path  string      `json:"path"`            // JSON Pointer 路径
	Value interface{} `json:"value,omitempty"` // 操作的值
	From  string      `json:"from,omitempty"`  // move/copy 操作的源路径
}

// MessageState 消息状态
type MessageState struct {
	Id        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"createdAt"`
}

// =============================================================================
// EventEmitter 接口
// =============================================================================

// EventEmitter AG-UI 标准事件发射器接口
type EventEmitter interface {
	// 生命周期事件
	EmitRunStarted(threadId, runId string, parentRunId string, input map[string]interface{})
	EmitRunFinished(threadId, runId string, result interface{})
	EmitRunError(message string, code string)

	// 步骤事件
	EmitStepStarted(stepName string)
	EmitStepFinished(stepName string)

	// 文本消息事件（流式）
	EmitTextMessageStart(messageId, role string)
	EmitTextMessageContent(messageId, delta string)
	EmitTextMessageEnd(messageId string)

	// 工具调用事件
	EmitToolCallStart(toolCallId, toolCallName string, toolType ToolType, targetId, parentMessageId string)
	EmitToolCallArgs(toolCallId, delta string)
	EmitToolCallEnd(toolCallId string)
	EmitToolCallResult(toolCallId, content, messageId string)

	// 思考过程事件
	EmitThinkingStart()
	EmitThinkingContent(messageId, delta string)
	EmitThinkingEnd()

	// 状态管理事件
	EmitStateSnapshot(snapshot interface{})
	EmitStateDelta(delta []JsonPatchOperation)
}

// =============================================================================
// Context 工具函数 - 使用泛型 Key
// =============================================================================

// emitterKey stores EventEmitter in context
var emitterKey = contextx.NewKey[EventEmitter]("emitter")

// GetEmitter 从 Context 获取事件发射器
func GetEmitter(ctx context.Context) (EventEmitter, bool) {
	return emitterKey.Get(ctx)
}

// WithEmitter 添加事件发射器到 Context
func WithEmitter(ctx context.Context, emitter EventEmitter) context.Context {
	return emitterKey.With(ctx, emitter)
}

// messageIDKey stores message ID in context
var messageIDKey = contextx.NewKey[string]("messageId")

// GetMessageID 从 Context 获取当前消息 ID
func GetMessageID(ctx context.Context) string {
	msgId, _ := messageIDKey.Get(ctx)
	return msgId
}

// WithMessageID 添加当前消息 ID 到 Context
func WithMessageID(ctx context.Context, msgId string) context.Context {
	return messageIDKey.With(ctx, msgId)
}

// =============================================================================
// 辅助函数
// =============================================================================

// NewBaseEvent 创建基础事件
func NewBaseEvent(eventType EventType) BaseEvent {
	return BaseEvent{
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewRunStartedEvent 创建 RUN_STARTED 事件
func NewRunStartedEvent(threadId, runId, parentRunId string, input map[string]interface{}) *RunStartedEvent {
	return &RunStartedEvent{
		BaseEvent:   NewBaseEvent(EventRunStarted),
		ThreadId:    threadId,
		RunId:       runId,
		ParentRunId: parentRunId,
		Input:       input,
	}
}

// NewRunFinishedEvent 创建 RUN_FINISHED 事件
func NewRunFinishedEvent(threadId, runId string, result interface{}) *RunFinishedEvent {
	return &RunFinishedEvent{
		BaseEvent: NewBaseEvent(EventRunFinished),
		ThreadId:  threadId,
		RunId:     runId,
		Result:    result,
	}
}

// NewRunErrorEvent 创建 RUN_ERROR 事件
func NewRunErrorEvent(message, code string) *RunErrorEvent {
	return &RunErrorEvent{
		BaseEvent: NewBaseEvent(EventRunError),
		Message:   message,
		Code:      code,
	}
}

// NewStepStartedEvent 创建 STEP_STARTED 事件
func NewStepStartedEvent(stepName string) *StepStartedEvent {
	return &StepStartedEvent{
		BaseEvent: NewBaseEvent(EventStepStarted),
		StepName:  stepName,
	}
}

// NewStepFinishedEvent 创建 STEP_FINISHED 事件
func NewStepFinishedEvent(stepName string) *StepFinishedEvent {
	return &StepFinishedEvent{
		BaseEvent: NewBaseEvent(EventStepFinished),
		StepName:  stepName,
	}
}

// NewTextMessageStartEvent 创建 TEXT_MESSAGE_START 事件
func NewTextMessageStartEvent(messageId, role string) *TextMessageStartEvent {
	return &TextMessageStartEvent{
		BaseEvent: NewBaseEvent(EventTextMessageStart),
		MessageId: messageId,
		Role:      role,
	}
}

// NewTextMessageContentEvent 创建 TEXT_MESSAGE_CONTENT 事件
func NewTextMessageContentEvent(messageId, delta string) *TextMessageContentEvent {
	return &TextMessageContentEvent{
		BaseEvent: NewBaseEvent(EventTextMessageContent),
		MessageId: messageId,
		Delta:     delta,
	}
}

// NewTextMessageEndEvent 创建 TEXT_MESSAGE_END 事件
func NewTextMessageEndEvent(messageId string) *TextMessageEndEvent {
	return &TextMessageEndEvent{
		BaseEvent: NewBaseEvent(EventTextMessageEnd),
		MessageId: messageId,
	}
}

// NewToolCallStartEvent 创建 TOOL_CALL_START 事件
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

// NewToolCallArgsEvent 创建 TOOL_CALL_ARGS 事件
func NewToolCallArgsEvent(toolCallId, delta string) *ToolCallArgsEvent {
	return &ToolCallArgsEvent{
		BaseEvent:  NewBaseEvent(EventToolCallArgs),
		ToolCallId: toolCallId,
		Delta:      delta,
	}
}

// NewToolCallEndEvent 创建 TOOL_CALL_END 事件
func NewToolCallEndEvent(toolCallId string) *ToolCallEndEvent {
	return &ToolCallEndEvent{
		BaseEvent:  NewBaseEvent(EventToolCallEnd),
		ToolCallId: toolCallId,
	}
}

// NewToolCallResultEvent 创建 TOOL_CALL_RESULT 事件
func NewToolCallResultEvent(toolCallId, content, messageId string) *ToolCallResultEvent {
	return &ToolCallResultEvent{
		BaseEvent:  NewBaseEvent(EventToolCallResult),
		ToolCallId: toolCallId,
		Content:    content,
		MessageId:  messageId,
	}
}

// NewThinkingStartEvent 创建 THINKING_START 事件
func NewThinkingStartEvent() *ThinkingStartEvent {
	return &ThinkingStartEvent{
		BaseEvent: NewBaseEvent(EventThinkingStart),
	}
}

// NewThinkingEndEvent 创建 THINKING_END 事件
func NewThinkingEndEvent() *ThinkingEndEvent {
	return &ThinkingEndEvent{
		BaseEvent: NewBaseEvent(EventThinkingEnd),
	}
}

// NewStateSnapshotEvent 创建 STATE_SNAPSHOT 事件
func NewStateSnapshotEvent(snapshot interface{}) *StateSnapshotEvent {
	return &StateSnapshotEvent{
		BaseEvent: NewBaseEvent(EventStateSnapshot),
		Snapshot:  snapshot,
	}
}

// NewStateDeltaEvent 创建 STATE_DELTA 事件
func NewStateDeltaEvent(delta []JsonPatchOperation) *StateDeltaEvent {
	return &StateDeltaEvent{
		BaseEvent: NewBaseEvent(EventStateDelta),
		Delta:     delta,
	}
}
