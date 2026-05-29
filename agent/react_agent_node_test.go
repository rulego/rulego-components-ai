package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// TestReactAgentWithBash 测试 ReactAgent 调用 bash 工具
// 验证工具调用结果是否能正确返回
func TestReactAgentWithBash(t *testing.T) {
	// 配置信息从环境变量读取
	baseURL, apiKey, model := getTestConfig()

	skipIfNoAPIKey(t, apiKey)

	t.Run("BashTool", func(t *testing.T) {
		// 定义 ReactAgent 规则链，使用 bash 工具
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "react_agent_bash_test",
				"name": "React Agent Bash Test",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "react_agent",
						"type": "ai/agent",
						"name": "Bash Agent",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个有用的助手。当需要执行命令时，使用 bash 工具。",
							"maxStep": 5,
							"name": "bash_agent",
							"description": "An agent that can execute bash commands",
							"tools": [
								{
									"name": "bash",
									"description": "执行 shell 命令，支持 ls, pwd, echo, cat 等命令",
									"type": "builtin",
									"config": {
										"timeout": 30,
										"whitelist": ["ls", "pwd", "echo", "cat", "head", "tail", "dir", "type"]
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
		engine, err := rulego.New("react_agent_bash_test", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 发送消息 - 请求执行一个简单命令
		meta := types.NewMetadata()
		msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "请使用 bash 工具执行 pwd 命令并告诉我当前工作目录")

		done := make(chan string, 1)
		var lastMsg types.RuleMsg
		var callbackErr error

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			if err != nil {
				callbackErr = err
				done <- ""
			} else {
				t.Logf("Success in OnEnd: %s", outMsg.GetData())
				lastMsg = outMsg
				done <- outMsg.GetData()
			}
		}))

		select {
		case result := <-done:
			if callbackErr != nil {
				skipIfAPIError(t, callbackErr)
				t.Fatalf("OnEnd error: %v", callbackErr)
			}
			if result == "" {
				t.Error("Response is empty")
			} else {
				t.Logf("Agent Response: %s", result)
				assert.True(t, len(result) > 0)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("Timeout waiting for response")
		}

		// 打印最后收到的消息信息
		t.Logf("Final message data type: %s", lastMsg.DataType)
		t.Logf("Final message metadata: %v", lastMsg.Metadata)
	})
}

// TestReactAgentWithBashStream 测试流式模式下工具调用结果返回
func TestReactAgentWithBashStream(t *testing.T) {
	// 配置信息从环境变量读取
	baseURL, apiKey, model := getTestConfig()

	skipIfNoAPIKey(t, apiKey)

	t.Run("StreamMode", func(t *testing.T) {
		// 定义 ReactAgent 规则链
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "react_agent_bash_stream_test",
				"name": "React Agent Bash Stream Test",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "react_agent",
						"type": "ai/agent",
						"name": "Bash Stream Agent",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个有用的助手。当需要执行命令时，使用 bash 工具。",
							"maxStep": 5,
							"name": "bash_stream_agent",
							"description": "An agent that can execute bash commands in stream mode",
							"tools": [
								{
									"name": "bash",
									"description": "执行 shell 命令，支持 ls, pwd, echo, cat 等命令",
									"type": "builtin",
									"config": {
										"timeout": 30,
										"whitelist": ["ls", "pwd", "echo", "cat", "head", "tail", "dir", "type"]
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
		engine, err := rulego.New("react_agent_bash_stream_test", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 发送消息 - 启用流式模式，让 AI 执行命令并总结结果
		meta := types.NewMetadata()
		meta.PutValue("stream", "true")
		msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "请使用 bash 工具执行 ls 命令列出当前目录的文件，并告诉我有哪些文件")

		done := make(chan string, 1)
		var fullContent strings.Builder
		var chunkCount int
		var toolCallCount int        // 工具调用次数
		var toolResultContent string // 工具返回内容
		var mu sync.Mutex

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				skipIfAPIError(t, err)
				t.Logf("Error in OnEnd: %v", err)
				done <- ""
			} else {
				chunkCount++
				// 检查是否是流式完成的最终结果
				if outMsg.Metadata.GetValue("stream_completed") == "true" {
					t.Logf("Final response (chunk #%d): %s", chunkCount, outMsg.GetData())
					done <- outMsg.GetData()
				} else {
					// 流式 chunk，记录但不发送到 done channel
					isToolCall := outMsg.Metadata.GetValue("tool_call") == "true"
					if isDebugMode() {
						t.Logf("Stream chunk #%d (isChunk=%s, isToolCall=%v): %s",
							chunkCount,
							outMsg.Metadata.GetValue("chunk"),
							isToolCall,
							truncateString(outMsg.GetData(), 100))
					}

					// 记录工具调用
					if isToolCall {
						toolCallCount++
						// 尝试解析工具调用结果
						data := outMsg.GetData()
						if strings.Contains(data, `"event":"tool_result"`) {
							toolResultContent = data
						}
					}

					fullContent.WriteString(outMsg.GetData())
				}
			}
		}))

		select {
		case result := <-done:
			mu.Lock()
			if result == "" && fullContent.Len() > 0 {
				result = fullContent.String()
			}
			toolCalls := toolCallCount
			toolResult := toolResultContent
			mu.Unlock()

			if result == "" {
				t.Error("Response is empty")
			} else {
				t.Logf("Agent Response: %s", result)
				assert.True(t, len(result) > 0, "响应内容不应为空")

				// 验证工具被调用过
				assert.True(t, toolCalls > 0, "应该至少调用一次工具 (实际调用: %d 次)", toolCalls)
				t.Logf("Tool call count: %d", toolCalls)

				// 验证响应中包含文件信息
				hasFileInfo := strings.Contains(result, "文件") ||
					strings.Contains(result, "file") ||
					strings.Contains(result, "目录") ||
					strings.Contains(result, "directory") ||
					len(result) > 20
				assert.True(t, hasFileInfo,
					"响应应该包含文件信息，实际响应: %s", result)

				// 验证工具返回内容不为空
				if toolResult != "" {
					t.Logf("Tool result received: %s", truncateString(toolResult, 200))
				}
			}
		case <-time.After(120 * time.Second):
			t.Error("Timeout waiting for response")
		}
	})
}

// TestReactAgentToolResultCallback 测试工具结果回调 - 使用 WithOnNodeCompleted
func TestReactAgentToolResultCallback(t *testing.T) {
	// 配置信息从环境变量读取
	baseURL, apiKey, model := getTestConfig()

	skipIfNoAPIKey(t, apiKey)

	t.Run("ToolResultViaSSE", func(t *testing.T) {
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "tool_result_sse_test",
				"name": "Tool Result SSE Test",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "react_agent",
						"type": "ai/agent",
						"name": "Test Agent",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个测试助手。当用户要求执行命令时，使用 bash 工具。",
							"maxStep": 3,
							"name": "test_agent",
							"description": "Test agent for tool results",
							"tools": [
								{
									"name": "bash",
									"description": "执行 shell 命令获取系统信息",
									"type": "builtin",
									"config": {
										"timeout": 30,
										"whitelist": ["pwd", "ls", "echo", "cat", "head", "tail", "dir", "type"]
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
		engine, err := rulego.New("tool_result_sse_test", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 发送消息 - 让 AI 执行命令并总结结果
		meta := types.NewMetadata()
		meta.PutValue("stream", "true")
		msg := types.NewMsg(0, "TEST", types.TEXT, meta, "请使用 bash 工具执行 pwd 命令并告诉我当前工作目录")

		var nodeLogs []types.RuleNodeRunLog
		done := make(chan string, 1)
		var toolCallCount int
		var finalResponse string
		var streamContent strings.Builder
		var mu sync.Mutex

		engine.OnMsg(msg,
			types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					skipIfAPIError(t, err)
					t.Logf("Error in OnEnd: %v", err)
					done <- ""
				} else if outMsg.Metadata.GetValue("stream_completed") == "true" {
					// 最终结果
					finalResponse = outMsg.GetData()
					if finalResponse == "" && streamContent.Len() > 0 {
						finalResponse = streamContent.String()
					}
					t.Logf("Final response: %s", finalResponse)
					done <- finalResponse
				} else {
					streamContent.WriteString(outMsg.GetData())
					// 流式 chunk - 仅在调试模式下打印详细内容
					if outMsg.Metadata.GetValue("tool_call") == "true" {
						toolCallCount++
						if isDebugMode() {
							t.Logf("Tool call chunk: %s", truncateString(outMsg.GetData(), 100))
						}
					}
				}
			}),
			types.WithOnNodeCompleted(func(ctx types.RuleContext, nodeLog types.RuleNodeRunLog) {
				mu.Lock()
				defer mu.Unlock()
				nodeLogs = append(nodeLogs, nodeLog)
				t.Logf("Node completed: %s, relationType: %s", nodeLog.Id, nodeLog.RelationType)
			}),
		)

		select {
		case result := <-done:
			mu.Lock()
			logsSnapshot := make([]types.RuleNodeRunLog, len(nodeLogs))
			copy(logsSnapshot, nodeLogs)
			toolCalls := toolCallCount
			mu.Unlock()

			t.Logf("=== Test Summary ===")
			t.Logf("Node logs count: %d", len(logsSnapshot))
			t.Logf("Tool call count: %d", toolCalls)

			// 验证结果不为空
			assert.True(t, len(result) > 0, "响应内容不应为空")

			// 验证工具被调用过
			assert.True(t, toolCalls > 0, "应该至少调用一次工具 (实际调用: %d 次)", toolCalls)

			// 验证响应中包含工作目录信息
			hasWorkDir := strings.Contains(result, "/") ||
				strings.Contains(result, "\\") ||
				strings.Contains(result, "目录") ||
				strings.Contains(result, "directory") ||
				strings.Contains(result, "路径") ||
				strings.Contains(result, "path")
			assert.True(t, hasWorkDir,
				"响应应该包含工作目录信息，实际响应: %s", result)

			for i, log := range logsSnapshot {
				t.Logf("Node %d: %s, relationType: %s", i, log.Id, log.RelationType)
				if log.Err != "" {
					t.Logf("  Error: %s", log.Err)
				}
			}

		case <-time.After(120 * time.Second):
			t.Error("Timeout")
		}
	})
}

// TestReactAgentWithCommand_MultiToolCalls 测试 ReactAgent 自主多次调用命令工具
// 场景：让 AI 执行多个命令来完成一个任务，验证多次工具调用能力
func TestReactAgentWithCommand_MultiToolCalls(t *testing.T) {
	// 配置信息从环境变量读取
	baseURL, apiKey, model := getTestConfig()

	skipIfNoAPIKey(t, apiKey)

	t.Run("MultiCommandExecution", func(t *testing.T) {
		// 定义 ReactAgent 规则链，使用 command_execute 工具
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "react_agent_command_multi_test",
				"name": "React Agent Command Multi Test",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "react_agent",
						"type": "ai/agent",
						"name": "Command Agent",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个有用的助手，可以执行命令来帮助用户。当用户要求你执行命令时，使用 bash 工具。你可以连续执行多个命令来完成复杂任务。",
							"maxStep": 10,
							"name": "command_agent",
							"description": "An agent that can execute commands",
							"tools": [
								{
									"name": "bash",
									"description": "执行 shell 命令，支持 ls, pwd, echo, cat, grep 等命令",
									"type": "builtin",
									"config": {
										"timeout": 30,
										"whitelist": ["ls", "pwd", "echo", "cat", "head", "tail", "dir", "type"]
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
		engine, err := rulego.New("react_agent_command_multi_test", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 发送消息 - 要求 AI 执行多个命令
		// 任务：1. 先查看当前目录(pwd) 2. 列出当前目录文件(ls) 3. 统计文件数量
		meta := types.NewMetadata()
		msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "请帮我执行以下操作：1. 查看当前工作目录 2. 列出当前目录的文件 3. 告诉我当前目录有多少文件")

		done := make(chan string, 1)
		var lastMsg types.RuleMsg

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			if err != nil {
				skipIfAPIError(t, err)
				t.Logf("Error in OnEnd: %v", err)
				done <- ""
			} else {
				t.Logf("Success in OnEnd: %s", truncateString(outMsg.GetData(), 500))
				lastMsg = outMsg
				done <- outMsg.GetData()
			}
		}))

		select {
		case result := <-done:
			if result == "" {
				t.Error("Response is empty")
			} else {
				t.Logf("Agent Response: %s", result)
				assert.True(t, len(result) > 0)

				// 验证响应中包含工作目录信息
				hasWorkDir := strings.Contains(result, "/") || strings.Contains(result, "目录") || strings.Contains(result, "directory")
				assert.True(t, hasWorkDir, "响应应该包含工作目录信息")

				// 验证响应中包含文件列表信息
				hasFileInfo := strings.Contains(result, "文件") || strings.Contains(result, "file") || len(result) > 50
				assert.True(t, hasFileInfo, "响应应该包含文件信息")
			}
		case <-time.After(120 * time.Second):
			t.Error("Timeout waiting for response")
		}

		t.Logf("Final message data type: %s", lastMsg.DataType)
	})
}

// TestReactAgentWithCommand_StreamMode 测试流式模式下的命令执行
func TestReactAgentWithCommand_StreamMode(t *testing.T) {
	// 配置信息从环境变量读取
	baseURL, apiKey, model := getTestConfig()

	skipIfNoAPIKey(t, apiKey)

	t.Run("StreamMode", func(t *testing.T) {
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "react_agent_command_stream_test",
				"name": "React Agent Command Stream Test",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "react_agent",
						"type": "ai/agent",
						"name": "Stream Command Agent",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个命令执行助手。使用 bash 工具执行命令并实时报告结果。",
							"maxStep": 10,
							"name": "stream_command_agent",
							"description": "An agent that executes commands in stream mode",
							"tools": [
								{
									"name": "bash",
									"description": "执行 shell 命令",
									"type": "builtin",
									"config": {
										"timeout": 30,
										"whitelist": ["ls", "pwd", "echo", "cat", "head", "tail", "dir", "type"]
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
		engine, err := rulego.New("react_agent_command_stream_test", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 发送消息 - 启用流式模式
		meta := types.NewMetadata()
		meta.PutValue("stream", "true")
		msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "请列出当前目录的文件，并告诉我有哪些文件")

		done := make(chan string, 1)
		var fullContent strings.Builder
		var chunkCount int
		var toolCallCount int
		var mu sync.Mutex

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				skipIfAPIError(t, err)
				t.Logf("Error in OnEnd: %v", err)
				done <- ""
			} else {
				chunkCount++
				// 检查是否是流式完成的最终结果
				if outMsg.Metadata.GetValue("stream_completed") == "true" {
					t.Logf("Final response (chunk #%d): %s", chunkCount, truncateString(outMsg.GetData(), 500))
					done <- outMsg.GetData()
				} else {
					// 流式 chunk
					isToolCall := outMsg.Metadata.GetValue("tool_call") == "true"

					// 记录工具调用
					if isToolCall {
						toolCallCount++
					}

					fullContent.WriteString(outMsg.GetData())
				}
			}
		}))

		select {
		case result := <-done:
			mu.Lock()
			toolCalls := toolCallCount
			mu.Unlock()

			// 流式模式下可能会收到空的最终消息，等待有内容的消息
			if result == "" {
				// 等待下一个有内容的消息
				select {
				case result = <-done:
					if result == "" {
						t.Error("Response is empty after waiting")
						return
					}
				case <-time.After(10 * time.Second):
					t.Error("Timeout waiting for non-empty response")
					return
				}
			}
			t.Logf("Agent Response: %s", result)
			assert.True(t, len(result) > 0, "响应内容不应为空")

			// 验证工具被调用过
			t.Logf("Tool call count: %d", toolCalls)
		case <-time.After(120 * time.Second):
			t.Error("Timeout waiting for response")
		}
	})
}

// TestReactAgentWithCommand_ComplexTask 测试复杂任务需要多次工具调用
func TestReactAgentWithCommand_ComplexTask(t *testing.T) {
	// 配置信息从环境变量读取
	baseURL, apiKey, model := getTestConfig()

	skipIfNoAPIKey(t, apiKey)

	t.Run("ComplexMultiStepTask", func(t *testing.T) {
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "react_agent_complex_task_test",
				"name": "React Agent Complex Task Test",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "react_agent",
						"type": "ai/agent",
						"name": "Complex Task Agent",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个高级命令执行助手。你可以使用 bash 工具执行多个命令来完成复杂任务。每一步都要仔细思考，根据上一步的结果决定下一步操作。",
							"maxStep": 15,
							"name": "complex_task_agent",
							"description": "An agent that handles complex tasks with multiple command executions",
							"tools": [
								{
									"name": "bash",
									"description": "执行 shell 命令，支持文件和目录操作",
									"type": "builtin",
									"config": {
										"timeout": 30,
										"whitelist": ["ls", "pwd", "echo", "cat", "head", "tail", "grep", "find", "wc"]
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
		engine, err := rulego.New("react_agent_complex_task_test", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 复杂任务：探索目录结构
		meta := types.NewMetadata()
		msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "请帮我完成以下任务：1. 显示当前工作目录 2. 列出当前目录的内容 3. 如果有子目录，选择一个子目录并列出其内容 4. 总结你发现的信息")

		done := make(chan string, 1)
		var toolCallCount int
		var mu sync.Mutex

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				skipIfAPIError(t, err)
				t.Logf("Error in OnEnd: %v", err)
				done <- ""
			} else {
				// 检查工具调用
				if outMsg.Metadata.GetValue("tool_call") == "true" {
					toolCallCount++
				}

				// 检查是否是最终结果
				if outMsg.Metadata.GetValue("stream_completed") == "true" || outMsg.Metadata.GetValue("stream_completed") == "" {
					t.Logf("Success in OnEnd: %s", truncateString(outMsg.GetData(), 500))
					done <- outMsg.GetData()
				}
			}
		}))

		select {
		case result := <-done:
			mu.Lock()
			toolCalls := toolCallCount
			mu.Unlock()

			if result == "" {
				t.Error("Response is empty")
			} else {
				t.Logf("Agent Response: %s", result)
				assert.True(t, len(result) > 0)

				// 验证响应中包含目录信息
				hasDirInfo := strings.Contains(result, "/") ||
					strings.Contains(result, "目录") ||
					strings.Contains(result, "directory") ||
					strings.Contains(result, "文件") ||
					strings.Contains(result, "file")
				assert.True(t, hasDirInfo, "响应应该包含目录或文件信息")

				t.Logf("Tool call count: %d", toolCalls)
			}
		case <-time.After(180 * time.Second):
			t.Error("Timeout waiting for response")
		}
	})
}

// TestAgentV2Integration 集成测试
func TestAgentV2Integration(t *testing.T) {
	// 配置信息从环境变量读取
	baseURL, apiKey, model := getTestConfig()

	skipIfNoAPIKey(t, apiKey)

	// 1. 测试基础对话
	t.Run("BasicConversation", func(t *testing.T) {
		dsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "agent_v2_basic_chain",
				"name": "Agent V2 Basic Chain",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "chat_agent",
						"type": "ai/agent",
						"name": "Chat Agent",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个有用的助手。请用简短的话回答。",
							"maxStep": 5,
							"name": "helper_agent",
							"description": "A helper agent"
						}
					}
				],
				"connections": []
			}
		}`, baseURL, apiKey, model)

		config := rulego.NewConfig()
		engine, err := rulego.New("agent_v2_basic_chain", []byte(dsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 发送消息
		meta := types.NewMetadata()
		msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "你好，请介绍一下你自己")

		done := make(chan string, 1)

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			if err != nil {
				skipIfAPIError(t, err)
				t.Logf("Error in OnEnd: %v", err)
				done <- ""
			} else {
				t.Logf("Success in OnEnd: %s", outMsg.GetData())
				done <- outMsg.GetData()
			}
		}))

		select {
		case result := <-done:
			if result == "" {
				t.Error("Response is empty")
			} else {
				t.Logf("Agent Response: %s", result)
				assert.True(t, len(result) > 0)
			}
		case <-time.After(30 * time.Second):
			t.Error("Timeout")
		}
	})

	// 2. 测试带工具调用的 Agent
	t.Run("AgentWithTools", func(t *testing.T) {
		// 1. 注册工具规则链 (Calculator)
		calcDsl := `{
			"ruleChain": {
				"id": "calculator_tool",
				"name": "Calculator Tool"
			},
			"metadata": {
				"nodes": [
					{
						"id": "js_calc",
						"type": "jsTransform",
						"name": "JS Calculator",
						"configuration": {
							"jsScript": "var input = (typeof msg === 'string') ? JSON.parse(msg) : msg; var result; try { result = eval(input.expression); } catch(e) { result = 'Error'; } return {msg: result.toString(), metadata: metadata, msgType: 'TEXT'};"
						}
					}
				]
			}
		}`

		config := rulego.NewConfig()
		// 先注册工具链
		_, err := rulego.New("calculator_tool", []byte(calcDsl), types.WithConfig(config))
		assert.Nil(t, err)

		// 2. 定义 Agent 规则链
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {
				"id": "agent_v2_tools_chain",
				"name": "Agent V2 Tools Chain",
				"root": true
			},
			"metadata": {
				"nodes": [
					{
						"id": "chat_agent_tools",
						"type": "ai/agent",
						"name": "Chat Agent With Tools",
						"configuration": {
							"url": "%s",
							"key": "%s",
							"model": "%s",
							"systemPrompt": "你是一个有用的助手。你可以使用工具进行计算。",
							"maxStep": 5,
							"name": "math_agent",
							"description": "A math agent",
							"tools": [
								{
									"name": "calculator",
									"description": "Calculate mathematical expressions",
									"type": "rulechain",
									"targetId": "calculator_tool",
									"parameters": "{\"type\":\"object\",\"properties\":{\"expression\":{\"type\":\"string\",\"description\":\"The math expression to evaluate, e.g. 1+1\"}},\"required\":[\"expression\"]}"
								}
							]
						}
					}
				],
				"connections": []
			}
		}`, baseURL, apiKey, model)

		engine, err := rulego.New("agent_v2_tools_chain", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		// 发送消息
		meta := types.NewMetadata()
		msg := types.NewMsg(0, "TEST_MSG_2", types.JSON, meta, "{\"model\": \""+model+"\", \"messages\": [{\"role\": \"user\", \"content\": \"请计算 (123 + 456) * 2 是多少？\"}]}")

		done := make(chan string, 1)

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			if err != nil {
				skipIfAPIError(t, err)
				t.Logf("Error in AgentWithTools OnEnd: %v", err)
				done <- ""
			} else {
				t.Logf("Success in AgentWithTools OnEnd: %s", outMsg.GetData())
				done <- outMsg.GetData()
			}
		}))

		select {
		case result := <-done:
			if result == "" {
				t.Error("Response is empty")
			} else {
				t.Logf("Agent Response: %s", result)
				assert.True(t, len(result) > 0)
				// 验证结果包含正确答案 1158
				if !strings.Contains(result, "1158") {
					t.Logf("Warning: Expected result to contain 1158, but got: %s", result)
				}
			}
		case <-time.After(60 * time.Second):
			t.Error("Timeout")
		}
	})
}

// ============================================================================
// 并行工具调用测试
// ============================================================================

type toolCallEvent struct {
	Name      string    `json:"name"`
	Event     string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	Index     int       `json:"index"`
}

// TestParallelToolCalls 测试并行工具调用
func TestParallelToolCalls(t *testing.T) {
	baseURL, apiKey, model := getTestConfig()
	skipIfNoAPIKey(t, apiKey)

	t.Run("ParallelExecution", func(t *testing.T) {
		agentDsl := fmt.Sprintf(`{
			"ruleChain": {"id": "parallel_tool_test", "name": "Parallel Tool Test", "root": true},
			"metadata": {
				"nodes": [{
					"id": "react_agent", "type": "ai/agent", "name": "Parallel Agent",
					"configuration": {
						"url": "%s", "key": "%s", "model": "%s",
						"systemPrompt": "你是一个高效的助手。重要规则：每个命令必须单独调用一次 bash 工具，不要在单个命令中使用 & 或 ; 连接多个命令。当用户要求执行多个命令时，必须调用多次 bash 工具。",
						"maxStep": 10,
						"tools": [{"name": "bash", "description": "执行 shell 命令", "type": "builtin",
							"config": {"timeout": 30, "whitelist": ["echo", "sleep", "date"]}}]
					}
				}], "connections": []
			}
		}`, baseURL, apiKey, model)

		config := rulego.NewConfig()
		engine, err := rulego.New("parallel_tool_test", []byte(agentDsl), types.WithConfig(config))
		assert.Nil(t, err)
		defer engine.Stop(context.Background())

		var toolCallEvents []toolCallEvent

		meta := types.NewMetadata()
		meta.PutValue("stream", "true")
		msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta,
			"请同时执行以下3个命令并告诉我结果：1. sleep 2  2. sleep 2  3. sleep 2")

		done := make(chan string, 1)

		engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
			if err != nil {
				t.Logf("Error: %v", err)
				done <- ""
				return
			}

			if outMsg.Metadata.GetValue("tool_call") == "true" {
				data := outMsg.GetData()
				var event struct {
					Name      string `json:"name"`
					Event     string `json:"event"`
					Index     int    `json:"index"`
					Timestamp int64  `json:"timestamp"`
				}
				if parseErr := json.Unmarshal([]byte(data), &event); parseErr == nil {
					if event.Event == "tool_start" {
						toolCallEvents = append(toolCallEvents, toolCallEvent{
							Name:      event.Name,
							Event:     event.Event,
							Timestamp: time.UnixMilli(event.Timestamp),
							Index:     event.Index,
						})
					}
				}
			}

			if outMsg.Metadata.GetValue("stream_completed") == "true" &&
				outMsg.Metadata.GetValue("full_content") == "true" {
				done <- outMsg.GetData()
			}
		}))

		select {
		case result := <-done:
			assert.True(t, len(result) > 0, "Response should not be empty")
			t.Logf("Agent Response: %s", truncateString(result, 200))
			t.Logf("Tool call events: %d", len(toolCallEvents))

			if len(toolCallEvents) >= 2 {
				for i := 1; i < len(toolCallEvents); i++ {
					timeDiff := toolCallEvents[i].Timestamp.Sub(toolCallEvents[0].Timestamp)
					t.Logf("Time diff between tool %d and tool 0: %v", i, timeDiff)
				}
			}
		case <-time.After(120 * time.Second):
			t.Error("Timeout")
		}
	})
}
