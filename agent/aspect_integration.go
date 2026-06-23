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
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/api/types"
)

// AgentAspectExecutor Agent 切面执行器
// 封装所有切面相关的执行逻辑
type AgentAspectExecutor struct {
	manager *aspect.AspectManager
	logger  types.Logger
}

// NewAgentAspectExecutor 创建切面执行器
func NewAgentAspectExecutor(logger types.Logger) *AgentAspectExecutor {
	exec := &AgentAspectExecutor{
		manager: aspect.NewAspectManager(),
		logger:  logger,
	}

	// 从全局注册表复制切面
	for _, a := range aspect.GetGlobalAspects() {
		exec.manager.Register(a.New())
	}

	return exec
}

// Manager 返回切面管理器
func (e *AgentAspectExecutor) Manager() *aspect.AspectManager {
	return e.manager
}

// ExecuteOptions 执行选项
type ExecuteOptions struct {
	ChainId    string
	AgentName  string
	Msg        types.RuleMsg
	SessionKey string
}

// ExecuteSync 同步执行（带切面）
func (e *AgentAspectExecutor) ExecuteSync(
	ctx context.Context,
	opts ExecuteOptions,
	input *aspect.AgentInput,
	messages []*schema.Message,
	executor func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error),
) (*aspect.AgentOutput, error) {
	point := e.buildPoint(opts)
	startTime := time.Now()

	// 创建工具调用收集器并注入到 context
	toolCallsCollector := aspect.NewToolCallsCollector()
	ctx = aspect.WithToolCallsCollector(ctx, toolCallsCollector)

	// 1. Start 切面
	input, err := e.manager.ExecuteStart(ctx, point, input)
	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 2. Before 切面
	input, err = e.manager.ExecuteBefore(ctx, point, input)
	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 3. 合并历史消息
	mergedMessages := e.mergeMessages(input, messages)

	// 打印调试日志：系统提示词和最新消息
	e.logDebugInfo(mergedMessages, input.SystemPrompt)

	// 4. Around 切面 + 实际执行
	output, err := e.manager.ExecuteAround(ctx, point, input, func(ctx context.Context, in *aspect.AgentInput) (*aspect.AgentOutput, error) {
		msg, err := executor(ctx, mergedMessages)
		if err != nil {
			return nil, err
		}
		if msg == nil {
			return nil, fmt.Errorf("model returned nil message")
		}
		return e.buildOutput(ctx, msg, in, startTime), nil
	})

	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 5. After 切面
	output, _ = e.manager.ExecuteAfter(ctx, point, output)

	// 6. Completed 切面
	output.IsSuccess = true
	e.manager.ExecuteCompleted(ctx, point, output)

	return output, nil
}

// ExecuteStream 流式执行（带切面）
func (e *AgentAspectExecutor) ExecuteStream(
	ctx context.Context,
	opts ExecuteOptions,
	input *aspect.AgentInput,
	messages []*schema.Message,
	streamExecutor func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error),
	onChunk func(chunk string, isFirst bool),
) (*aspect.AgentOutput, error) {
	point := e.buildPoint(opts)
	startTime := time.Now()

	// 创建工具调用收集器并注入到 context
	toolCallsCollector := aspect.NewToolCallsCollector()
	ctx = aspect.WithToolCallsCollector(ctx, toolCallsCollector)

	// 1. Start 切面
	input, err := e.manager.ExecuteStart(ctx, point, input)
	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 2. Before 切面
	input, err = e.manager.ExecuteBefore(ctx, point, input)
	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 3. 合并历史消息
	mergedMessages := e.mergeMessages(input, messages)

	// 打印调试日志：系统提示词和最新消息
	e.logDebugInfo(mergedMessages, input.SystemPrompt)

	// 4. Around 切面 + 执行流式调用
	output, err := e.manager.ExecuteAround(ctx, point, input, func(ctx context.Context, in *aspect.AgentInput) (*aspect.AgentOutput, error) {
		streamReader, err := streamExecutor(ctx, mergedMessages)
		if err != nil {
			return nil, err
		}
		defer streamReader.Close()

		var fullContent strings.Builder
		var lastChunk *schema.Message
		chunkCount := 0

		for {
			chunk, err := streamReader.Recv()
			if err != nil {
				if err != io.EOF {
					if e.logger != nil {
						e.logger.Warnf("[ExecuteStream] Stream ended with error: %v, total chunks: %d, content length: %d", err, chunkCount, fullContent.Len())
					}
				}
				break
			}
			lastChunk = chunk
			chunkCount++

			if chunk.Content != "" {
				fullContent.WriteString(chunk.Content)

				streamChunk := &aspect.StreamChunk{
					Content:   chunk.Content,
					IsFinal:   false,
					Timestamp: time.Now(),
				}
				e.manager.ExecuteStreamChunk(ctx, point, streamChunk)

				if onChunk != nil {
					onChunk(chunk.Content, chunkCount == 1)
				}
			}

			if chunkCount > config.MaxStreamChunks {
				if e.logger != nil {
					e.logger.Warnf("[ExecuteStream] MaxStreamChunks (%d) exceeded, stopping stream", config.MaxStreamChunks)
				}
				break
			}
		}

		// 6. 构建输出
		return e.buildStreamOutput(ctx, fullContent.String(), lastChunk, input, startTime), nil
	})

	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 7. After 切面
	output, _ = e.manager.ExecuteAfter(ctx, point, output)

	// 8. Completed 切面
	output.IsSuccess = true
	e.manager.ExecuteCompleted(ctx, point, output)

	return output, nil
}

// buildPoint 构建切面调用点
func (e *AgentAspectExecutor) buildPoint(opts ExecuteOptions) *aspect.AgentPoint {
	point := &aspect.AgentPoint{
		AgentId:   opts.ChainId,
		AgentName: opts.AgentName,
		AgentType: "agent",
		ThreadId:  opts.ChainId,
		UserId:    opts.Msg.Metadata.GetValue("userId"),
		Metadata:  make(map[string]string),
	}

	// 复制消息元数据
	for k, v := range opts.Msg.Metadata.Values() {
		point.Metadata[k] = v
	}

	return point
}

// mergeMessages 合并消息，处理切面修改的内容
func (e *AgentAspectExecutor) mergeMessages(input *aspect.AgentInput, currentMessages []*schema.Message) []*schema.Message {
	var mergedMsgs []*schema.Message

	// 1. 处理系统消息（优先级：切面修改的 SystemPrompt > 切面添加的 Messages > 原始系统消息）
	if input.SystemPrompt != "" {
		// 切面修改了 SystemPrompt，使用它
		mergedMsgs = append(mergedMsgs, &schema.Message{
			Role:    schema.System,
			Content: input.SystemPrompt,
		})
	} else if len(input.Messages) > 0 {
		// 切面可能添加了系统消息到 Messages
		for _, m := range input.Messages {
			if m.Role == schema.System {
				mergedMsgs = append(mergedMsgs, m)
			}
		}
	} else {
		// 使用原始消息中的系统消息
		for _, m := range currentMessages {
			if m.Role == schema.System {
				mergedMsgs = append(mergedMsgs, m)
			}
		}
	}

	// 2. 添加历史消息
	if len(input.HistoryMessages) > 0 {
		mergedMsgs = append(mergedMsgs, input.HistoryMessages...)
	}

	// 3. 添加当前消息（非系统消息）
	for _, m := range currentMessages {
		if m.Role != schema.System {
			mergedMsgs = append(mergedMsgs, m)
		}
	}

	// 如果没有任何消息，返回原始消息
	if len(mergedMsgs) == 0 {
		return currentMessages
	}

	return mergedMsgs
}

// buildOutput 构建同步输出
func (e *AgentAspectExecutor) buildOutput(ctx context.Context, msg *schema.Message, input *aspect.AgentInput, startTime time.Time) *aspect.AgentOutput {
	output := &aspect.AgentOutput{
		Content:          msg.Content,
		Messages:         []*schema.Message{msg},
		OriginalMessages: input.OriginalMessages,
		Duration:         time.Since(startTime).Milliseconds(),
		Metadata:         make(map[string]any),
		SessionKey:       input.SessionKey,
	}

	// 提取 token 使用统计
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		output.TokenUsage = aspect.TokenUsage{
			PromptTokens:     msg.ResponseMeta.Usage.PromptTokens,
			CompletionTokens: msg.ResponseMeta.Usage.CompletionTokens,
			TotalTokens:      msg.ResponseMeta.Usage.TotalTokens,
			CachedTokens:     msg.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
		}
	}

	// 从 context 读取工具调用结果
	output.ToolCalls = aspect.GetToolCallResultsFromContext(ctx)

	return output
}

// buildStreamOutput 构建流式输出
func (e *AgentAspectExecutor) buildStreamOutput(ctx context.Context, fullContent string, lastChunk *schema.Message, input *aspect.AgentInput, startTime time.Time) *aspect.AgentOutput {
	output := &aspect.AgentOutput{
		Content:          fullContent,
		OriginalMessages: input.OriginalMessages,
		Duration:         time.Since(startTime).Milliseconds(),
		Metadata:         make(map[string]any),
		SessionKey:       input.SessionKey,
	}

	// 提取 token 使用统计（从最后一个 chunk 获取）
	if lastChunk != nil && lastChunk.ResponseMeta != nil && lastChunk.ResponseMeta.Usage != nil {
		output.TokenUsage = aspect.TokenUsage{
			PromptTokens:     lastChunk.ResponseMeta.Usage.PromptTokens,
			CompletionTokens: lastChunk.ResponseMeta.Usage.CompletionTokens,
			TotalTokens:      lastChunk.ResponseMeta.Usage.TotalTokens,
			CachedTokens:     lastChunk.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
		}
	}

	// 从 context 读取工具调用结果
	output.ToolCalls = aspect.GetToolCallResultsFromContext(ctx)

	return output
}

// InjectEmitter 注入 Emitter 到 context
func InjectEmitter(ctx context.Context, chainId string) context.Context {
	if emitter, ok := aspect.GetEmitterWithFallback(ctx, chainId); ok {
		return aspect.WithEmitter(ctx, emitter)
	}
	return ctx
}

// InjectAspectManager 注入切面管理器到 context
func InjectAspectManager(ctx context.Context, manager *aspect.AspectManager) context.Context {
	return aspect.WithAspectManager(ctx, manager)
}

// BuildTokenMetadata 构建 token 统计元数据
func BuildTokenMetadata(msg types.RuleMsg, tokenUsage aspect.TokenUsage, modelName string) {
	msg.Metadata.PutValue(config.KeyModel, modelName)
	if tokenUsage.TotalTokens > 0 {
		msg.Metadata.PutValue(config.KeyPromptTokens, formatInt(tokenUsage.PromptTokens))
		msg.Metadata.PutValue(config.KeyCompletionTokens, formatInt(tokenUsage.CompletionTokens))
		msg.Metadata.PutValue(config.KeyTotalTokens, formatInt(tokenUsage.TotalTokens))
		msg.Metadata.PutValue(config.KeyCachedTokens, formatInt(tokenUsage.CachedTokens))
	}
}

// BuildStreamEndMetadata 构建流式结束元数据
func BuildStreamEndMetadata(msg types.RuleMsg) {
	msg.Metadata.PutValue(config.KeyStreamCompleted, config.ValueTrue)
	msg.Metadata.PutValue(config.KeyChunk, "")
}

// BuildStreamChunkMetadata 构建流式块元数据
func BuildStreamChunkMetadata(msg types.RuleMsg, isFirst bool) {
	msg.Metadata.PutValue(config.KeyChunk, config.ValueTrue)
	// 修复断流 BUG：不要设置 KeyStreamStart
	// RuleGo 引擎在遇到 KeyStreamStart 时会提前调用 childDone() 结束 HTTP 等待
	// 导致长耗时的工具调用后，后续流式 chunk 会因为 Context 被 Cancel 而被丢弃
	// 我们依赖最后的 types.Success 消息来触发 childDone() 结束请求
	msg.Metadata.Delete(config.KeyStreamStart)
}

// BuildStreamChunkMetadataWithModel 构建流式块元数据（带模型名称）
func BuildStreamChunkMetadataWithModel(msg types.RuleMsg, isFirst bool, modelName string) {
	BuildStreamChunkMetadata(msg, isFirst)
	if modelName != "" {
		msg.Metadata.PutValue(config.KeyModel, modelName)
	}
}

func formatInt(n int) string {
	return strconv.Itoa(n)
}

// logDebugInfo 打印调试信息：系统提示词摘要和最新消息
func (e *AgentAspectExecutor) logDebugInfo(messages []*schema.Message, systemPrompt string) {
	if e.logger == nil {
		return
	}

	// 打印最新一条用户消息
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == schema.User {
			content := msg.Content
			// 如果是多模态消息，提取文本和图片信息
			if len(msg.UserInputMultiContent) > 0 {
				var textParts []string
				imageCount := 0
				for _, part := range msg.UserInputMultiContent {
					if part.Type == schema.ChatMessagePartTypeText {
						textParts = append(textParts, part.Text)
					} else if part.Type == schema.ChatMessagePartTypeImageURL {
						imageCount++
					}
				}
				if len(textParts) > 0 {
					content = strings.Join(textParts, " ")
				}
				if imageCount > 0 {
					e.logger.Debugf("[Agent] Latest user message (multimodal): textLen=%d, images=%d, text=%s", len(content), imageCount, truncateString(content, 200))
				} else {
					e.logger.Debugf("[Agent] Latest user message: %s", truncateString(content, 200))
				}
			} else {
				e.logger.Debugf("[Agent] Latest user message: %s", truncateString(content, 200))
			}
			break
		}
	}
}

// truncateString 截断字符串
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
