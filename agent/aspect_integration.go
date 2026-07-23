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

// AgentAspectExecutor Agent faceted executor
// Encapsulates execution logic for all aspects.
type AgentAspectExecutor struct {
	manager *aspect.AspectManager
	logger  types.Logger
}

// NewAgentAspectExecutor creates a faceted executor
func NewAgentAspectExecutor(logger types.Logger) *AgentAspectExecutor {
	exec := &AgentAspectExecutor{
		manager: aspect.NewAspectManager(),
		logger:  logger,
	}

	// Copy the cut from the global registry
	for _, a := range aspect.GetGlobalAspects() {
		exec.manager.Register(a.New())
	}

	return exec
}

// Manager returns the Face Manager
func (e *AgentAspectExecutor) Manager() *aspect.AspectManager {
	return e.manager
}

// ExecuteOptions executes options
type ExecuteOptions struct {
	ChainId    string
	AgentName  string
	Msg        types.RuleMsg
	SessionKey string
}

// ExecuteSync performs synchronous execution with aspects.
func (e *AgentAspectExecutor) ExecuteSync(
	ctx context.Context,
	opts ExecuteOptions,
	input *aspect.AgentInput,
	messages []*schema.Message,
	executor func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error),
) (*aspect.AgentOutput, error) {
	point := e.buildPoint(opts)
	startTime := time.Now()

	// Create a tool call collector and inject it into context
	toolCallsCollector := aspect.NewToolCallsCollector()
	ctx = aspect.WithToolCallsCollector(ctx, toolCallsCollector)

	// 1. Start the facet
	input, err := e.manager.ExecuteStart(ctx, point, input)
	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 2. Before cross-section
	input, err = e.manager.ExecuteBefore(ctx, point, input)
	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 3. Merge historical messages
	mergedMessages := e.mergeMessages(input, messages)

	// Print debug logs: system prompts and latest news
	e.logDebugInfo(mergedMessages, input.SystemPrompt)

	// 4. Around the Facet + Actual Execution
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

	// 5. After cross-section
	output, _ = e.manager.ExecuteAfter(ctx, point, output)

	// 6. Completed cross-section
	output.IsSuccess = true
	e.manager.ExecuteCompleted(ctx, point, output)

	return output, nil
}

// ExecuteStream performs streaming execution with aspects.
func (e *AgentAspectExecutor) ExecuteStream(
	ctx context.Context,
	opts ExecuteOptions,
	input *aspect.AgentInput,
	messages []*schema.Message,
	streamExecutor func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error),
	onChunk func(content, reasoning string, isFirst bool),
) (*aspect.AgentOutput, error) {
	point := e.buildPoint(opts)
	startTime := time.Now()

	// Create a tool call collector and inject it into context
	toolCallsCollector := aspect.NewToolCallsCollector()
	ctx = aspect.WithToolCallsCollector(ctx, toolCallsCollector)

	// 1. Start the facet
	input, err := e.manager.ExecuteStart(ctx, point, input)
	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 2. Before cross-section
	input, err = e.manager.ExecuteBefore(ctx, point, input)
	if err != nil {
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 3. Merge historical messages
	mergedMessages := e.mergeMessages(input, messages)

	// Print debug logs: system prompts and latest news
	e.logDebugInfo(mergedMessages, input.SystemPrompt)

	// 4. Around Section + Execute stream call
	output, err := e.manager.ExecuteAround(ctx, point, input, func(ctx context.Context, in *aspect.AgentInput) (*aspect.AgentOutput, error) {
		streamReader, err := streamExecutor(ctx, mergedMessages)
		if err != nil {
			return nil, err
		}
		defer streamReader.Close()

		var fullContent strings.Builder
		var lastChunk *schema.Message
		var streamErr error // Midstream error (non-EOF) no longer silently swallows
		chunkCount := 0

		// onChunk is handled by the upper layer (react_agent) for entering StreamTellQueue, which is not blocking; here it can be called synchronously.
		for {
			chunk, err := streamReader.Recv()
			if err != nil {
				if err != io.EOF {
					if e.logger != nil {
						e.logger.Warnf("[ExecuteStream] Stream ended with error: %v, total chunks: %d, content length: %d", err, chunkCount, fullContent.Len())
					}
					streamErr = err
				}
				break
			}
			lastChunk = chunk

			if chunk.Content != "" || chunk.ReasoningContent != "" {
				chunkCount++
				if chunk.Content != "" {
					fullContent.WriteString(chunk.Content)
				}

				streamChunk := &aspect.StreamChunk{
					Content:   chunk.Content,
					IsFinal:   false,
					Timestamp: time.Now(),
				}
				e.manager.ExecuteStreamChunk(ctx, point, streamChunk)

				if onChunk != nil {
					onChunk(chunk.Content, chunk.ReasoningContent, chunkCount == 1)
				}
			}

			if chunkCount > config.MaxStreamChunks {
				if e.logger != nil {
					e.logger.Warnf("[ExecuteStream] MaxStreamChunks (%d) exceeded, stopping stream", config.MaxStreamChunks)
				}
				break
			}
		}

		// 6. Build output. If an error occurs mid-stream, it carries the error into the output.Error and return error, allowing the upper layer to perceive "truncated" rather than silently succeeding.
		output := e.buildStreamOutput(ctx, fullContent.String(), lastChunk, input, startTime)
		if streamErr != nil {
			output.Error = streamErr
		}
		return output, streamErr
	})

	if err != nil {
		// Truncation/Error: The output may contain some content (sent to the frontend via chunk), and add error to distinguish between "truncation" and "success" at the upper level.
		if output != nil {
			output.Error = err
			e.manager.ExecuteCompleted(ctx, point, output)
			return output, err
		}
		e.manager.ExecuteCompleted(ctx, point, &aspect.AgentOutput{Error: err, IsSuccess: false})
		return nil, err
	}

	// 7. After cross-section
	output, _ = e.manager.ExecuteAfter(ctx, point, output)

	// 8. Completed cross-section
	output.IsSuccess = true
	e.manager.ExecuteCompleted(ctx, point, output)

	return output, nil
}

// buildPoint Constructs the facet call point
func (e *AgentAspectExecutor) buildPoint(opts ExecuteOptions) *aspect.AgentPoint {
	point := &aspect.AgentPoint{
		AgentId:   opts.ChainId,
		AgentName: opts.AgentName,
		AgentType: "agent",
		ThreadId:  opts.ChainId,
		UserId:    opts.Msg.Metadata.GetValue("userId"),
		Metadata:  make(map[string]string),
	}

	// Copy message metadata
	for k, v := range opts.Msg.Metadata.Values() {
		point.Metadata[k] = v
	}

	return point
}

// mergeMessages to merge messages and handle the changes made in the face
func (e *AgentAspectExecutor) mergeMessages(input *aspect.AgentInput, currentMessages []*schema.Message) []*schema.Message {
	var mergedMsgs []*schema.Message

	// 1. Handling system messages (priority: SystemPrompt modified in the faceted > Messages added in the facet > original system message)
	if input.SystemPrompt != "" {
		// The section modifies SystemPrompt to use it
		mergedMsgs = append(mergedMsgs, &schema.Message{
			Role:    schema.System,
			Content: input.SystemPrompt,
		})
	} else if len(input.Messages) > 0 {
		// The facet may have added system messages to messages
		for _, m := range input.Messages {
			if m.Role == schema.System {
				mergedMsgs = append(mergedMsgs, m)
			}
		}
	} else {
		// Use system messages from the original message
		for _, m := range currentMessages {
			if m.Role == schema.System {
				mergedMsgs = append(mergedMsgs, m)
			}
		}
	}

	// 2. Add historical messages
	if len(input.HistoryMessages) > 0 {
		mergedMsgs = append(mergedMsgs, input.HistoryMessages...)
	}

	// 3. Add the current message (non-system message)
	for _, m := range currentMessages {
		if m.Role != schema.System {
			mergedMsgs = append(mergedMsgs, m)
		}
	}

	// If there is no message, return to the original message
	if len(mergedMsgs) == 0 {
		return currentMessages
	}

	return mergedMsgs
}

// buildOutput: Build synchronous output
func (e *AgentAspectExecutor) buildOutput(ctx context.Context, msg *schema.Message, input *aspect.AgentInput, startTime time.Time) *aspect.AgentOutput {
	output := &aspect.AgentOutput{
		Content:          msg.Content,
		Messages:         []*schema.Message{msg},
		OriginalMessages: input.OriginalMessages,
		Duration:         time.Since(startTime).Milliseconds(),
		Metadata:         make(map[string]any),
		SessionKey:       input.SessionKey,
	}

	// Token extraction usage statistics
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		output.TokenUsage = aspect.TokenUsage{
			PromptTokens:     msg.ResponseMeta.Usage.PromptTokens,
			CompletionTokens: msg.ResponseMeta.Usage.CompletionTokens,
			TotalTokens:      msg.ResponseMeta.Usage.TotalTokens,
			CachedTokens:     msg.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
		}
	}

	// Read the result of the tool call from the context
	output.ToolCalls = aspect.GetToolCallResultsFromContext(ctx)

	return output
}

// buildStreamOutput
func (e *AgentAspectExecutor) buildStreamOutput(ctx context.Context, fullContent string, lastChunk *schema.Message, input *aspect.AgentInput, startTime time.Time) *aspect.AgentOutput {
	output := &aspect.AgentOutput{
		Content:          fullContent,
		OriginalMessages: input.OriginalMessages,
		Duration:         time.Since(startTime).Milliseconds(),
		Metadata:         make(map[string]any),
		SessionKey:       input.SessionKey,
	}

	// Token extraction usage statistics (obtained from the last chunk)
	if lastChunk != nil && lastChunk.ResponseMeta != nil && lastChunk.ResponseMeta.Usage != nil {
		output.TokenUsage = aspect.TokenUsage{
			PromptTokens:     lastChunk.ResponseMeta.Usage.PromptTokens,
			CompletionTokens: lastChunk.ResponseMeta.Usage.CompletionTokens,
			TotalTokens:      lastChunk.ResponseMeta.Usage.TotalTokens,
			CachedTokens:     lastChunk.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
		}
	}

	// Read the result of the tool call from the context
	output.ToolCalls = aspect.GetToolCallResultsFromContext(ctx)

	return output
}

// InjectEmitter injects an emitter into the context
func InjectEmitter(ctx context.Context, chainId string) context.Context {
	if emitter, ok := aspect.GetEmitterWithFallback(ctx, chainId); ok {
		return aspect.WithEmitter(ctx, emitter)
	}
	return ctx
}

// InjectAspectManager injects the Aspect Manager into context
func InjectAspectManager(ctx context.Context, manager *aspect.AspectManager) context.Context {
	return aspect.WithAspectManager(ctx, manager)
}

// BuildTokenMetadata Constructs token statistics and metadata
func BuildTokenMetadata(msg types.RuleMsg, tokenUsage aspect.TokenUsage, modelName string) {
	msg.Metadata.PutValue(config.KeyModel, modelName)
	if tokenUsage.TotalTokens > 0 {
		msg.Metadata.PutValue(config.KeyPromptTokens, formatInt(tokenUsage.PromptTokens))
		msg.Metadata.PutValue(config.KeyCompletionTokens, formatInt(tokenUsage.CompletionTokens))
		msg.Metadata.PutValue(config.KeyTotalTokens, formatInt(tokenUsage.TotalTokens))
		msg.Metadata.PutValue(config.KeyCachedTokens, formatInt(tokenUsage.CachedTokens))
	}
}

// BuildStreamEndMetadata to construct the stream end metadata
func BuildStreamEndMetadata(msg types.RuleMsg) {
	msg.Metadata.PutValue(config.KeyStreamCompleted, config.ValueTrue)
	msg.Metadata.PutValue(config.KeyChunk, "")
}

// BuildStreamChunkMetadata to construct streaming block metadata
func BuildStreamChunkMetadata(msg types.RuleMsg, isFirst bool) {
	msg.Metadata.PutValue(config.KeyChunk, config.ValueTrue)
	// Fixed stream drop BUG: Do not set KeyStreamStart
	// When the RuleGo engine encounters KeyStreamStart, it calls childDone() in advance to end the HTTP wait
	// This causes the time-consuming tool call to be discarded due to the context being canceled
	// We rely on the last types.Success message to trigger the childDone() termination request
	msg.Metadata.Delete(config.KeyStreamStart)
}

// BuildStreamChunkMetadataWithModel to construct streaming block metadata (with model name)
func BuildStreamChunkMetadataWithModel(msg types.RuleMsg, isFirst bool, modelName string) {
	BuildStreamChunkMetadata(msg, isFirst)
	if modelName != "" {
		msg.Metadata.PutValue(config.KeyModel, modelName)
	}
}

func formatInt(n int) string {
	return strconv.Itoa(n)
}

// logDebugInfo prints debug information: system prompt summaries and latest news
func (e *AgentAspectExecutor) logDebugInfo(messages []*schema.Message, systemPrompt string) {
	if e.logger == nil {
		return
	}

	// Print the latest user message
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == schema.User {
			content := msg.Content
			// For multimodal messages, extract text and image information
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

// truncateString Truncates the string
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
