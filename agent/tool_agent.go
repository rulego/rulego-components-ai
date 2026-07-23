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

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ToolAgent wraps InvokableTool as adk.Agent
type ToolAgent struct {
	name        string
	description string
	tool        tool.InvokableTool
}

// NewToolAgent creates a tool agent
func NewToolAgent(name, description string, t tool.InvokableTool) *ToolAgent {
	return &ToolAgent{
		name:        name,
		description: description,
		tool:        t,
	}
}

// Name returns the proxy name
func (a *ToolAgent) Name(ctx context.Context) string {
	return a.name
}

// Description returns the proxy description
func (a *ToolAgent) Description(ctx context.Context) string {
	return a.description
}

// Run implements adk.Agent's Run method
func (a *ToolAgent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()

	go func() {
		defer generator.Close()

		// Retrieve the last user message as tool input
		var content string
		if len(input.Messages) > 0 {
			for i := len(input.Messages) - 1; i >= 0; i-- {
				if input.Messages[i].Role == schema.User {
					content = input.Messages[i].Content
					break
				}
			}
			if content == "" {
				content = input.Messages[len(input.Messages)-1].Content
			}
		}

		// Invoke tools
		result, err := a.tool.InvokableRun(ctx, content)
		if err != nil {
			generator.Send(&adk.AgentEvent{Err: err})
			return
		}

		// Return results
		generator.Send(&adk.AgentEvent{
			Output: &adk.AgentOutput{
				MessageOutput: &adk.MessageVariant{
					Message: &schema.Message{
						Role:    schema.Assistant,
						Content: fmt.Sprintf("%v", result),
					},
				},
			},
		})
	}()

	return iterator
}

// Stream implementation adk.Agent Stream method
func (a *ToolAgent) Stream(ctx context.Context, input *adk.AgentInput) (*schema.StreamReader[*adk.AgentEvent], error) {
	return nil, fmt.Errorf("stream not implemented for ToolAgent")
}
