package action

import (
	"fmt"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/rulego/rulego/test/assert"
	"os"
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

func TestGenerateTextNodeOnMsg(t *testing.T) {
	var node TextGenerateNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("OPEN_AI_KEY", "")
	configuration["url"] = getEnvOrDefault("OPEN_AI_BASE_URL", "https://ai.gitee.com/v1")
	configuration["model"] = getEnvOrDefault("OPEN_AI_MODEL", "DeepSeek-R1-Distill-Qwen-32B")
	configuration["systemPrompt"] = getEnvOrDefault("OPEN_AI_SYSTEM_PROMPT", "你是一个订票助手，解析订票数量，输出格式：call_buy,名称,数量")
	configuration["messages"] = []ChatMessage{
		{
			Content: getEnvOrDefault("OPEN_AI_SYSTEM_PROMPT", "帮我订5张《哪吒2》电影票"),
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
		assert.Equal(t, types.Success, relationType)
		fmt.Println("用时:" + time.Since(starTime).String())
		fmt.Println(msg.Data)
	})
	metaData := types.NewMetadata()
	msg := ctx.NewMsg("AI_MESSAGE", metaData, "")
	node.OnMsg(ctx, msg)

}

func TestGenerateTextNodeOnMsg2(t *testing.T) {
	var node TextGenerateNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("OPEN_AI_KEY", "")
	configuration["url"] = getEnvOrDefault("OPEN_AI_BASE_URL", "https://ai.gitee.com/v1")
	configuration["model"] = getEnvOrDefault("OPEN_AI_MODEL", "DeepSeek-R1-Distill-Qwen-32B")
	configuration["systemPrompt"] = getEnvOrDefault("OPEN_AI_SYSTEM_PROMPT", "你是一个订票助手，解析订票数量，输出格式：token,call_buy,名称,数量")
	configuration["userMessage"] = getEnvOrDefault("OPEN_AI_USER_MESSAGE", "${userMsg}")
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
		assert.Equal(t, types.Success, relationType)
		fmt.Print(msg.Data)
	})
	metaData := types.NewMetadata()
	msg := ctx.NewMsg("AI_MESSAGE", metaData, `{"userMsg":"帮我订 3张《哪吒2》电影票"}`)
	node.OnMsg(ctx, msg)
}
