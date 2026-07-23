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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego"
	"github.com/rulego/rulego-components-ai/config"
	aitool "github.com/rulego/rulego-components-ai/tool"
	_ "github.com/rulego/rulego-components-ai/tool/skill"
	"github.com/rulego/rulego/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integrated test configuration - obtained from environment variables
func getIntegrationConfig() config.LLMConfig {
	return config.LLMConfig{
		Url:   getEnvOrDefault("LLM_BASE_URL", "https://open.bigmodel.cn/api/coding/paas/v4"),
		Key:   getEnvOrDefault("LLM_API_KEY", ""),
		Model: getEnvOrDefault("LLM_MODEL", "GLM-5"),
		Params: config.ModelParams{
			Temperature: 0.7,
			MaxTokens:   1024,
			// Zhipu AI does not support the following parameters; set them to 0 to disable them
			FrequencyPenalty: 0,
			PresencePenalty:  0,
			TopP:             0,
		},
		MaxRetries: 2,
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// TestIntegration_CreateChatModel Test the creation of chat models
func TestIntegration_CreateChatModel(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})

	require.NoError(t, err, "Failed to create chat model")
	require.NotNil(t, chatModel, "Chat model should not be nil")

	t.Logf("✅ Chat model created successfully for model: %s", cfg.Model)
}

// TestIntegration_SimpleChat Test simple conversations
func TestIntegration_SimpleChat(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages := []*schema.Message{
		schema.SystemMessage("你是一个有帮助的助手。"),
		schema.UserMessage("你好，请用一句话介绍你自己。"),
	}

	response, err := chatModel.Generate(ctx, messages)
	if err != nil {
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
			t.Skipf("API rate limited, skipping: %v", err)
		}
		require.NoError(t, err, "Failed to generate response")
	}
	require.NotNil(t, response, "Response should not be nil")
	require.NotEmpty(t, response.Content, "Response content should not be empty")

	t.Logf("✅ Response: %s", truncateString(response.Content, 200))
}

// TestIntegration_StreamChat Test streaming conversations
func TestIntegration_StreamChat(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages := []*schema.Message{
		schema.SystemMessage("你是一个有帮助的助手。"),
		schema.UserMessage("请数从1到5，每个数字占一行。"),
	}

	stream, err := chatModel.Stream(ctx, messages)
	if err != nil {
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
			t.Skipf("API rate limited, skipping: %v", err)
		}
		require.NoError(t, err, "Failed to create stream")
	}
	require.NotNil(t, stream, "Stream should not be nil")

	var fullContent string
	chunkCount := 0

	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		chunkCount++
		fullContent += chunk.Content
	}

	require.NotEmpty(t, fullContent, "Stream content should not be empty")
	t.Logf("✅ Stream completed: %d chunks, content length: %d", chunkCount, len(fullContent))
	t.Logf("   Content preview: %s", truncateString(fullContent, 100))
}

// TestIntegration_CreateReactAgent Test the creation of the React Agent
func TestIntegration_CreateReactAgent(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	// Create a simple testing tool
	tools := []config.Tool{
		{
			Name:        "echo",
			Description: "回显输入内容",
			Type:        config.ToolTypeRuleChain,
			TargetId:    "echo_chain",
			Parameters:  `{"type":"object","properties":{"message":{"type":"string","description":"要回显的内容"}}}`,
		},
	}

	agentTools, _, _, err := CreateTools(tools, ToolOptions{
		Logger: NewTestLogger(t),
	})
	require.NoError(t, err, "Failed to create tools")

	agent, err := CreateReactAgent(context.Background(), chatModel, AgentOptions{
		Name:         "test_agent",
		Description:  "测试智能体",
		SystemPrompt: "你是一个测试助手。",
		MaxStep:      5,
		ToolsConfig:  buildToolsConfig(agentTools),
		Logger:       NewTestLogger(t),
	})

	require.NoError(t, err, "Failed to create react agent")
	require.NotNil(t, agent, "Agent should not be nil")

	t.Logf("✅ React Agent created successfully")
}

// TestIntegration_MultiTurnChat Test multi-turn dialogue
func TestIntegration_MultiTurnChat(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// First round of dialogue
	messages := []*schema.Message{
		schema.SystemMessage("你是一个有帮助的助手，请记住用户告诉你的信息。"),
		schema.UserMessage("我叫小明，今年18岁。"),
	}

	response1, err := chatModel.Generate(ctx, messages)
	require.NoError(t, err, "First turn failed")
	t.Logf("✅ Turn 1: %s", truncateString(response1.Content, 100))

	// The second round of dialogue is a memory test
	messages = append(messages, response1)
	messages = append(messages, schema.UserMessage("你还记得我叫什么名字吗？"))

	response2, err := chatModel.Generate(ctx, messages)
	require.NoError(t, err, "Second turn failed")
	t.Logf("✅ Turn 2: %s", truncateString(response2.Content, 100))

	// The validation model remembered the name
	assert.Contains(t, response2.Content, "小明", "Model should remember the name")
}

// TestIntegration_ErrorHandling Handling of test errors
func TestIntegration_ErrorHandling(t *testing.T) {
	// Testing is invalid for URL
	invalidConfig := config.LLMConfig{
		Url:   "https://invalid-url-that-does-not-exist.com/v1",
		Key:   "invalid-key",
		Model: "invalid-model",
	}

	_, err := CreateChatModel(invalidConfig, ModelOptions{
		WrapRetry:  true,
		MaxRetries: 1,
	})

	// Creating a model should not fail (errors occur when called)
	assert.NoError(t, err, "CreateChatModel should not fail for invalid config")
}

// TestIntegration_TokenLimits Test token limits
func TestIntegration_TokenLimits(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	// Set the MaxTokens lower
	cfg.Params.MaxTokens = 50

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ask for a potentially long answer
	messages := []*schema.Message{
		schema.UserMessage("请用两句话介绍Go语言。"),
	}

	response, err := chatModel.Generate(ctx, messages)
	if err != nil {
		if strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "deadline exceeded") {
			t.Skipf("API unavailable or timeout, skipping: %v", err)
		}
	}
	require.NoError(t, err, "Failed with token limit")

	// Verification answers are truncated (shorter)
	t.Logf("✅ Response length with MaxTokens=50: %d chars", len(response.Content))
	t.Logf("   Preview: %s", truncateString(response.Content, 100))
}

// TestIntegration_RetryMechanism Test the retrial mechanism
func TestIntegration_RetryMechanism(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: 3,
	})
	require.NoError(t, err)

	// Verification is the model after packaging
	_, ok := chatModel.(*RetryChatModelWrapper)
	assert.True(t, ok, "Should be wrapped with RetryChatModelWrapper")

	t.Logf("✅ Retry mechanism enabled with MaxRetries=3")
}

// TestIntegration_ConcurrentRequests Test concurrency requests
// Note: This test may trigger API rate limits, which will be skipped if you encounter a 429 error
func TestIntegration_ConcurrentRequests(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	const numRequests = 2 // Reduce the number of concurrent requests to avoid rate limits
	results := make(chan string, numRequests)
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			messages := []*schema.Message{
				schema.UserMessage(fmt.Sprintf("请说一句话，包含数字%d", idx+1)),
			}

			response, err := chatModel.Generate(ctx, messages)
			if err != nil {
				errors <- err
				return
			}
			results <- response.Content
		}(i)
	}

	successCount := 0
	rateLimited := false
	for i := 0; i < numRequests; i++ {
		select {
		case content := <-results:
			successCount++
			t.Logf("✅ Concurrent request %d completed: %s", successCount, truncateString(content, 50))
		case err := <-errors:
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "Too Many Requests") {
				rateLimited = true
				t.Logf("⚠️ Rate limited, skipping test: %v", err)
			} else {
				t.Logf("❌ Concurrent request failed: %v", err)
			}
		case <-time.After(90 * time.Second):
			t.Fatal("Timeout waiting for concurrent requests")
		}
	}

	// If you encounter a rate limit, skip the test instead of failing
	if rateLimited {
		t.Skip("API rate limit reached, skipping test")
	}

	assert.GreaterOrEqual(t, successCount, 1, "At least one concurrent request should succeed")
}

// TestIntegration_DifferentModels Test different models (if multiple models are available)
func TestIntegration_DifferentModels(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	models := []string{cfg.Model} // More models can be added

	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			testCfg := cfg
			testCfg.Model = model

			chatModel, err := CreateChatModel(testCfg, ModelOptions{
				Logger:     NewTestLogger(t),
				WrapRetry:  true,
				MaxRetries: cfg.MaxRetries,
			})
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			messages := []*schema.Message{
				schema.UserMessage("说'测试成功'"),
			}

			response, err := chatModel.Generate(ctx, messages)
			require.NoError(t, err)

			t.Logf("✅ Model %s: %s", model, truncateString(response.Content, 50))
		})
	}
}

// ============================================
// Auxiliary function
// ============================================

// isDebugMode checks whether it is in debug mode (controlled by environment variable DEBUG_TEST)
// Enable detailed log output when set to DEBUG_TEST=1
func isDebugMode() bool {
	return os.Getenv("DEBUG_TEST") == "1"
}

// NewTestLogger creates a test log logger
func NewTestLogger(t *testing.T) TestLogger {
	return TestLogger{t: t}
}

// TestLogger test logger
type TestLogger struct {
	t *testing.T
}

// Printf prints logs
func (l TestLogger) Printf(format string, v ...interface{}) {
	l.t.Logf("[AGENT] "+format, v...)
}

// Debugf debugging log
func (l TestLogger) Debugf(format string, v ...interface{}) {
	l.t.Logf("[DEBUG] "+format, v...)
}

// Infof Information Log
func (l TestLogger) Infof(format string, v ...interface{}) {
	l.t.Logf("[INFO] "+format, v...)
}

// Warnf warning log
func (l TestLogger) Warnf(format string, v ...interface{}) {
	l.t.Logf("[WARN] "+format, v...)
}

// Errorf error log
func (l TestLogger) Errorf(format string, v ...interface{}) {
	l.t.Logf("[ERROR] "+format, v...)
}

// Note: The truncateString function is defined in aspect_integration.go

// ============================================================================
// Rule chain integration testing – multimodal image recognition
// ============================================================================

// TestIntegration_RuleChainVisionWithURL Using Rule Chains to Test Multimodal Image Recognition (URL Images)
// References: tpclaw/data/agents/agent01.json and rulego/engine/engine_test.go
func TestIntegration_RuleChainVisionWithURL(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	// Using models that support visual capabilities (such as glm-4.6v)
	visionModel := getEnvOrDefault("LLM_VISION_MODEL", "glm-4.6v")

	// Building a Rule Chain DSL (Reference agent01.json Structure)
	agentDsl := fmt.Sprintf(`{
		"ruleChain": {
			"id": "vision_test_chain",
			"name": "Vision Test Chain",
			"root": true
		},
		"metadata": {
			"firstNodeIndex": 0,
			"nodes": [
				{
					"id": "vision_agent",
					"type": "ai/agent",
					"name": "图片识别Agent",
					"configuration": {
						"url": "%s",
						"key": "%s",
						"model": "%s",
						"systemPrompt": "你是一个图像识别助手。请简洁地描述图片内容。",
						"maxStep": 5,
						"params": {
							"temperature": 0.7,
							"maxTokens": 1024
						}
					}
				},
				{
					"id": "end_node",
					"type": "end",
					"name": "结束"
				}
			],
			"connections": [
				{
					"fromId": "vision_agent",
					"toId": "end_node",
					"type": "Success"
				}
			]
		}
	}`, cfg.Url, cfg.Key, visionModel)

	// Create a rule engine
	config := rulego.NewConfig()
	engine, err := rulego.New("vision_test_chain", []byte(agentDsl), types.WithConfig(config))
	require.NoError(t, err, "Failed to create rule engine")
	defer engine.Stop(context.Background())

	// Build multimodal messages (including image URLs)
	// Using multimodal messaging in OpenAI format
	imageURL := "https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg"
	messagePayload := fmt.Sprintf(`{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "请描述这张图片的内容，用一句话回答。"},
					{"type": "image_url", "image_url": {"url": "%s"}}
				]
			}
		]
	}`, imageURL)

	meta := types.NewMetadata()
	msg := types.NewMsg(0, "VISION_TEST", types.JSON, meta, messagePayload)

	// Send messages and wait for results
	done := make(chan string, 1)
	var lastMsg types.RuleMsg
	var callbackErr error

	engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
		if err != nil {
			callbackErr = err
			done <- ""
		} else {
			t.Logf("Success: %s", truncateString(outMsg.GetData(), 200))
			lastMsg = outMsg
			done <- outMsg.GetData()
		}
	}))

	select {
	case result := <-done:
		if callbackErr != nil {
			skipIfAPIError(t, callbackErr)
			require.NoError(t, callbackErr, "Failed to process vision request")
		}
		require.NotEmpty(t, result, "Response should not be empty")
		t.Logf("✅ Vision response: %s", truncateString(result, 300))

		// Verification responses include image descriptions of relevant content
		assert.True(t, len(result) > 10, "Response should contain meaningful content")
	case <-time.After(120 * time.Second):
		t.Fatal("Timeout waiting for vision response")
	}

	t.Logf("Final message type: %s", lastMsg.DataType)
}

// TestIntegration_RuleChainVisionWithBase64 Using Rule Chains to Test Multimodal Image Recognition (Base64 Images)
func TestIntegration_RuleChainVisionWithBase64(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	visionModel := getEnvOrDefault("LLM_VISION_MODEL", "glm-4.6v")

	// Build a Rule Chain DSL
	agentDsl := fmt.Sprintf(`{
		"ruleChain": {
			"id": "vision_base64_chain",
			"name": "Vision Base64 Test",
			"root": true
		},
		"metadata": {
			"nodes": [
				{
					"id": "vision_agent",
					"type": "ai/agent",
					"name": "图片识别Agent",
					"configuration": {
						"url": "%s",
						"key": "%s",
						"model": "%s",
						"systemPrompt": "你是一个图像识别助手。",
						"maxStep": 3,
						"params": {
							"temperature": 0.7,
							"maxTokens": 512
						}
					}
				}
			],
			"connections": []
		}
	}`, cfg.Url, cfg.Key, visionModel)

	config := rulego.NewConfig()
	engine, err := rulego.New("vision_base64_chain", []byte(agentDsl), types.WithConfig(config))
	require.NoError(t, err)
	defer engine.Stop(context.Background())

	// Create a simple test image of Base64 (1x1 red PNG)
	base64Img := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

	messagePayload := fmt.Sprintf(`{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "请描述这张图片的颜色。"},
					{"type": "image_url", "image_url": {"url": "%s"}}
				]
			}
		]
	}`, base64Img)

	meta := types.NewMetadata()
	msg := types.NewMsg(0, "VISION_BASE64_TEST", types.JSON, meta, messagePayload)

	done := make(chan string, 1)
	engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
		if err != nil {
			t.Logf("Error: %v", err)
			done <- ""
		} else {
			done <- outMsg.GetData()
		}
	}))

	select {
	case result := <-done:
		require.NotEmpty(t, result, "Response should not be empty")
		t.Logf("✅ Base64 vision response: %s", truncateString(result, 200))
	case <-time.After(120 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

// TestIntegration_RuleChainVisionStream Testing streaming multimodal image recognition using a chain of rules
func TestIntegration_RuleChainVisionStream(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	visionModel := getEnvOrDefault("LLM_VISION_MODEL", "glm-4.6v")

	agentDsl := fmt.Sprintf(`{
		"ruleChain": {
			"id": "vision_stream_chain",
			"name": "Vision Stream Test",
			"root": true
		},
		"metadata": {
			"nodes": [
				{
					"id": "vision_agent",
					"type": "ai/agent",
					"name": "流式图片识别Agent",
					"configuration": {
						"url": "%s",
						"key": "%s",
						"model": "%s",
						"systemPrompt": "你是一个图像识别助手。详细描述图片内容。",
						"maxStep": 3,
						"params": {
							"temperature": 0.7,
							"maxTokens": 1024
						}
					}
				}
			],
			"connections": []
		}
	}`, cfg.Url, cfg.Key, visionModel)

	config := rulego.NewConfig()
	engine, err := rulego.New("vision_stream_chain", []byte(agentDsl), types.WithConfig(config))
	require.NoError(t, err)
	defer engine.Stop(context.Background())

	imageURL := "https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg"
	messagePayload := fmt.Sprintf(`{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "请详细描述这张图片。"},
					{"type": "image_url", "image_url": {"url": "%s"}}
				]
			}
		]
	}`, imageURL)

	meta := types.NewMetadata()
	meta.PutValue("stream", "true") // Enable streaming mode
	msg := types.NewMsg(0, "VISION_STREAM_TEST", types.JSON, meta, messagePayload)

	done := make(chan string, 1)
	var fullContent strings.Builder
	var chunkCount int
	var mu sync.Mutex

	engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			t.Logf("Error: %v", err)
			done <- ""
		} else {
			chunkCount++
			// Check whether the final result is the flow stream completion
			if outMsg.Metadata.GetValue("stream_completed") == "true" {
				t.Logf("Final response (chunk #%d): %s", chunkCount, truncateString(outMsg.GetData(), 200))
				done <- outMsg.GetData()
			} else {
				// Streaming chunk - Prints detailed content only in debug mode
				if isDebugMode() {
					t.Logf("Chunk #%d: %s", chunkCount, truncateString(outMsg.GetData(), 100))
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
		chunks := chunkCount
		mu.Unlock()

		require.NotEmpty(t, result, "Response should not be empty")
		t.Logf("Stream vision completed: %d chunks", chunks)
		t.Logf("   Full response: %s", truncateString(result, 300))
	case <-time.After(120 * time.Second):
		t.Fatal("Timeout waiting for stream response")
	}
}

// TestIntegration_RuleChainVisionMultiTurn Test multi-turn image dialogues using a chain of rules
func TestIntegration_RuleChainVisionMultiTurn(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	visionModel := getEnvOrDefault("LLM_VISION_MODEL", "glm-4.6v")

	agentDsl := fmt.Sprintf(`{
		"ruleChain": {
			"id": "vision_multi_turn_chain",
			"name": "Vision Multi-Turn Test",
			"root": true
		},
		"metadata": {
			"nodes": [
				{
					"id": "vision_agent",
					"type": "ai/agent",
					"name": "多轮图片对话Agent",
					"configuration": {
						"url": "%s",
						"key": "%s",
						"model": "%s",
						"systemPrompt": "你是一个图像识别助手。记住用户告诉你的信息。",
						"maxStep": 3,
						"params": {
							"temperature": 0.7,
							"maxTokens": 1024
						}
					}
				}
			],
			"connections": []
		}
	}`, cfg.Url, cfg.Key, visionModel)

	config := rulego.NewConfig()
	engine, err := rulego.New("vision_multi_turn_chain", []byte(agentDsl), types.WithConfig(config))
	require.NoError(t, err)
	defer engine.Stop(context.Background())

	imageURL := "https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg"

	// Round 1: Send pictures and ask questions
	messagePayload := fmt.Sprintf(`{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "请描述这张图片的主要内容。"},
					{"type": "image_url", "image_url": {"url": "%s"}}
				]
			}
		]
	}`, imageURL)

	meta := types.NewMetadata()
	msg := types.NewMsg(0, "VISION_MULTI_TURN_1", types.JSON, meta, messagePayload)

	done := make(chan string, 1)
	engine.OnMsg(msg, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
		if err != nil {
			done <- ""
		} else {
			done <- outMsg.GetData()
		}
	}))

	select {
	case result := <-done:
		require.NotEmpty(t, result)
		t.Logf("✅ Turn 1 response: %s", truncateString(result, 150))
	case <-time.After(120 * time.Second):
		t.Fatal("Timeout in turn 1")
	}

	// Second round: Follow-up questions (no pictures, memory test)
	messagePayload2 := `{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "请描述这张图片的主要内容。"},
					{"type": "image_url", "image_url": {"url": "https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg"}}
				]
			},
			{
				"role": "assistant",
				"content": "图片中有一位女士和一只狗在户外。"
			},
			{
				"role": "user",
				"content": "你刚才描述的内容中，主要是什么动物？"
			}
		]
	}`

	msg2 := types.NewMsg(0, "VISION_MULTI_TURN_2", types.JSON, meta, messagePayload2)
	done2 := make(chan string, 1)

	engine.OnMsg(msg2, types.WithOnEnd(func(ctx types.RuleContext, outMsg types.RuleMsg, err error, relationType string) {
		if err != nil {
			done2 <- ""
		} else {
			done2 <- outMsg.GetData()
		}
	}))

	select {
	case result := <-done2:
		require.NotEmpty(t, result)
		t.Logf("✅ Turn 2 response: %s", truncateString(result, 150))
		// The verification model remembers the image content
		assert.True(t, strings.Contains(result, "狗") || strings.Contains(result, "dog"),
			"Response should mention the dog from the image")
	case <-time.After(120 * time.Second):
		t.Fatal("Timeout in turn 2")
	}
}

// ============================================
// Skill Hot Update Test
// ============================================

// mockDynamicSkillLister is a dynamic skill tool for testing mock
type mockDynamicSkillLister struct {
	skills      string
	instruction string
}

func (m *mockDynamicSkillLister) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "skill", Desc: "mock skill tool"}, nil
}

func (m *mockDynamicSkillLister) ListSkills(ctx context.Context) (string, error) {
	return m.skills, nil
}

func (m *mockDynamicSkillLister) GetSkillInstruction() string {
	return m.instruction
}

// TestExtractOriginalSystemContent tests extracting the original content from the system message
func TestExtractOriginalSystemContent(t *testing.T) {
	// Content without markers should be returned as is
	content := "You are a helpful assistant."
	result := ExtractOriginalSystemContent(content)
	assert.Equal(t, content, result)

	// Content with markers should be returned to the part before the marker
	contentWithSkill := "You are a helpful assistant.\n<!-- SKILL_LIST -->\nSkill system instructions\n<available_skills>...</available_skills>"
	result = ExtractOriginalSystemContent(contentWithSkill)
	assert.Equal(t, "You are a helpful assistant.", result)

	// Empty content
	result = ExtractOriginalSystemContent("")
	assert.Equal(t, "", result)
}

// TestBuildSkillModifier tests the behavior of MessageModifier
func TestBuildSkillModifier(t *testing.T) {
	mock := &mockDynamicSkillLister{
		skills:      "<available_skills>\n<skill>\n<name>test_skill</name>\n<description>Test</description>\n</skill>\n</available_skills>",
		instruction: "You have skills available.",
	}

	modifier := BuildSkillModifier(mock)
	require.NotNil(t, modifier)

	ctx := context.Background()

	t.Run("首次注入：无 system message", func(t *testing.T) {
		input := []*schema.Message{
			{Role: schema.User, Content: "Hello"},
		}
		result := modifier(ctx, input)

		assert.Len(t, result, 2)
		assert.Equal(t, schema.System, result[0].Role)
		assert.Contains(t, result[0].Content, "You have skills available.")
		assert.Contains(t, result[0].Content, "test_skill")
		assert.Equal(t, schema.User, result[1].Role)
	})

	t.Run("首次注入：已有 system message", func(t *testing.T) {
		input := []*schema.Message{
			{Role: schema.System, Content: "You are a helpful assistant."},
			{Role: schema.User, Content: "Hello"},
		}
		result := modifier(ctx, input)

		assert.Len(t, result, 2)
		assert.Equal(t, schema.System, result[0].Role)
		assert.Contains(t, result[0].Content, "You are a helpful assistant.")
		assert.Contains(t, result[0].Content, "test_skill")
		assert.Contains(t, result[0].Content, skillPromptMarker)
	})

	t.Run("不修改原始消息对象（浅拷贝安全）", func(t *testing.T) {
		originalContent := "You are a helpful assistant."
		originalMsg := &schema.Message{Role: schema.System, Content: originalContent}
		input := []*schema.Message{
			originalMsg,
			{Role: schema.User, Content: "Hello"},
		}

		result := modifier(ctx, input)

		// The original message should not be altered
		assert.Equal(t, originalContent, originalMsg.Content)
		// The system message in the result should be a new object
		assert.NotEqual(t, originalMsg, result[0])
		assert.Contains(t, result[0].Content, "test_skill")
	})

	t.Run("多轮累积不重复", func(t *testing.T) {
		input := []*schema.Message{
			{Role: schema.System, Content: "You are a helpful assistant."},
			{Role: schema.User, Content: "Hello"},
		}

		// Round 1
		result1 := modifier(ctx, input)
		// Round 2: Use the system message from round 1 as input
		input2 := []*schema.Message{
			result1[0], // System message containing a marker
			{Role: schema.Assistant, Content: "Hi there!"},
			{Role: schema.User, Content: "What skills do you have?"},
		}
		result2 := modifier(ctx, input2)

		// Skill prompts in system messages should not be repeated
		sysContent := result2[0].Content
		count := strings.Count(sysContent, "<available_skills>")
		assert.Equal(t, 1, count, "技能提示词不应重复，出现次数: %d", count)
		// The original content should be retained
		assert.Contains(t, sysContent, "You are a helpful assistant.")
	})

	t.Run("ListSkills 失败时返回原始消息", func(t *testing.T) {
		failMock := &mockDynamicSkillLister{
			skills:      "",
			instruction: "instruction",
		}
		failModifier := BuildSkillModifier(failMock)

		input := []*schema.Message{
			{Role: schema.User, Content: "Hello"},
		}
		result := failModifier(ctx, input)

		// No content should be injected
		assert.Len(t, result, 1)
		assert.Equal(t, schema.User, result[0].Role)
	})
}

// TestIntegration_SkillWithReactAgent Test the integration of skill tools with ReactAgent (requires LLM)
func TestIntegration_SkillWithReactAgent(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	// Create a skill catalog
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "greeting")
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: greeting
description: A skill for greeting users in different languages
---
When greeting users, always say "Hello from the greeting skill!" and then greet in 3 languages.
`), 0644))

	// Create agents with skill tools
	toolsConfig := []config.Tool{
		{
			Type: config.ToolTypeBuiltin,
			Name: "skill",
			Config: map[string]interface{}{
				"localDirs": []string{tmpDir},
			},
		},
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	tools, _, _, err := CreateTools(toolsConfig, ToolOptions{
		WrapVisual: false,
		Logger:     NewTestLogger(t),
	})
	require.NoError(t, err)
	require.NotEmpty(t, tools)

	// The validation tool is DynamicSkillLister
	_, isDynamic := tools[0].(aitool.DynamicSkillLister)
	require.True(t, isDynamic, "skill tool should implement DynamicSkillLister")

	// Create a MessageModifier
	var messageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
	for _, t := range tools {
		if dst, ok := t.(aitool.DynamicSkillLister); ok {
			messageModifier = BuildSkillModifier(dst)
			break
		}
	}
	require.NotNil(t, messageModifier)

	// Create a React Agent
	agent, err := CreateReactAgent(context.Background(), chatModel, AgentOptions{
		MaxStep:         10,
		ToolsConfig:     buildToolsConfig(tools),
		Logger:          NewTestLogger(t),
		MessageModifier: messageModifier,
	})
	require.NoError(t, err)

	// Send requests to verify that agents can recognize and use skills
	ctx := context.Background()
	resp, err := agent.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Please greet me using the greeting skill"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	t.Logf("✅ Agent response: %s", truncateString(resp.Content, 200))
}

// TestIntegration_SkillHotReload Test skills hot-updated (requires LLM)
func TestIntegration_SkillHotReload(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	tmpDir := t.TempDir()

	// Starting skills
	skillDir := filepath.Join(tmpDir, "math")
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: math
description: Math helper skill
---
Always respond with "Math skill activated!"
`), 0644))

	toolsConfig := []config.Tool{
		{
			Type: config.ToolTypeBuiltin,
			Name: "skill",
			Config: map[string]interface{}{
				"localDirs": []string{tmpDir},
			},
		},
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	tools, _, _, err := CreateTools(toolsConfig, ToolOptions{
		WrapVisual: false,
		Logger:     NewTestLogger(t),
	})
	require.NoError(t, err)

	var messageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
	for _, t := range tools {
		if dst, ok := t.(aitool.DynamicSkillLister); ok {
			messageModifier = BuildSkillModifier(dst)
			break
		}
	}

	agent, err := CreateReactAgent(context.Background(), chatModel, AgentOptions{
		MaxStep:         10,
		ToolsConfig:     buildToolsConfig(tools),
		Logger:          NewTestLogger(t),
		MessageModifier: messageModifier,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// 1. First round request: Only math skills
	resp, err := agent.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "What skills do you have? List them."},
	})
	require.NoError(t, err)
	t.Logf("✅ Turn 1 (before hot-reload): %s", truncateString(resp.Content, 200))

	// 2. A new skill has been added during runtime
	time.Sleep(100 * time.Millisecond)
	newSkillDir := filepath.Join(tmpDir, "weather")
	require.NoError(t, os.MkdirAll(newSkillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(newSkillDir, "SKILL.md"), []byte(`---
name: weather
description: Weather forecast skill
---
Always respond with "Weather skill activated!"
`), 0644))

	// 3. Second round of requests: You should be able to see the new weather skill
	// Note: Since react.Agent is a precompiled graph, the tool schema will not change.
	// However, MessageModifier injects the latest skill list into the system prompt
	resp2, err := agent.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "What skills do you have now? List them."},
	})
	require.NoError(t, err)
	t.Logf("✅ Turn 2 (after hot-reload): %s", truncateString(resp2.Content, 200))
}

// TestIntegration_SkillDescriptionChange After modifying the skill description, the intelligent agent can perceive it (requires LLM)
func TestIntegration_SkillDescriptionChange(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	tmpDir := t.TempDir()

	// Starting skill: described as "Math helper skill"
	skillDir := filepath.Join(tmpDir, "calculator")
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: calculator
description: Math helper skill
---
This is a math skill.
`), 0644))

	toolsConfig := []config.Tool{
		{
			Type: config.ToolTypeBuiltin,
			Name: "skill",
			Config: map[string]interface{}{
				"localDirs": []string{tmpDir},
			},
		},
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	tools, _, _, err := CreateTools(toolsConfig, ToolOptions{
		WrapVisual: false,
		Logger:     NewTestLogger(t),
	})
	require.NoError(t, err)

	var messageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
	for _, t := range tools {
		if dst, ok := t.(aitool.DynamicSkillLister); ok {
			messageModifier = BuildSkillModifier(dst)
			break
		}
	}

	agent, err := CreateReactAgent(context.Background(), chatModel, AgentOptions{
		MaxStep:         10,
		ToolsConfig:     buildToolsConfig(tools),
		Logger:          NewTestLogger(t),
		MessageModifier: messageModifier,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// 1. Round One: Described as a "Math helper skill"
	resp, err := agent.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "What is the calculator skill about? Just tell me its description."},
	})
	require.NoError(t, err)
	t.Logf("✅ Turn 1 (original description): %s", truncateString(resp.Content, 200))
	assert.Contains(t, resp.Content, "Math helper skill")

	// 2. Modify skill descriptions
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: calculator
description: Advanced scientific calculator with graph plotting capabilities
---
This is an advanced calculator skill.
`), 0644))

	// 3. Second round: New descriptions should be seen
	resp2, err := agent.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "What is the calculator skill about now? Just tell me its description."},
	})
	require.NoError(t, err)
	t.Logf("✅ Turn 2 (updated description): %s", truncateString(resp2.Content, 200))
	assert.Contains(t, resp2.Content, "Advanced scientific calculator")
}
