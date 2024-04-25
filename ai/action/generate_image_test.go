package action

import (
	"fmt"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/rulego/rulego/test/assert"
	"github.com/sashabaranov/go-openai"
	"testing"
)

var imageDirective = `
{
	"title": "未来世界",
	"prompt": "根据标题生成图片"
}
`

func TestGenerateImageNodeOnMsg(t *testing.T) {
	var node GenerateImageNode
	var configuration = make(types.Configuration)
	configuration["key"] = getEnvOrDefault("OPEN_AI_KEY", "")
	configuration["url"] = getEnvOrDefault("OPEN_AI_BASE_URL", "https://api.openai.com/v1/")
	configuration["model"] = getEnvOrDefault("OPEN_AI_MODEL", openai.CreateImageModelDallE3)
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
	msg := ctx.NewMsg("AI_MESSAGE", metaData, imageDirective)
	node.OnMsg(ctx, msg)
}
