/*
 * Copyright 2024 The RuleGo Authors.
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

// Package processor provides AI-specific endpoint processors for RuleGo.
// These processors handle streaming responses, SSE formatting, and AI protocol conversions.
package processor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/api/types/endpoint"
	"github.com/rulego/rulego/builtin/processor"
)

// OpenAI Streaming Response Constants
// OpenAI 流式响应常量

const (
	// HeaderKeyXStreamEnabled is a custom header key for enabling streaming.
	HeaderKeyXStreamEnabled = "X-Stream-Enabled"

	// KeyStream is a metadata key used for enabling streaming.
	KeyStream = "stream"
	// KeyAgentMode is a metadata key used for setting AI agent mode.
	KeyAgentMode = "agent_mode"
	// KeyContextType is a metadata key used for setting AI context type.
	KeyContextType = "context_type"
	// KeyChunk is a metadata key used for identifying data chunks in a stream.
	KeyChunk = "chunk"
	// KeyToolCall is a metadata key used for identifying tool call data in a stream.
	KeyToolCall = "tool_call"
	// KeyStreamCompleted is a metadata key used for signaling stream completion.
	KeyStreamCompleted = "stream_completed"
	// KeyStreaming is a metadata key used for identifying if streaming is enabled.
	KeyStreaming = "_streaming"
	// KeyFullContent is a metadata key used for identifying full content message (skipped by processor).
	KeyFullContent = "full_content"

	// ValueTrue is the string representation of boolean true.
	ValueTrue = "true"
	// ValueOrchestrator is a constant for orchestrator agent mode.
	ValueOrchestrator = "orchestrator"
	// ValueConversation is a constant for conversation context type.
	ValueConversation = "conversation"
	// ValueStop is a constant for signaling the end of a stream.
	ValueStop = "stop"

	// KeyID is a metadata key for message ID.
	KeyID = "id"
	// KeyModel is a metadata key for model name.
	KeyModel = "model"

	// DefaultModel is the default model name used if not provided.
	DefaultModel = "rulego-model"
	// ChatcmplPrefix is the prefix for chat completion IDs.
	ChatcmplPrefix = "chatcmpl-"

	// OpenAI JSON keys
	KeyObject           = "object"
	KeyCreated          = "created"
	KeyChoices          = "choices"
	KeyIndex            = "index"
	KeyDelta            = "delta"
	KeyFinishReason     = "finish_reason"
	KeyRole             = "role"
	KeyUsage            = "usage"
	KeyPromptTokens     = "prompt_tokens"
	KeyCompletionTokens = "completion_tokens"
	KeyTotalTokens      = "total_tokens"

	// OpenAI JSON values
	ValueChatCompletionChunk = "chat.completion.chunk"
	ValueChatCompletion      = "chat.completion"
	ValueAssistant           = "assistant"

	// SSE constants
	SSEDataPrefix = "data: "
	SSEDone       = "[DONE]"

	// Token usage metadata keys
	KeyPromptTokensMetadata     = "prompt_tokens"
	KeyCompletionTokensMetadata = "completion_tokens"
	KeyTotalTokensMetadata      = "total_tokens"
	KeyCachedTokensMetadata     = "cached_tokens"

	// OpenAI usage sub-keys
	KeyPromptTokensDetails = "prompt_tokens_details"

	// 调用方流式协议标识。客户端需要流式展示工具调用过程（调用、参数、结果）时，
	// 在请求头带 X-Stream-Protocol: agui 走 AG-UI 扩展模式；不带时走标准 OpenAI 模式，
	// 不发 tool_calls、只返回最终文本（适用于严格遵循 OpenAI 规范的客户端）。
	HeaderStreamProtocol = "X-Stream-Protocol"
	ValueAGUI            = "agui"
)

func init() {
	// Register output processor for OpenAI streaming response handling
	// 注册 OpenAI 流式响应处理的输出处理器
	processor.OutBuiltins.Register("openaiStreamingResponse", openaiStreamingResponse)
}

// openaiStreamingResponse handles OpenAI-compatible streaming and non-streaming responses.
// It transforms RuleGo message exchanges into OpenAI API response format.
func openaiStreamingResponse(router endpoint.Router, exchange *endpoint.Exchange) bool {
	exchange.Lock()
	defer exchange.Unlock()

	// Check if headers are already written (by checking Content-Type)
	contentType := exchange.Out.Headers().Get(processor.HeaderKeyContentType)
	isSSE := strings.Contains(contentType, processor.HeaderValueEventStream)

	if err := exchange.Out.GetError(); err != nil {
		handleError(exchange, isSSE, err)
	} else if msg := exchange.Out.GetMsg(); msg != nil {
		handleMessage(exchange, msg, isSSE)
	}
	return true
}

// handleError processes error responses for both SSE and non-SSE modes.
func handleError(exchange *endpoint.Exchange, isSSE bool, err error) {
	if isSSE {
		// If already in SSE mode, send error as an SSE event to avoid breaking the stream
		// and avoid "superfluous response.WriteHeader" error
		errorResp := map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "stream_error",
				"param":   nil,
				"code":    nil,
			},
		}
		errorBytes, _ := json.Marshal(errorResp)
		errorData := fmt.Sprintf("%s%s\n\n", SSEDataPrefix, string(errorBytes))
		exchange.Out.SetBody([]byte(errorData))
		// 确保 Flush - 使用统一的 Flusher 接口
		if flusher, ok := exchange.Out.(endpoint.Flusher); ok {
			flusher.Flush()
		}
	} else {
		// Handle initial error
		exchange.Out.SetStatusCode(http.StatusBadRequest)
		// 使用 HeaderModifier 接口设置 Content-Type（线程安全）
		if t, ok := exchange.Out.(endpoint.HeaderModifier); ok {
			t.SetHeader(processor.HeaderKeyContentType, processor.HeaderValueApplicationJson)
		}
		// Create OpenAI error format
		errorResp := map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "invalid_request_error",
				"param":   nil,
				"code":    nil,
			},
		}
		errorData, _ := json.Marshal(errorResp)
		exchange.Out.SetBody(errorData)
	}
}

// handleMessage processes successful message responses.
func handleMessage(exchange *endpoint.Exchange, msg *types.RuleMsg, isSSE bool) {
	// Check if this is a chunk or complete response
	isChunk := msg.Metadata.GetValue(KeyChunk) == ValueTrue
	isCompleted := msg.Metadata.GetValue(KeyStreamCompleted) == ValueTrue

	// 如果是流式请求的完整内容消息（full_content=true），跳过处理
	// 流式内容已通过 chunk 发送，token 统计已在 handleCompletion 中处理
	if msg.Metadata.GetValue(KeyStream) == ValueTrue && msg.Metadata.GetValue(KeyFullContent) == ValueTrue {
		return
	}

	// Get or generate ID and model
	id := msg.Metadata.GetValue(KeyID)
	if id == "" {
		id = ChatcmplPrefix + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	model := msg.Metadata.GetValue(KeyModel)
	if model == "" {
		model = DefaultModel
	}

	if isChunk {
		handleChunk(exchange, msg, id, model, isSSE)
	} else if isCompleted {
		handleCompletion(exchange, msg, id, model, isSSE)
	} else {
		handleFullResponse(exchange, msg, id, model)
	}

	// 尝试刷新响应流
	// 使用统一的 Flusher 接口，支持 rest 和 fasthttp 等 endpoint
	if flusher, ok := exchange.Out.(endpoint.Flusher); ok {
		flusher.Flush()
	}
}

// isAGUIConsumer 判断请求头是否带 X-Stream-Protocol: agui（即是否走 AG-UI 扩展模式）。
func isAGUIConsumer(exchange *endpoint.Exchange) bool {
	if exchange != nil && exchange.In != nil {
		if h := exchange.In.Headers(); h != nil {
			return h.Get(HeaderStreamProtocol) == ValueAGUI
		}
	}
	return false
}

// handleChunk processes streaming chunk responses.
func handleChunk(exchange *endpoint.Exchange, msg *types.RuleMsg, id, model string, isSSE bool) {
	if !isSSE {
		setSSEHeaders(exchange)
	}

	// Check if this is a tool call chunk
	isToolCall := msg.Metadata.GetValue(KeyToolCall) == ValueTrue

	// 标准模式下跳过工具调用 chunk（只返回最终文本）；AG-UI 扩展模式透传工具调用过程。
	if isToolCall && !isAGUIConsumer(exchange) {
		return
	}

	// Stream chunk data
	// Format: data: {"id":"...","object":"chat.completion.chunk",...}
	delta := map[string]interface{}{}
	if reasoning := msg.Metadata.GetValue(config.KeyReasoningContent); reasoning != "" {
		delta["reasoning_content"] = reasoning
	}
	if isToolCall {
		// AG-UI 扩展模式：透传工具调用事件（type/toolCallName/content）
		var toolCallData map[string]interface{}
		if err := json.Unmarshal([]byte(msg.GetData()), &toolCallData); err == nil {
			delta["tool_calls"] = []interface{}{toolCallData}
		}
	} else if content := msg.GetData(); content != "" {
		delta["content"] = content
	}

	chunkResp := map[string]interface{}{
		KeyID:      id,
		KeyObject:  ValueChatCompletionChunk,
		KeyCreated: time.Now().Unix(),
		KeyModel:   model,
		KeyChoices: []interface{}{
			map[string]interface{}{
				KeyIndex:        0,
				KeyDelta:        delta,
				KeyFinishReason: nil,
			},
		},
	}
	chunkBytes, _ := json.Marshal(chunkResp)
	chunkData := fmt.Sprintf("%s%s\n\n", SSEDataPrefix, string(chunkBytes))
	exchange.Out.SetBody([]byte(chunkData))
}

// handleCompletion processes the final streaming completion response.
func handleCompletion(exchange *endpoint.Exchange, msg *types.RuleMsg, id, model string, isSSE bool) {
	// 确保 SSE headers 已设置（如果之前没有设置）
	if !isSSE {
		setSSEHeaders(exchange)
	}

	// 从 metadata 中获取 token 使用统计
	_, _, totalTokens := getTokenUsage(msg)

	// 先发送最终内容（如果有）
	var finalData string
	if msg.GetData() != "" {
		// 发送最终内容
		contentResp := map[string]interface{}{
			KeyID:      id,
			KeyObject:  ValueChatCompletionChunk,
			KeyCreated: time.Now().Unix(),
			KeyModel:   model,
			KeyChoices: []interface{}{
				map[string]interface{}{
					KeyIndex: 0,
					KeyDelta: map[string]interface{}{
						"content": msg.GetData(),
					},
					KeyFinishReason: nil,
				},
			},
		}
		contentBytes, _ := json.Marshal(contentResp)
		finalData = fmt.Sprintf("%s%s\n\n", SSEDataPrefix, string(contentBytes))
	}

	// 发送完成信号（包含标准 token 使用统计）
	chunkResp := map[string]interface{}{
		KeyID:      id,
		KeyObject:  ValueChatCompletionChunk,
		KeyCreated: time.Now().Unix(),
		KeyModel:   model,
		KeyChoices: []interface{}{
			map[string]interface{}{
				KeyIndex:        0,
				KeyDelta:        map[string]interface{}{},
				KeyFinishReason: ValueStop,
			},
		},
	}
	// 如果有 token 统计，添加标准 usage 结构
	if totalTokens > 0 {
		chunkResp[KeyUsage] = buildUsageObject(msg)
	}
	chunkBytes, _ := json.Marshal(chunkResp)
	// 格式: [finalData]data: {chunk}\n\ndata: [DONE]\n\n
	completeData := finalData + fmt.Sprintf("%s%s\n\n%s%s\n\n", SSEDataPrefix, string(chunkBytes), SSEDataPrefix, SSEDone)
	exchange.Out.SetBody([]byte(completeData))
}

// handleFullResponse processes non-streaming full responses.
func handleFullResponse(exchange *endpoint.Exchange, msg *types.RuleMsg, id, model string) {
	// 使用 HeaderModifier 接口设置 Content-Type（线程安全）
	if t, ok := exchange.Out.(endpoint.HeaderModifier); ok {
		t.SetHeader(processor.HeaderKeyContentType, processor.HeaderValueApplicationJson)
	}

	fullResp := map[string]interface{}{
		KeyID:      id,
		KeyObject:  ValueChatCompletion,
		KeyCreated: time.Now().Unix(),
		KeyModel:   model,
		KeyChoices: []interface{}{
			map[string]interface{}{
				KeyIndex: 0,
				"message": map[string]interface{}{
					KeyRole:   ValueAssistant,
					"content": msg.GetData(),
				},
				KeyFinishReason: ValueStop,
			},
		},
		KeyUsage: buildUsageObject(msg),
	}
	fullData, _ := json.Marshal(fullResp)
	exchange.Out.SetBody(fullData)
}

// setSSEHeaders sets the required headers for Server-Sent Events.
// Uses HeaderModifier interface for thread-safe header operations.
func setSSEHeaders(exchange *endpoint.Exchange) {
	// 始终使用 HeaderModifier 接口，它是线程安全的
	if t, ok := exchange.Out.(endpoint.HeaderModifier); ok {
		t.SetHeader(processor.HeaderKeyContentType, processor.HeaderValueEventStream)
		t.SetHeader(processor.HeaderKeyCacheControl, processor.HeaderValueNoCache+", no-transform")
		t.SetHeader(processor.HeaderKeyConnection, processor.HeaderValueKeepAlive)
		t.SetHeader("X-Accel-Buffering", "no")
	}
	// 如果不支持 HeaderModifier，跳过设置（避免并发写入 map）
}

// getTokenUsage extracts token usage statistics from message metadata.
// Returns prompt_tokens, completion_tokens, total_tokens.
func getTokenUsage(msg *types.RuleMsg) (int, int, int) {
	promptTokens := parseIntFromString(msg.Metadata.GetValue(KeyPromptTokensMetadata))
	completionTokens := parseIntFromString(msg.Metadata.GetValue(KeyCompletionTokensMetadata))
	totalTokens := parseIntFromString(msg.Metadata.GetValue(KeyTotalTokensMetadata))
	return promptTokens, completionTokens, totalTokens
}

// buildUsageObject 构建标准 OpenAI usage 对象。
// 只有 cached_tokens > 0 时才返回 prompt_tokens_details，兼容不支持缓存的接口。
func buildUsageObject(msg *types.RuleMsg) map[string]interface{} {
	promptTokens, completionTokens, totalTokens := getTokenUsage(msg)
	cachedTokens := parseIntFromString(msg.Metadata.GetValue(KeyCachedTokensMetadata))

	usage := map[string]interface{}{
		KeyPromptTokens:     promptTokens,
		KeyCompletionTokens: completionTokens,
		KeyTotalTokens:      totalTokens,
	}

	// 仅在有缓存数据时返回 prompt_tokens_details，兼容不支持缓存的 LLM
	if cachedTokens > 0 {
		usage[KeyPromptTokensDetails] = map[string]interface{}{
			"cached_tokens": cachedTokens,
		}
	}

	return usage
}

// parseIntFromString safely parses an integer from a string, returns 0 on error.
func parseIntFromString(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
