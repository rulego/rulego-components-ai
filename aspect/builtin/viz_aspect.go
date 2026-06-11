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

package builtin

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/rulego/rulego-components-ai/aspect"
)

// VizAspect 可视化切面
// 发送 AG-UI 标准事件用于智能体执行可视化
type VizAspect struct {
	order int
}

// NewVizAspect 创建可视化切面
func NewVizAspect() *VizAspect {
	return &VizAspect{
		order: 100,
	}
}

// Order 返回执行顺序
func (a *VizAspect) Order() int {
	return a.order
}

// New 创建切面的新实例
func (a *VizAspect) New() aspect.Aspect {
	return &VizAspect{
		order: a.order,
	}
}

// PointCut 始终应用此切面
func (a *VizAspect) PointCut(ctx context.Context, point *aspect.AgentPoint) bool {
	// 检查是否有 emitter
	_, ok := aspect.GetEmitterWithFallback(ctx, point.AgentId)
	return ok
}

// OnStart 发送 RUN_STARTED 事件
func (a *VizAspect) OnStart(ctx context.Context, point *aspect.AgentPoint, input *aspect.AgentInput) (*aspect.AgentInput, error) {
	emitter, ok := aspect.GetEmitterWithFallback(ctx, point.AgentId)
	if !ok {
		return input, nil
	}

	threadId := point.ThreadId
	if threadId == "" {
		threadId = input.Metadata["threadId"]
	}

	runId := fmt.Sprintf("agent_%s_%d", point.AgentId, time.Now().UnixNano())

	// 发送开始事件
	emitter.EmitRunStarted(threadId, runId, "", map[string]interface{}{
		"agentName": point.AgentName,
		"agentType": point.AgentType,
		"agentId":   point.AgentId,
	})

	// 发送输入消息事件
	// 只发送最新的用户消息（最后一条非系统消息），避免重复发送历史消息
	// 因为 OpenAI 等接口每次请求都会带完整的历史消息
	if input != nil && len(input.OriginalMessages) > 0 {
		// 找到最后一条用户消息
		var lastUserMsg *schema.Message
		for i := len(input.OriginalMessages) - 1; i >= 0; i-- {
			msg := input.OriginalMessages[i]
			if msg.Role != schema.System {
				lastUserMsg = msg
				break
			}
		}

		if lastUserMsg != nil {
			msgId := uuid.New().String()
			role := string(lastUserMsg.Role)
			if role == "" {
				role = "user"
			}
			emitter.EmitTextMessageStart(msgId, role)
			emitter.EmitTextMessageContent(msgId, lastUserMsg.Content)
			emitter.EmitTextMessageEnd(msgId)
		}
	}

	emitter.EmitStepStarted(point.AgentName)

	return input, nil
}

// OnCompleted 发送 RUN_FINISHED 事件
func (a *VizAspect) OnCompleted(ctx context.Context, point *aspect.AgentPoint, output *aspect.AgentOutput) {
	emitter, ok := aspect.GetEmitterWithFallback(ctx, point.AgentId)
	if !ok {
		return
	}

	emitter.EmitStepFinished(point.AgentName)

	// Check if we were streaming
	msgId := point.Metadata["_viz_msg_id"]
	if msgId != "" {
		// Streaming finished
		emitter.EmitTextMessageEnd(msgId)
	} else if output != nil && output.Content != "" {
		// Non-streaming completion, emit full content
		msgId = uuid.New().String()
		emitter.EmitTextMessageStart(msgId, "assistant")
		emitter.EmitTextMessageContent(msgId, output.Content)
		emitter.EmitTextMessageEnd(msgId)
	}

	metadata := map[string]interface{}{
		"result": output.Content,
	}

	if output.Duration > 0 {
		metadata["duration"] = output.Duration
	}

	if output.TokenUsage.TotalTokens > 0 {
		metadata["tokens"] = map[string]interface{}{
			"prompt":     output.TokenUsage.PromptTokens,
			"completion": output.TokenUsage.CompletionTokens,
			"total":      output.TokenUsage.TotalTokens,
		}
	}

	if output.Error != nil {
		emitter.EmitRunError(output.Error.Error(), "AGENT_ERROR")
	}

	emitter.EmitRunFinished("", "", metadata)
}

// OnChunk 发送流式内容事件
func (a *VizAspect) OnChunk(ctx context.Context, point *aspect.AgentPoint, chunk *aspect.StreamChunk) error {
	emitter, ok := aspect.GetEmitterWithFallback(ctx, point.AgentId)
	if !ok {
		return nil
	}

	// Use point.Metadata to store message state
	msgId := point.Metadata["_viz_msg_id"]
	if msgId == "" {
		msgId = uuid.New().String()
		point.Metadata["_viz_msg_id"] = msgId
		// Emit start event for the assistant's response
		emitter.EmitTextMessageStart(msgId, "assistant")
	}

	emitter.EmitTextMessageContent(msgId, chunk.Content)
	return nil
}

// BeforeToolCall 发送工具调用开始事件
func (a *VizAspect) BeforeToolCall(ctx context.Context, point *aspect.AgentPoint, call *aspect.ToolCallInfo) (*aspect.ToolCallInfo, error) {
	emitter, ok := aspect.GetEmitterWithFallback(ctx, point.AgentId)
	if !ok {
		return call, nil
	}

	toolType := call.ToolType
	if toolType == "" {
		toolType = aspect.ToolTypeUnknown
	}

	// 获取当前消息 ID 作为 parentMessageId
	parentMessageId := point.Metadata["_viz_msg_id"]

	emitter.EmitToolCallStart(call.CallId, call.Name, toolType, call.TargetId, parentMessageId)
	emitter.EmitToolCallArgs(call.CallId, call.Arguments)

	return call, nil
}

// AfterToolCall 发送工具调用完成事件
func (a *VizAspect) AfterToolCall(ctx context.Context, point *aspect.AgentPoint, call *aspect.ToolCallInfo, result *aspect.ToolCallResult) error {
	emitter, ok := aspect.GetEmitterWithFallback(ctx, point.AgentId)
	if !ok {
		return nil
	}

	emitter.EmitToolCallResult(call.CallId, result.Result, "")

	if result.Error != nil {
		emitter.EmitRunError(result.Error.Error(), "TOOL_ERROR")
	}

	return nil
}
