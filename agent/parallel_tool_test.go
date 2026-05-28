package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test/assert"

	"github.com/rulego/rulego-components-ai/tool/bash"
)

func init() {
	// 注册 bash 工具
	if err := bash.RegisterDefault(); err != nil {
		panic(err)
	}
}

// ToolCallEvent 记录工具调用事件
type ToolCallEvent struct {
	Name      string    `json:"name"`
	Event     string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	Index     int       `json:"index"`
}

// TestParallelToolCalls 测试并行工具调用
// 验证当模型返回多个工具调用时，是否并行执行
func TestParallelToolCalls(t *testing.T) {
	baseURL, apiKey, model := getTestConfig()
	skipIfNoAPIKey(t, apiKey)

	t.Run("ParallelExecution", func(t *testing.T) {
		// 定义 ReactAgent 规则链，启用并行工具调用
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "parallel_tool_test",
				"name": "Parallel Tool Test",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "react_agent",
						"type": "ai/agent",
						"name": "Parallel Agent",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个高效的助手。重要规则：每个命令必须单独调用一次 bash 工具，不要在单个命令中使用 & 或 ; 连接多个命令。当用户要求执行多个命令时，必须调用多次 bash 工具。",
							"maxStep": 10,
							"tools": [
								{
									"name": "bash",
									"description": "执行 shell 命令",
									"type": "builtin",
									"config": {
										"timeout": 30,
										"whitelist": ["echo", "sleep", "date"]
									}
								}
							]
						}
					}
				],
				"connections": []
			}
		}`, baseURL, apiKey, model)

		config := rulego.NewConfig()
		engine, err := rulego.New("parallel_tool_test", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 记录工具调用事件
		var toolCallEvents []ToolCallEvent

		// 发送消息 - 要求执行多个独立命令（使用 sleep 来更明显地展示并行效果）
		// 如果并行执行：3 个 sleep 2 命令总耗时约 2 秒
		// 如果顺序执行：3 个 sleep 2 命令总耗时约 6 秒
		meta := types.NewMetadata()
		meta.PutValue("stream", "true")
		msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta,
			"请同时执行以下3个命令并告诉我结果：1. sleep 2  2. sleep 2  3. sleep 2")

		done := make(chan string, 1)
		var fullContent strings.Builder

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			if err != nil {
				t.Logf("Error: %v", err)
				done <- ""
				return
			}

			// 检查是否是工具调用事件
			if outMsg.Metadata.GetValue("tool_call") == "true" {
				data := outMsg.GetData()
				t.Logf("Tool call event: %s", truncateString(data, 200))

				// 解析工具调用事件
				var event struct {
					Name      string `json:"name"`
					Event     string `json:"event"`
					Data      string `json:"data"`
					Index     int    `json:"index"`
					Timestamp int64  `json:"timestamp"`
				}
				if parseErr := json.Unmarshal([]byte(data), &event); parseErr == nil {
					if event.Event == "tool_start" {
						toolCallEvents = append(toolCallEvents, ToolCallEvent{
							Name:      event.Name,
							Event:     event.Event,
							Timestamp: time.UnixMilli(event.Timestamp),
							Index:     event.Index,
						})
						t.Logf("Tool start: name=%s, index=%d, timestamp=%v",
							event.Name, event.Index, time.UnixMilli(event.Timestamp))
					}
				}
			}

			// 检查是否是最终结果
			if outMsg.Metadata.GetValue("stream_completed") == "true" &&
				outMsg.Metadata.GetValue("full_content") == "true" {
				fullContent.WriteString(outMsg.GetData())
				done <- outMsg.GetData()
			}
		}))

		select {
		case result := <-done:
			if result == "" {
				t.Error("Response is empty")
				return
			}

			t.Logf("=== Test Results ===")
			t.Logf("Agent Response: %s", result)
			t.Logf("Tool call events count: %d", len(toolCallEvents))

			// 验证是否有多个工具调用
			if len(toolCallEvents) < 2 {
				t.Logf("Warning: Expected at least 2 parallel tool calls, got %d", len(toolCallEvents))
				t.Logf("This might be due to model behavior - it may have chosen to execute sequentially")
			}

			// 检查并行执行：如果多个工具调用的开始时间差小于 100ms，认为是并行的
			if len(toolCallEvents) >= 2 {
				for i := 1; i < len(toolCallEvents); i++ {
					timeDiff := toolCallEvents[i].Timestamp.Sub(toolCallEvents[0].Timestamp)
					t.Logf("Time difference between tool %d and tool 0: %v", i, timeDiff)

					if timeDiff < 100*time.Millisecond {
						t.Logf("✓ Tool %d and tool 0 appear to be executed in parallel (time diff: %v)", i, timeDiff)
					} else {
						t.Logf("✗ Tool %d and tool 0 appear to be executed sequentially (time diff: %v)", i, timeDiff)
					}
				}
			}

		case <-time.After(120 * time.Second):
			t.Error("Timeout waiting for response")
		}
	})
}
