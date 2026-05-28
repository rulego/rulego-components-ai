package action

import (
	"fmt"
	"os"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/rulego/rulego/test/assert"
	"testing"
)

func TestGenerateImageNodeOnMsg(t *testing.T) {
	// 图片生成测试需要专门的 LLM_IMAGE_* 环境变量
	// 因为不是所有 LLM API 都支持图片生成（如 Gitee AI, BigModel 等）
	apiKey := os.Getenv("LLM_IMAGE_API_KEY")
	baseURL := os.Getenv("LLM_IMAGE_BASE_URL")
	model := os.Getenv("LLM_IMAGE_MODEL")

	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("LLM_IMAGE_API_KEY, LLM_IMAGE_BASE_URL, or LLM_IMAGE_MODEL not set, skipping image generation test")
	}

	var node GenerateImageNode
	var configuration = make(types.Configuration)
	configuration["key"] = apiKey
	configuration["url"] = baseURL
	configuration["model"] = model
	configuration["prompt"] = "未来世界"
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
	msg := ctx.NewMsg("AI_MESSAGE", metaData, "")
	node.OnMsg(ctx, msg)
}
