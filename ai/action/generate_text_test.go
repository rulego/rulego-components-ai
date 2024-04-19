package action

import (
	"fmt"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/rulego/rulego/test/assert"
	"github.com/sashabaranov/go-openai"
	"os"
	"testing"
)

func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

var directive = `
{
	"title": "简述人工智能的发展史",
	"prompt": "用简练的语言回答"
}
`

func TestGenerateTextNodeOnMsg(t *testing.T) {
	var node TextGenerateNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("OPEN_AI_KEY", "")
	configuration["url"] = getEnvOrDefault("OPEN_AI_BASE_URL", "https://api.openai.com/v1/")
	configuration["model"] = getEnvOrDefault("OPEN_AI_MODEL", openai.GPT3Dot5Turbo)
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
	msg := ctx.NewMsg("AI_MESSAGE", metaData, directive)
	node.OnMsg(ctx, msg)
}
