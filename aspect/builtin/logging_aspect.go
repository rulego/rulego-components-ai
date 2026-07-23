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

	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego/api/types"
)

// LoggingAspect log-side section
// Record key events during the agent's execution process
type LoggingAspect struct {
	order  int
	logger types.Logger
}

// NewLoggingAspect creates a log aspect
func NewLoggingAspect(logger types.Logger) *LoggingAspect {
	return &LoggingAspect{
		order:  200,
		logger: logger,
	}
}

// Order returns the execution order
func (a *LoggingAspect) Order() int {
	return a.order
}

// New: Create a new instance of the face
func (a *LoggingAspect) New() aspect.Aspect {
	return &LoggingAspect{
		order:  a.order,
		logger: a.logger,
	}
}

// PointCut always applies this facet
func (a *LoggingAspect) PointCut(ctx context.Context, point *aspect.AgentPoint) bool {
	return a.logger != nil
}

// OnStart recording begins
func (a *LoggingAspect) OnStart(ctx context.Context, point *aspect.AgentPoint, input *aspect.AgentInput) (*aspect.AgentInput, error) {
	a.log("[Aspect:OnStart] Agent=%s Type=%s ThreadId=%s UserId=%s",
		point.AgentName, point.AgentType, point.ThreadId, point.UserId)

	// Record the number of messages entered
	msgCount := len(input.Messages)
	if len(input.HistoryMessages) > 0 {
		a.log("[Aspect:OnStart] Messages=%d (History=%d, Current=%d)",
			msgCount+len(input.HistoryMessages), len(input.HistoryMessages), msgCount)
	} else {
		a.log("[Aspect:OnStart] Messages=%d", msgCount)
	}

	return input, nil
}

// OnCompleted Record complete
func (a *LoggingAspect) OnCompleted(ctx context.Context, point *aspect.AgentPoint, output *aspect.AgentOutput) {
	if output.Error != nil {
		a.log("[Aspect:OnCompleted] Agent=%s FAILED: %v", point.AgentName, output.Error)
	} else {
		a.log("[Aspect:OnCompleted] Agent=%s SUCCESS Duration=%dms Tokens=%d/%d/%d",
			point.AgentName,
			output.Duration,
			output.TokenUsage.PromptTokens,
			output.TokenUsage.CompletionTokens,
			output.TokenUsage.TotalTokens)
	}

	// Record tool calls
	if len(output.ToolCalls) > 0 {
		a.log("[Aspect:OnCompleted] ToolCalls=%d", len(output.ToolCalls))
		for _, tc := range output.ToolCalls {
			if tc.Error != nil {
				a.log("[Aspect:OnCompleted]   - Tool=%s ERROR: %v", tc.Name, tc.Error)
			} else {
				a.log("[Aspect:OnCompleted]   - Tool=%s Duration=%dms", tc.Name, tc.Duration)
			}
		}
	}
}

// OnChunk records streaming blocks
func (a *LoggingAspect) OnChunk(ctx context.Context, point *aspect.AgentPoint, chunk *aspect.StreamChunk) error {
	if chunk.IsToolCall {
		a.log("[Aspect:OnChunk] ToolCall ToolName=%s", chunk.ToolName)
	} else if chunk.IsError {
		a.log("[Aspect:OnChunk] ERROR: %s", chunk.Content)
	}
	// Do not record every content block to avoid excessive logging
	return nil
}

// BeforeToolCall records tool calls
func (a *LoggingAspect) BeforeToolCall(ctx context.Context, point *aspect.AgentPoint, call *aspect.ToolCallInfo) (*aspect.ToolCallInfo, error) {
	// Truncate parameters that are too long
	args := call.Arguments
	if len(args) > 200 {
		args = args[:200] + "..."
	}
	a.log("[Aspect:BeforeToolCall] Tool=%s CallId=%s Args=%s", call.Name, call.CallId, args)
	return call, nil
}

// AfterToolCall records the tool results
func (a *LoggingAspect) AfterToolCall(ctx context.Context, point *aspect.AgentPoint, call *aspect.ToolCallInfo, result *aspect.ToolCallResult) error {
	if result.Error != nil {
		a.log("[Aspect:AfterToolCall] Tool=%s CallId=%s ERROR: %v", result.Name, result.CallId, result.Error)
	} else {
		// The result of being cut off too long
		resultStr := result.Result
		if len(resultStr) > 200 {
			resultStr = resultStr[:200] + "..."
		}
		a.log("[Aspect:AfterToolCall] Tool=%s CallId=%s Duration=%dms Result=%s",
			result.Name, result.CallId, result.Duration, resultStr)
	}
	return nil
}

// log internal log method
func (a *LoggingAspect) log(format string, args ...interface{}) {
	if a.logger != nil {
		a.logger.Debugf(format, args...)
	}
}
