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

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego-components-ai/session"
	"github.com/rulego/rulego-components-ai/utils/token"
	"github.com/rulego/rulego/api/types"
)

// ============================================
// SSE event handling
// ============================================

// SSEEventType SSE event type
type SSEEventType string

const (
	// SSEEventToolStart tool call begins
	SSEEventToolStart SSEEventType = "tool_start"
	// SSEEventToolResult tool call result
	SSEEventToolResult SSEEventType = "tool_result"
	// SSEEventToolError tool call error
	SSEEventToolError SSEEventType = "tool_error"
)

// SSECallback SSE callback function type
type SSECallback func(toolCallId, toolName, eventType, data string, index int)

// sseCallbackKey is used to store SSE callbacks in context
type sseCallbackKey struct{}

// GetSSECallback retrieves SSE callbacks from contexts
func GetSSECallback(ctx context.Context) SSECallback {
	if cb, ok := ctx.Value(sseCallbackKey{}).(SSECallback); ok {
		return cb
	}
	return nil
}

// WithSSECallback stores the SSE callback into context
func WithSSECallback(ctx context.Context, cb SSECallback) context.Context {
	return context.WithValue(ctx, sseCallbackKey{}, cb)
}

// SSEHandler SSE Event Processor
type SSEHandler struct {
	ctx     types.RuleContext
	msg     types.RuleMsg
	enabled bool
	mu      sync.Mutex
	queue   *StreamTellQueue // Non-space-time tool events are joined and unified with chunk for order preservation
}

// NewSSEHandler creates SSE processors
func NewSSEHandler(ctx types.RuleContext, msg types.RuleMsg) *SSEHandler {
	return &SSEHandler{
		ctx:     ctx,
		msg:     msg,
		enabled: msg.Metadata.GetValue(config.KeyStream) == config.ValueTrue,
	}
}

// IsEnabled returns whether SSE is enabled
func (h *SSEHandler) IsEnabled() bool {
	return h.enabled
}

// UseQueue sets the streaming TellNext queue: after setting, tool events are changed to queue and unified with chunk for order preservation.
// It must be called before the Callback injection (tool execution); The executeStream initialization phase ensures that happens-before does not require locking for the queue field.
func (h *SSEHandler) UseQueue(q *StreamTellQueue) {
	h.queue = q
}

// Callback returns an SSE callback function (used to inject into context)
func (h *SSEHandler) Callback() SSECallback {
	if !h.enabled {
		return nil
	}
	return h.sendEvent
}

// sendEvent sends an SSE event
func (h *SSEHandler) sendEvent(toolCallId, toolName, eventType, data string, index int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	timestamp := time.Now().UnixMilli()
	eventData := h.buildEventData(toolCallId, toolName, eventType, data, index, timestamp)

	chunkMsg := h.msg.Copy()
	chunkMsg.SetData(string(eventData))
	chunkMsg.DataType = types.TEXT
	chunkMsg.Metadata.PutValue(config.KeyChunk, config.ValueTrue)
	chunkMsg.Metadata.PutValue(config.KeyToolCall, config.ValueTrue)
	if h.queue != nil {
		h.queue.Enqueue(chunkMsg)
	} else {
		h.ctx.TellNext(chunkMsg, types.Stream)
	}
}

// buildEventData builds SSE event data
func (h *SSEHandler) buildEventData(toolCallId, toolName, eventType, data string, index int, timestamp int64) []byte {
	var eventData map[string]interface{}

	switch SSEEventType(eventType) {
	case SSEEventToolStart:
		eventData = map[string]interface{}{
			"type":         string(aspect.EventToolCallStart),
			"timestamp":    timestamp,
			"toolCallId":   toolCallId,
			"toolCallName": toolName,
			"index":        index,
		}
		// Parse and merge additional data
		var parsedData map[string]interface{}
		if err := json.Unmarshal([]byte(data), &parsedData); err == nil {
			if args, ok := parsedData["arguments"]; ok {
				eventData["arguments"] = args
			}
			if toolType, ok := parsedData["toolType"]; ok {
				eventData["toolType"] = toolType
			}
			if targetId, ok := parsedData["targetId"]; ok {
				eventData["targetId"] = targetId
			}
		}

	case SSEEventToolResult:
		eventData = map[string]interface{}{
			"type":         string(aspect.EventToolCallResult),
			"timestamp":    timestamp,
			"toolCallId":   toolCallId,
			"toolCallName": toolName,
			"content":      data,
			"index":        index,
		}

	case SSEEventToolError:
		eventData = map[string]interface{}{
			"type":         string(aspect.EventToolCallResult),
			"timestamp":    timestamp,
			"toolCallId":   toolCallId,
			"toolCallName": toolName,
			"content":      data,
			"error":        true,
			"index":        index,
		}
	}

	result, _ := json.Marshal(eventData)
	return result
}

// ============================================
// Visualize tool wrappers
// ============================================

// VisualToolWrapper
// Responsible for sending AG-UI visualization events and SSE stream events
type VisualToolWrapper struct {
	base                tool.InvokableTool
	name                string
	agentId             string
	agentName           string
	toolType            aspect.ToolType
	targetId            string
	aspectManager       *aspect.AspectManager
	maxStep             int
	maxToolOutputLength int
	logger              types.Logger
	callCounter         int32
	metricsCollector    *token.MetricsCollector
}

// NewVisualToolWrapper creates a visual tool wrapper
func NewVisualToolWrapper(base tool.InvokableTool, opts ToolWrapOptions) *VisualToolWrapper {
	return &VisualToolWrapper{
		base:                base,
		name:                opts.Name,
		agentId:             opts.AgentId,
		agentName:           opts.AgentName,
		toolType:            opts.ToolType,
		targetId:            opts.TargetId,
		aspectManager:       opts.AspectManager,
		maxStep:             opts.MaxStep,
		maxToolOutputLength: opts.MaxToolOutputLength,
		logger:              opts.Logger,
		metricsCollector:    opts.MetricsCollector,
	}
}

// Info returns tool information
func (w *VisualToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.base.Info(ctx)
}

// InvokableRun executes the tool and sends AG-UI visualization events and SSE stream events
func (w *VisualToolWrapper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (result string, err error) {
	// The tool executes panic without killing the entire server: after capture, it returns the error result to the agent (the agent sees the error and decides to retry/change method).
	// Occasional critical errors such as concurrent map in agent parallel tools cause process crashes (one of the two root causes of repeated server exits).
	defer func() {
		if r := recover(); r != nil {
			if w.logger != nil {
				w.logger.Errorf("[ToolCall] PANIC recovered tool=%s: %v", w.name, r)
			}
			result = fmt.Sprintf("Error: tool %s panicked: %v", w.name, r)
			err = nil
		}
	}()
	if !session.IsExecutableToolCallArgs(w.name, argumentsInJSON) {
		if w.logger != nil {
			w.logger.Warnf("[ToolCall] SKIP tool=%s, reason=blocked_invalid_arguments, args=%s", w.name, argumentsInJSON)
		}
		return fmt.Sprintf("Error: blocked_invalid_arguments - invalid or empty arguments for tool %s", w.name), nil
	}

	var toolIndex int
	var stepWarn string
	if stepCounter := getOrCreateStepCounter(ctx); stepCounter != nil {
		step := atomic.AddInt32(stepCounter, 1)
		toolIndex = int(step)
		if w.maxStep > 0 && int(step) >= w.maxStep {
			if w.logger != nil {
				w.logger.Warnf("react_agent: step %d/%d reached max step limit", step, w.maxStep)
			}
		} else if w.maxStep > 0 && int(step) >= w.maxStep-3 {
			if w.logger != nil {
				w.logger.Infof("react_agent: step %d/%d approaching max step limit", step, w.maxStep)
			}
			// L1-1 Soft Reminder: Approaches the step limit (≤3 steps left), this step tool result prefix finishing reminder.
			// At this point, the agent still has writable runs, which is more effective than maxStep hard truncation (when hard truncation, the agent has no next round and no guidance is visible).
			// Address "run written when steps run out"—combined with AGENTS.md "the main producer writes run, first edition," providing double insurance.
			stepWarn = fmt.Sprintf("⚠️步数将尽（%d/%d）：若主体产出已完成，请尽快写 run 记录 + 追加 MEMORY，避免步数耗尽丢失。\n", step, w.maxStep)
		}
	}

	callNum := atomic.AddInt32(&w.callCounter, 1)
	toolCallId := fmt.Sprintf("tool-%s-%d-%d-%s", w.name, time.Now().UnixMilli(), callNum, generateShortID())
	startTime := time.Now()

	if w.logger != nil {
		argsPreview := argumentsInJSON
		if len(argsPreview) > 200 {
			argsPreview = argsPreview[:200] + "..."
		}
		w.logger.Debugf("[ToolCall] START tool=%s, toolType=%s, targetId=%s, args=%s", w.name, w.toolType, w.targetId, argsPreview)
	}

	sendToSSE := GetSSECallback(ctx)
	emitter, _ := aspect.GetEmitter(ctx)

	point := &aspect.AgentPoint{
		AgentId:   w.agentId,
		AgentName: w.agentName,
		AgentType: "react_agent",
		ToolName:  w.name,
		Metadata:  make(map[string]string),
	}

	callInfo := &aspect.ToolCallInfo{
		CallId:    toolCallId,
		Name:      w.name,
		Arguments: argumentsInJSON,
		ToolType:  w.toolType,
		TargetId:  w.targetId,
		StartTime: startTime,
	}

	if w.aspectManager != nil {
		var err error
		callInfo, err = w.aspectManager.ExecuteToolCallBefore(ctx, point, callInfo)
		if err != nil {
			// Facet returns errors and blocks tool calls
			return fmt.Sprintf("Tool call blocked by aspect: %v", err), nil
		}
	}

	// TOOL_CALL_START Already uniformly sent by VizAspect.BeforeToolCall (same ctx emitter), emit is no longer repeated here (fixed double firing: original START/RESULT emitted twice)
	if sendToSSE != nil {
		eventData := map[string]interface{}{
			"toolCallId":   toolCallId,
			"toolCallName": w.name,
			"toolType":     string(w.toolType),
			"targetId":     w.targetId,
			"arguments":    argumentsInJSON,
			"index":        toolIndex,
			"timestamp":    time.Now().UnixMilli(),
		}
		eventJSON, _ := json.Marshal(eventData)
		sendToSSE(toolCallId, w.name, string(SSEEventToolStart), string(eventJSON), toolIndex)
	}

	inputTokens := token.EstimateTokens(argumentsInJSON)

	// doom-loop detection (before execution: duplicate with the same name and same parameter in the sliding window)
	var doomWarn string
	if detector := GetDoomLoopDetector(ctx); detector != nil {
		if warn := detector.BeforeCall(w.name, argumentsInJSON); warn != "" {
			doomWarn = warn
			if w.logger != nil {
				w.logger.Warnf("[DoomLoop] BLOCK tool=%s: %s", w.name, warn)
			}
		}
	} else if w.logger != nil {
		// detector not injected into ctx: doom foolproof completely fails (tool layer cannot prevent repeated calls).
		// Each print call makes it easy to directly confirm from the log whether the doom is active:
		//   Seeing this line flooding = doom invalid (dedup still supports the provider layer); Seeing BLOCK = hit rejection; Neither = normal does not trigger.
		w.logger.Warnf("[DoomLoop] detector NOT in ctx, doom disabled (tool=%s)", w.name)
	}

	// doom hit: refuses execution (does not actually call the tool, avoiding repeated side effects), returns a strong error result, soft prompt for the LLM to change method.
	// Do not return Go error (err=nil), allowing the agent loop to continue rather than interrupt—combined with the MessageRewriter's history fold
	// (dedupRepetitiveToolCalls), which both prompts the LLM and ensures that the history sent to the provider is not contiguous, repeated, or triggered
	// "Repetitive tool calls" 400. maxStep has a safety net and ultimately stopped.
	if doomWarn != "" {
		// Merge stepWarn (step limit reminder, consistent with normal path):d oom rejection if it happens exactly in the last few steps,
		// The agent can still see a prompt to "write run quickly to finish," rather than just seeing Doom reject.
		msg := doomWarn
		if stepWarn != "" {
			msg = stepWarn + msg
		}
		blockedResult := fmt.Sprintf("Error: doom_loop_repeated - 工具 %s 本次调用被拒绝执行。%s", w.name, msg)
		// Still records doom history (allowing subsequent rounds to keep counting)
		if detector := GetDoomLoopDetector(ctx); detector != nil {
			detector.AfterCall(w.name, argumentsInJSON, true)
		}
		duration := time.Since(startTime).Milliseconds()
		if w.metricsCollector != nil {
			w.metricsCollector.Record(w.name, duration, inputTokens, token.EstimateTokens(blockedResult), true)
		}
		callResult := &aspect.ToolCallResult{
			CallId:    toolCallId,
			Name:      w.name,
			Arguments: argumentsInJSON,
			Result:    blockedResult,
			Error:     fmt.Errorf("doom_loop_repeated"),
			Duration:  duration,
			EndTime:   time.Now(),
		}
		if w.aspectManager != nil {
			w.aspectManager.ExecuteToolCallAfter(ctx, point, callInfo, callResult)
		}
		aspect.AddToolCallResultToContext(ctx, callResult)
		if emitter != nil {
			emitter.EmitToolCallEnd(toolCallId)
		}
		if sendToSSE != nil {
			sendToSSE(toolCallId, w.name, string(SSEEventToolError), "doom_loop_repeated", toolIndex)
			sendToSSE(toolCallId, w.name, string(SSEEventToolResult), blockedResult, toolIndex)
		}
		if w.logger != nil {
			w.logger.Warnf("[ToolCall] BLOCKED tool=%s, reason=doom_loop_repeated", w.name)
		}
		return blockedResult, nil
	}

	result, err = w.base.InvokableRun(ctx, argumentsInJSON, opts...)

	// doom-loop check (after execution: record this call + consecutive failures)
	if detector := GetDoomLoopDetector(ctx); detector != nil {
		if warn := detector.AfterCall(w.name, argumentsInJSON, err != nil || isFailureResult(result)); warn != "" {
			// At this point, doomWarn is always empty (doom hits are already early return), so just assign a value directly
			doomWarn = warn
			if w.logger != nil {
				w.logger.Warnf("[DoomLoop] %s", warn)
			}
		}
	}

	// L1-1: Merge the closing reminder close to maxStep into doomWarn, and return the tool result prefix to the agent
	if stepWarn != "" {
		doomWarn = stepWarn + doomWarn
	}

	duration := time.Since(startTime).Milliseconds()
	outputTokens := token.EstimateTokens(result)

	if w.metricsCollector != nil {
		w.metricsCollector.Record(w.name, duration, inputTokens, outputTokens, err != nil)
	}

	callResult := &aspect.ToolCallResult{
		CallId:    toolCallId,
		Name:      w.name,
		Arguments: argumentsInJSON,
		Result:    result,
		Error:     err,
		Duration:  duration,
		EndTime:   time.Now(),
	}

	if w.logger != nil {
		resultPreview := result
		if len(resultPreview) > 300 {
			resultPreview = resultPreview[:300] + "..."
		}
		if err != nil {
			w.logger.Debugf("[ToolCall] ERROR tool=%s, duration=%dms, error=%v, result=%s", w.name, duration, err, resultPreview)
		} else {
			w.logger.Debugf("[ToolCall] END tool=%s, duration=%dms, resultLen=%d, result=%s", w.name, duration, len(result), resultPreview)
		}
	}

	if err != nil {
		callResult.Result = fmt.Sprintf("Tool execution failed: %v", err)

		if w.aspectManager != nil {
			w.aspectManager.ExecuteToolCallAfter(ctx, point, callInfo, callResult)
		}

		if emitter != nil {
			// The RESULT has been uniformly sent by VizAspect.AfterToolCall (same ctx emitter), and only the END is sent here
			emitter.EmitToolCallEnd(toolCallId)
		}

		if sendToSSE != nil {
			sendToSSE(toolCallId, w.name, string(SSEEventToolError), err.Error(), toolIndex)
			sendToSSE(toolCallId, w.name, string(SSEEventToolResult), callResult.Result, toolIndex)
		}
		// Instead of returning error interruption processes, it returns error messages as the result and allows the agent to continue running
		return prefixDoomWarn(doomWarn, callResult.Result), nil
	}

	if w.aspectManager != nil {
		w.aspectManager.ExecuteToolCallAfter(ctx, point, callInfo, callResult)
	}

	aspect.AddToolCallResultToContext(ctx, callResult)

	if emitter != nil {
		// The RESULT has been uniformly sent by VizAspect.AfterToolCall (same ctx emitter), and only the END is sent here
		emitter.EmitToolCallEnd(toolCallId)
	}

	if sendToSSE != nil {
		sendToSSE(toolCallId, w.name, string(SSEEventToolResult), result, toolIndex)
	}

	return prefixDoomWarn(doomWarn, truncateResult(result, w.maxToolOutputLength)), nil
}

// prefixDoomWarn: If there is a doom-loop warning, it will be spelled to the result prefix so the agent can see it.
func prefixDoomWarn(warn, result string) string {
	if warn == "" {
		return result
	}
	return warn + "\n\n" + result
}

// isFailureResult checks whether the tool result indicates failure. Returns (string, nil) when the tool fails—err always nil,
// Store the error in result ("Error: ..." comes from common.ErrXxx; "Tool execution failed" comes from this wrapper),
// Therefore, the continuous failure detection of doom-loop must look at the result content (review C2).
func isFailureResult(result string) bool {
	return strings.HasPrefix(result, "Error:") || strings.HasPrefix(result, "Tool execution failed")
}

// ============================================
// Context auxiliary function
// ============================================

// stepCounterKey is used to store the step counter in context
type stepCounterKey struct{}

// getOrCreateStepCounter Retrieves or creates a step counter from context
func getOrCreateStepCounter(ctx context.Context) *int32 {
	if counter, ok := ctx.Value(stepCounterKey{}).(*int32); ok {
		return counter
	}
	return nil
}

// WithStepCounter stores the step counter in context
func WithStepCounter(ctx context.Context, counter *int32) context.Context {
	return context.WithValue(ctx, stepCounterKey{}, counter)
}

// Ensure VisualToolWrapper implements tool.InvokableTool
var _ tool.InvokableTool = (*VisualToolWrapper)(nil)
