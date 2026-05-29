package action

import (
	"fmt"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/rulego/rulego/test/assert"
	"os"
	"strings"
	"testing"
	"time"
)

func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getVisionModelOrDefault(defaultModel string) string {
	if m := os.Getenv("LLM_VISION_MODEL"); m != "" {
		return m
	}
	return getEnvOrDefault("LLM_MODEL", defaultModel)
}

func TestGenerateTextNodeOnMsg(t *testing.T) {
	if os.Getenv("LLM_API_KEY") == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}
	var node TextGenerateNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("LLM_API_KEY", "")
	configuration["url"] = getEnvOrDefault("LLM_BASE_URL", "https://ai.gitee.com/v1")
	configuration["model"] = getEnvOrDefault("LLM_MODEL", "DeepSeek-R1-Distill-Qwen-32B")
	configuration["systemPrompt"] = "你是一个订票助手，解析订票数量，输出格式：call_buy,名称,数量"
	configuration["messages"] = []ChatMessage{
		{
			Content: "帮我订5张《哪吒2》电影票",
			Role:    "user",
		},
	}
	config := types.NewConfig()
	err := node.Init(config, configuration)
	if err != nil {
		t.Errorf("err=%s", err)
	}
	starTime := time.Now()
	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") {
				t.Skipf("API rate limited: %v", err)
			}
			t.Logf("OnMsg error: %v", err)
		}
		assert.Equal(t, types.Success, relationType)
		fmt.Println("用时:" + time.Since(starTime).String())
		fmt.Println(msg.Data)
	})
	metaData := types.NewMetadata()
	msg := ctx.NewMsg("AI_MESSAGE", metaData, "")
	node.OnMsg(ctx, msg)

}

func TestGenerateTextNodeOnMsg2(t *testing.T) {
	if os.Getenv("LLM_API_KEY") == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}
	var node TextGenerateNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("LLM_API_KEY", "")
	configuration["url"] = getEnvOrDefault("LLM_BASE_URL", "https://ai.gitee.com/v1")
	configuration["model"] = getEnvOrDefault("LLM_MODEL", "DeepSeek-R1-Distill-Qwen-32B")
	configuration["systemPrompt"] = "你是一个订票助手，解析订票数量，输出格式：token,call_buy,名称,数量"
	configuration["messages"] = []ChatMessage{
		{
			Content: "我的token是:aabbcc123467",
			Role:    "user",
		},
		{
			Content: "${msg.userMsg}",
			Role:    "user",
		},
	}
	config := types.NewConfig()
	err := node.Init(config, configuration)
	if err != nil {
		t.Errorf("err=%s", err)
	}
	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") {
				t.Skipf("API rate limited: %v", err)
			}
			t.Logf("OnMsg error: %v", err)
		}
		assert.Equal(t, types.Success, relationType)
		fmt.Print(msg.Data)
	})
	metaData := types.NewMetadata()
	msg := ctx.NewMsg("AI_MESSAGE", metaData, `{"userMsg":"帮我订 3张《哪吒2》电影票"}`)
	node.OnMsg(ctx, msg)
}

func TestGenerateTextNodeOnMsg3(t *testing.T) {
	if os.Getenv("LLM_API_KEY") == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}
	var node TextGenerateNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("LLM_API_KEY", "")
	configuration["url"] = getEnvOrDefault("LLM_BASE_URL", "https://ai.gitee.com/v1")
	configuration["model"] = getEnvOrDefault("LLM_MODEL", "DeepSeek-R1-Distill-Qwen-32B")
	configuration["systemPrompt"] = "你是AI助手"
	configuration["params"] = Params{
		Temperature: 0.9,
		KeepThink:   true,
	}
	configuration["messages"] = []ChatMessage{
		{
			Content: "${msg.userMsg}",
			Role:    "user",
		},
	}
	config := types.NewConfig()
	err := node.Init(config, configuration)
	if err != nil {
		t.Errorf("err=%s", err)
	}
	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") {
				t.Skipf("API rate limited: %v", err)
			}
			t.Logf("OnMsg error: %v", err)
		}
		assert.Equal(t, types.Success, relationType)
		fmt.Print(msg.Data)
	})
	metaData := types.NewMetadata()
	msg := ctx.NewMsg("AI_MESSAGE", metaData, `{"userMsg":"介绍一下AI发展史"}`)
	node.OnMsg(ctx, msg)
}

func TestGenerateTextNodeOnMsg4(t *testing.T) {
	if os.Getenv("LLM_API_KEY") == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}
	var node TextGenerateNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("LLM_API_KEY", "")
	configuration["url"] = getEnvOrDefault("LLM_BASE_URL", "https://ai.gitee.com/v1")
	configuration["model"] = getEnvOrDefault("LLM_MODEL", "DeepSeek-R1-Distill-Qwen-32B")
	configuration["systemPrompt"] = "你是聪明的助手，以 json 格式输出数据，字段保存：name,num,token"
	configuration["messages"] = []ChatMessage{
		{
			Content: "我的token是:aabbcc123467",
			Role:    "user",
		},
		{
			Content: "${msg.userMsg}",
			Role:    "user",
		},
	}
	configuration["params"] = Params{
		ResponseFormat: "json_schema",
		JsonSchema: `{
				"type": "object",
				"properties": {
					"name": {
						"type": "string"
					},
					"num": {
						"type": "integer"
					},
					"token": {
						"type": "string"
					}
				},
				"required": ["name", "num", "token"]
			}`,
	}
	config := types.NewConfig()
	err := node.Init(config, configuration)
	if err != nil {
		t.Errorf("err=%s", err)
	}
	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") {
				t.Skipf("API rate limited: %v", err)
			}
			t.Logf("OnMsg error: %v", err)
		}
		assert.Equal(t, types.Success, relationType)
		fmt.Print(msg.Data)
	})
	metaData := types.NewMetadata()
	msg := ctx.NewMsg("AI_MESSAGE", metaData, `{"userMsg":"帮我订 3张《哪吒2》电影票"}`)
	node.OnMsg(ctx, msg)
}

func TestGenerateTextNodeOnMsgMultiContent(t *testing.T) {
	if os.Getenv("LLM_API_KEY") == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}
	var node TextGenerateNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("LLM_API_KEY", "")
	configuration["url"] = getEnvOrDefault("LLM_BASE_URL", "https://ai.gitee.com/v1")
	configuration["model"] = getVisionModelOrDefault("Qwen2-VL-72B")
	configuration["systemPrompt"] = ""
	configuration["messages"] = []ChatMessage{
		{
			Content: "我的token是:aabbcc123467",
			Role:    "user",
		},
		{
			Content: "${msg.userMsg}",
			Role:    "user",
		},
	}
	configuration["images"] = []string{
		"https://rulego.cc/img/architecture_zh.png",
	}
	config := types.NewConfig()
	err := node.Init(config, configuration)
	if err != nil {
		t.Errorf("err=%s", err)
	}
	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") {
				t.Skipf("API rate limited: %v", err)
			}
			t.Logf("OnMsg error: %v", err)
		}
		assert.Equal(t, types.Success, relationType)
		fmt.Print(msg.Data)
	})
	metaData := types.NewMetadata()
	msg := ctx.NewMsg("AI_MESSAGE", metaData, `{"userMsg":"解析图片"}`)
	node.OnMsg(ctx, msg)
}
