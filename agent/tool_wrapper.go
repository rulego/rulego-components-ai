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
// SSE 事件处理
// ============================================

// SSEEventType SSE 事件类型
type SSEEventType string

const (
	// SSEEventToolStart 工具调用开始
	SSEEventToolStart SSEEventType = "tool_start"
	// SSEEventToolResult 工具调用结果
	SSEEventToolResult SSEEventType = "tool_result"
	// SSEEventToolError 工具调用错误
	SSEEventToolError SSEEventType = "tool_error"
)

// SSECallback SSE 回调函数类型
type SSECallback func(toolCallId, toolName, eventType, data string, index int)

// sseCallbackKey 用于在 context 中存储 SSE 回调
type sseCallbackKey struct{}

// GetSSECallback 从 context 获取 SSE 回调
func GetSSECallback(ctx context.Context) SSECallback {
	if cb, ok := ctx.Value(sseCallbackKey{}).(SSECallback); ok {
		return cb
	}
	return nil
}

// WithSSECallback 将 SSE 回调存入 context
func WithSSECallback(ctx context.Context, cb SSECallback) context.Context {
	return context.WithValue(ctx, sseCallbackKey{}, cb)
}

// SSEHandler SSE 事件处理器
type SSEHandler struct {
	ctx     types.RuleContext
	msg     types.RuleMsg
	enabled bool
	mu      sync.Mutex
	queue   *StreamTellQueue // 非空时工具事件入队，与 chunk 统一保序
}

// NewSSEHandler 创建 SSE 处理器
func NewSSEHandler(ctx types.RuleContext, msg types.RuleMsg) *SSEHandler {
	return &SSEHandler{
		ctx:     ctx,
		msg:     msg,
		enabled: msg.Metadata.GetValue(config.KeyStream) == config.ValueTrue,
	}
}

// IsEnabled 返回是否启用 SSE
func (h *SSEHandler) IsEnabled() bool {
	return h.enabled
}

// UseQueue 设置流式 TellNext 队列：设置后工具事件改为入队，与 chunk 统一保序。
// 必须在 Callback 注入（工具执行）之前调用；由 executeStream 初始化阶段保证 happens-before，queue 字段无需加锁。
func (h *SSEHandler) UseQueue(q *StreamTellQueue) {
	h.queue = q
}

// Callback 返回 SSE 回调函数（用于注入到 context）
func (h *SSEHandler) Callback() SSECallback {
	if !h.enabled {
		return nil
	}
	return h.sendEvent
}

// sendEvent 发送 SSE 事件
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

// buildEventData 构建 SSE 事件数据
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
		// 解析并合并额外数据
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
// 可视化工具包装器
// ============================================

// VisualToolWrapper 可视化工具包装器
// 负责发送 AG-UI 可视化事件和 SSE 流事件
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

// NewVisualToolWrapper 创建可视化工具包装器
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

// Info 返回工具信息
func (w *VisualToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.base.Info(ctx)
}

// InvokableRun 执行工具并发送 AG-UI 可视化事件和 SSE 流事件
func (w *VisualToolWrapper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (result string, err error) {
	// 工具执行 panic 不杀整个 server：捕获后作为 error result 返回给 agent（agent 可见错误，决定重试/换法）。
	// 治 agent 并行工具偶发的 concurrent map 等致命错误导致进程崩溃（server 反复 exit 2 根因之一）。
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
			// L1-1 软提醒：接近步数上限（还剩 ≤3 步），本步工具 result 前缀收尾提醒。
			// agent 此时仍有轮次可写 run，比 maxStep 硬截断更有效（硬截断时 agent 已无下一轮看不到引导）。
			// 治"步数耗尽没写 run"——配合 AGENTS.md「产出主体即写 run 首版」双保险。
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
			// 切面返回错误，阻止工具调用
			return fmt.Sprintf("Tool call blocked by aspect: %v", err), nil
		}
	}

	// TOOL_CALL_START 已由 VizAspect.BeforeToolCall 统一发（同一 ctx emitter），此处不再重复 emit（修双发：原 START/RESULT 被 emit 两次）
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

	// doom-loop 检测（执行前：滑动窗口内同名同参重复）
	var doomWarn string
	if detector := GetDoomLoopDetector(ctx); detector != nil {
		if warn := detector.BeforeCall(w.name, argumentsInJSON); warn != "" {
			doomWarn = warn
			if w.logger != nil {
				w.logger.Warnf("[DoomLoop] BLOCK tool=%s: %s", w.name, warn)
			}
		}
	} else if w.logger != nil {
		// detector 未注入到 ctx：doom 防呆完全失效（工具层拦不住重复调用）。
		// 每次调用打印，便于从日志直接确认 doom 是否生效：
		//   看到此行刷屏 = doom 失效（dedup 仍兜底 provider 层）；看到 BLOCK = 命中拒绝；两者都无 = 正常未触发。
		w.logger.Warnf("[DoomLoop] detector NOT in ctx, doom disabled (tool=%s)", w.name)
	}

	// doom 命中：拒绝执行（不真正调用工具，避免重复副作用），返回强错误 result 软提示 LLM 换方法。
	// 不返回 Go error（err=nil），让 agent 循环继续而非中断——配合 MessageRewriter 的历史折叠
	// (dedupRepetitiveToolCalls)，既提示 LLM、又保证发往 provider 的历史不连续重复、不触发
	// "Repetitive tool calls" 400。maxStep 兜底最终停止。
	if doomWarn != "" {
		// 合并 stepWarn（步数将尽提醒，与正常路径一致）：doom 拒绝若恰好发生在最后几步，
		// agent 仍能看到「尽快写 run 收尾」的提醒，而非只看到 doom 拒绝。
		msg := doomWarn
		if stepWarn != "" {
			msg = stepWarn + msg
		}
		blockedResult := fmt.Sprintf("Error: doom_loop_repeated - 工具 %s 本次调用被拒绝执行。%s", w.name, msg)
		// 仍记录到 doom history（让后续轮次持续计数）
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

	// doom-loop 检测（执行后：记录本次调用 + 连续失败）
	if detector := GetDoomLoopDetector(ctx); detector != nil {
		if warn := detector.AfterCall(w.name, argumentsInJSON, err != nil || isFailureResult(result)); warn != "" {
			// 走到这里 doomWarn 必为空（doom 命中已在上方 early return），直接赋值即可
			doomWarn = warn
			if w.logger != nil {
				w.logger.Warnf("[DoomLoop] %s", warn)
			}
		}
	}

	// L1-1：把接近 maxStep 的收尾提醒合并进 doomWarn，随工具 result 前缀返回给 agent
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
			// RESULT 已由 VizAspect.AfterToolCall 统一发（同一 ctx emitter），此处只发 END
			emitter.EmitToolCallEnd(toolCallId)
		}

		if sendToSSE != nil {
			sendToSSE(toolCallId, w.name, string(SSEEventToolError), err.Error(), toolIndex)
			sendToSSE(toolCallId, w.name, string(SSEEventToolResult), callResult.Result, toolIndex)
		}
		// 不返回错误中断流程，而是将错误信息作为结果返回，让 agent 继续运行
		return prefixDoomWarn(doomWarn, callResult.Result), nil
	}

	if w.aspectManager != nil {
		w.aspectManager.ExecuteToolCallAfter(ctx, point, callInfo, callResult)
	}

	aspect.AddToolCallResultToContext(ctx, callResult)

	if emitter != nil {
		// RESULT 已由 VizAspect.AfterToolCall 统一发（同一 ctx emitter），此处只发 END
		emitter.EmitToolCallEnd(toolCallId)
	}

	if sendToSSE != nil {
		sendToSSE(toolCallId, w.name, string(SSEEventToolResult), result, toolIndex)
	}

	return prefixDoomWarn(doomWarn, truncateResult(result, w.maxToolOutputLength)), nil
}

// prefixDoomWarn 若有 doom-loop 警告则拼到结果前缀，让 agent 看到。
func prefixDoomWarn(warn, result string) string {
	if warn == "" {
		return result
	}
	return warn + "\n\n" + result
}

// isFailureResult 判断工具结果是否表示失败。工具失败时返回 (string, nil)——err 永远 nil，
// 错误塞进 result（"Error: ..." 来自 common.ErrXxx，"Tool execution failed" 来自本包装器），
// 故 doom-loop 的连续失败检测必须看 result 内容（审查 C2）。
func isFailureResult(result string) bool {
	return strings.HasPrefix(result, "Error:") || strings.HasPrefix(result, "Tool execution failed")
}

// ============================================
// Context 辅助函数
// ============================================

// stepCounterKey 用于在 context 中存储步数计数器
type stepCounterKey struct{}

// getOrCreateStepCounter 从 context 获取或创建步数计数器
func getOrCreateStepCounter(ctx context.Context) *int32 {
	if counter, ok := ctx.Value(stepCounterKey{}).(*int32); ok {
		return counter
	}
	return nil
}

// WithStepCounter 将步数计数器存入 context
func WithStepCounter(ctx context.Context, counter *int32) context.Context {
	return context.WithValue(ctx, stepCounterKey{}, counter)
}

// Ensure VisualToolWrapper implements tool.InvokableTool
var _ tool.InvokableTool = (*VisualToolWrapper)(nil)
