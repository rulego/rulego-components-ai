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

// ToolAgent 包装 InvokableTool 作为 adk.Agent
type ToolAgent struct {
	name        string
	description string
	tool        tool.InvokableTool
}

// NewToolAgent 创建工具代理
func NewToolAgent(name, description string, t tool.InvokableTool) *ToolAgent {
	return &ToolAgent{
		name:        name,
		description: description,
		tool:        t,
	}
}

// Name 返回代理名称
func (a *ToolAgent) Name(ctx context.Context) string {
	return a.name
}

// Description 返回代理描述
func (a *ToolAgent) Description(ctx context.Context) string {
	return a.description
}

// Run 实现 adk.Agent 的 Run 方法
func (a *ToolAgent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()

	go func() {
		defer generator.Close()

		// 获取最后一条用户消息作为工具输入
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

		// 调用工具
		result, err := a.tool.InvokableRun(ctx, content)
		if err != nil {
			generator.Send(&adk.AgentEvent{Err: err})
			return
		}

		// 返回结果
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

// Stream 实现 adk.Agent 的 Stream 方法
func (a *ToolAgent) Stream(ctx context.Context, input *adk.AgentInput) (*schema.StreamReader[*adk.AgentEvent], error) {
	return nil, fmt.Errorf("stream not implemented for ToolAgent")
}
