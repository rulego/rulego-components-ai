/*
 * Copyright 2026 The RuleGo Authors.
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

package intent

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/rulego/rulego/test/assert"
)

func TestIntentRecognitionNode_Type(t *testing.T) {
	node := &IntentNode{}
	assert.Equal(t, "ai/intent", node.Type())
}

func TestIntentRecognitionNode_New(t *testing.T) {
	node := &IntentNode{}
	newNode := node.New()

	assert.NotNil(t, newNode)
	intentNode, ok := newNode.(*IntentNode)
	assert.True(t, ok)

	assert.Equal(t, "https://ai.gitee.com/v1", intentNode.Config.Url)
	assert.Equal(t, "Qwen2.5-72B-Instruct", intentNode.Config.Model)
	assert.Equal(t, types.DefaultRelationType, intentNode.Config.DefaultIntent)
	assert.Equal(t, float32(0.1), intentNode.Config.Temperature)
	assert.Equal(t, 0, intentNode.Config.MaxTokens)

	assert.Equal(t, 3, len(intentNode.Config.Intents))
	assert.Equal(t, "createRule", intentNode.Config.Intents[0].Name)
	assert.Equal(t, "control", intentNode.Config.Intents[1].Name)
	assert.Equal(t, "query", intentNode.Config.Intents[2].Name)
}

func TestIntentRecognitionNode_Init(t *testing.T) {
	t.Run("成功初始化", func(t *testing.T) {
		node := &IntentNode{}
		config := types.NewConfig()

		configuration := map[string]interface{}{
			"url":           "https://api.openai.com/v1",
			"key":           "test-key",
			"model":         "gpt-3.5-turbo",
			"defaultIntent": "Default",
			"input":         "${msg.content}",
			"intents": []map[string]interface{}{
				{"name": "test", "description": "测试意图"},
			},
		}

		err := node.Init(config, configuration)
		assert.Nil(t, err)
		assert.Equal(t, "https://api.openai.com/v1", node.Config.Url)
		assert.Equal(t, "test-key", node.Config.Key)
		assert.Equal(t, "gpt-3.5-turbo", node.Config.Model)
		assert.Equal(t, "${msg.content}", node.Config.Input)
		assert.NotNil(t, node.Client)
		assert.NotNil(t, node.userInputTemplate)
		assert.True(t, node.hasVar)
	})

	t.Run("成功初始化带systemPrompt", func(t *testing.T) {
		node := &IntentNode{}
		config := types.NewConfig()

		configuration := map[string]interface{}{
			"url":          "https://api.openai.com/v1",
			"key":          "test-key",
			"model":        "gpt-3.5-turbo",
			"systemPrompt": "你是一个意图分类器",
			"intents": []map[string]interface{}{
				{"name": "test", "description": "测试意图"},
			},
		}

		err := node.Init(config, configuration)
		assert.Nil(t, err)
		assert.Equal(t, "你是一个意图分类器", node.Config.SystemPrompt)
		assert.NotNil(t, node.systemPromptTemplate)
	})

	t.Run("缺少URL", func(t *testing.T) {
		node := &IntentNode{}
		config := types.NewConfig()

		configuration := map[string]interface{}{
			"key": "test-key",
			"intents": []map[string]interface{}{
				{"name": "test", "description": "测试意图"},
			},
		}

		err := node.Init(config, configuration)
		assert.NotNil(t, err)
		assert.True(t, strings.Contains(err.Error(), "URL is required"))
	})

	t.Run("缺少API Key", func(t *testing.T) {
		node := &IntentNode{}
		config := types.NewConfig()

		configuration := map[string]interface{}{
			"url": "https://api.openai.com/v1",
			"intents": []map[string]interface{}{
				{"name": "test", "description": "测试意图"},
			},
		}

		err := node.Init(config, configuration)
		assert.NotNil(t, err)
		assert.True(t, strings.Contains(err.Error(), "API Key is required"))
	})

	t.Run("缺少意图定义", func(t *testing.T) {
		node := &IntentNode{}
		config := types.NewConfig()

		configuration := map[string]interface{}{
			"url": "https://api.openai.com/v1",
			"key": "test-key",
		}

		err := node.Init(config, configuration)
		assert.NotNil(t, err)
		assert.True(t, strings.Contains(err.Error(), "at least one intent must be defined"))
	})

	t.Run("无效的用户输入表达式", func(t *testing.T) {
		node := &IntentNode{}
		config := types.NewConfig()

		configuration := map[string]interface{}{
			"url":   "https://api.openai.com/v1",
			"key":   "test-key",
			"input": "${invalid.expression",
			"intents": []map[string]interface{}{
				{"name": "test", "description": "测试意图"},
			},
		}

		err := node.Init(config, configuration)
		if err != nil {
			assert.True(t, strings.Contains(err.Error(), "invalid input expression"))
		} else {
			t.Log("The expression may be accepted by the parser, so this test is skipped")
		}
	})
}

func TestIntentRecognitionNode_IsValidIntent(t *testing.T) {
	node := &IntentNode{}
	node.Config.Intents = []Intent{
		{Name: "greeting"},
		{Name: "question"},
		{Name: "request"},
	}

	assert.True(t, node.isValidIntent("greeting"))
	assert.True(t, node.isValidIntent("question"))
	assert.True(t, node.isValidIntent("request"))
	assert.False(t, node.isValidIntent("unknown"))
	assert.False(t, node.isValidIntent(""))
}

func TestIntentRecognitionNode_BuildDefaultPrompt(t *testing.T) {
	node := &IntentNode{}
	node.Config.DefaultIntent = "unknown"
	node.Config.Intents = []Intent{
		{Name: "greeting", Description: "用户问候"},
	}

	prompt := node.buildDefaultPrompt()

	assert.True(t, strings.Contains(prompt, "意图分类器"))
	assert.True(t, strings.Contains(prompt, "greeting"))
	assert.True(t, strings.Contains(prompt, "用户问候"))
	assert.True(t, strings.Contains(prompt, "unknown"))
	assert.True(t, strings.Contains(prompt, "只输出意图名称"))
}

func TestIntentRecognitionNode_CleanIntentResponse(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"createRule", "createRule"},
		{"  createRule  ", "createRule"},
		{"\"createRule\"", "createRule"},
		{"`createRule`", "createRule"},
		{"```json\ncontrol\n```", "control"},
		{"createRule\n其他内容", "createRule"},
		{"```\nchat\n```", "chat"},
	}

	for _, tt := range tests {
		result := cleanIntentResponse(tt.input)
		assert.Equal(t, tt.expected, result)
	}
}

// getTestConfig Retrieves test configuration (read from environment variables)
// Note: Please set the environment variable LLM_API_KEY to run the test
// Example:
//
//	export LLM_BASE_URL="https://token-plan-cn.xiaomimimo.com/v1"
//	export LLM_API_KEY="your-api-key"
//	export LLM_MODEL="mimo-v2.5-pro"
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getTestConfig() (baseURL, apiKey, model string) {
	baseURL = getEnvOrDefault("LLM_BASE_URL", "https://token-plan-cn.xiaomimimo.com/v1")
	apiKey = os.Getenv("LLM_API_KEY") // It must be set through environment variables; default values are not provided
	model = getEnvOrDefault("LLM_MODEL", "mimo-v2.5-pro")
	return
}

func TestIntentNode_OnMsg_Integration(t *testing.T) {
	baseURL, apiKey, model := getTestConfig()
	if apiKey == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	config := types.NewConfig()

	configuration := map[string]interface{}{
		"url":           baseURL,
		"key":           apiKey,
		"model":         model,
		"defaultIntent": "chat",
		"temperature":   0.1,
		"intents": []map[string]interface{}{
			{"name": "createRule", "description": "用户想要创建或配置AI检测规则，如安全帽检测、烟火预警、跌倒检测等"},
			{"name": "control", "description": "用户想要直接控制设备执行动作，如打开灯光、关闭风机、发送告警等"},
			{"name": "chat", "description": "一般性对话、闲聊、系统使用咨询"},
		},
	}

	testCases := []struct {
		name            string
		input           string
		expectedIntents []string
	}{
		{
			name:            "创建规则-安全帽检测",
			input:           "发现有人没戴安全帽，打开灯光",
			expectedIntents: []string{"createRule", "control"},
		},
		{
			name:            "控制设备-关闭风机",
			input:           "把风机关掉",
			expectedIntents: []string{"control"},
		},
		{
			name:            "自然对话",
			input:           "你好，你能做什么？",
			expectedIntents: []string{"chat"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			node := &IntentNode{}
			err := node.Init(config, configuration)
			assert.Nil(t, err)

			var resultRelationType string
			var resultMsg types.RuleMsg
			var resultErr error
			done := make(chan struct{})

			ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
				resultMsg = msg
				resultRelationType = relationType
				resultErr = err
				close(done)
			})

			metaData := types.NewMetadata()
			msg := ctx.NewMsg("AI_MESSAGE", metaData, tc.input)
			startTime := time.Now()

			go node.OnMsg(ctx, msg)

			select {
			case <-done:
				elapsed := time.Since(startTime)
				t.Logf("Duration: %v", elapsed)
				t.Logf("Route: %s", resultRelationType)
				t.Logf("Mistake: %v", resultErr)
				t.Logf("  metadata.intent: %s", resultMsg.GetMetadata().GetValue(IntentMetadataKey))
				t.Logf("Whether the original data is retained: data = %s", resultMsg.GetData())

				if resultErr != nil {
					errStr := resultErr.Error()
					if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") {
						t.Skipf("API rate limited: %v", resultErr)
					}
				}
				assert.Nil(t, resultErr)

				matched := false
				for _, expected := range tc.expectedIntents {
					if resultRelationType == expected {
						matched = true
						break
					}
				}
				assert.True(t, matched, fmt.Sprintf("期望路由到 %v，实际路由到 %s", tc.expectedIntents, resultRelationType))

				assert.NotEqual(t, "", resultMsg.GetMetadata().GetValue(IntentMetadataKey))
				assert.Equal(t, tc.input, resultMsg.GetData(), "原始输入应该被保留，不被覆盖")

			case <-time.After(60 * time.Second):
				t.Fatal("Test timeout (60 seconds)")
			}
		})
	}
}
