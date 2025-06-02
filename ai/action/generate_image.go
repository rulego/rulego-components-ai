package action

import (
	"fmt"
	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego/utils/maps"
	"github.com/rulego/rulego/utils/str"
	"github.com/sashabaranov/go-openai"
	"strings"
)

func init() {
	_ = rulego.Registry.Register(&GenerateImageNode{})
}

// GenerateImageNodeConfiguration 包含节点的配置信息
type GenerateImageNodeConfiguration struct {
	Url            string `json:"url"`             // OpenAI API 的基础 URL
	Key            string `json:"key"`             // OpenAI API 的密钥
	Model          string `json:"model"`           // 使用的模型名称
	Prompt         string `json:"prompt"`          // 图像生成的提示
	N              int    `json:"n"`               // 生成图像的数量
	ResponseFormat string `json:"response_format"` // 响应格式 取值：url/b64_json
	Quality        string `json:"quality"`         // 图像质量 取值：hd/standard
	Size           string `json:"size"`            // 图像尺寸 取值：256x256/512x512/1024x1024/1792x1024/1024x1792
	Style          string `json:"style"`           // 图像风格 取值：vivid/natural
}

// GenerateImageNode 是用于生成图像的节点
type GenerateImageNode struct {
	Config         GenerateImageNodeConfiguration
	AiClient       *openai.Client
	promptTemplate str.Template
}

// Type 返回节点类型
func (x *GenerateImageNode) Type() string {
	return "ai/createImage"
}

// New 创建一个新的 GenerateImageNode 实例
func (x *GenerateImageNode) New() types.Node {
	return &GenerateImageNode{
		Config: GenerateImageNodeConfiguration{
			Url:            "https://ai.gitee.com/v1",
			Key:            "",
			Model:          openai.CreateImageModelDallE3,
			Prompt:         "A futuristic cityscape at sunset",
			N:              1,
			ResponseFormat: "url",
			Quality:        "standard",
			Size:           "1024x1024",
			Style:          "vivid",
		},
	}
}

// Init 初始化节点
func (x *GenerateImageNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	err := maps.Map2Struct(configuration, &x.Config)
	if err != nil {
		return err
	}
	x.Config.Url = strings.TrimSpace(x.Config.Url)
	if x.Config.Url == "" {
		return fmt.Errorf("URL is missing")
	}
	x.Config.Prompt = strings.TrimSpace(x.Config.Prompt)
	if x.Config.Url == "" {
		return fmt.Errorf("prompt is missing")
	}
	x.promptTemplate = str.NewTemplate(x.Config.Prompt)

	if x.Config.N == 0 {
		x.Config.N = 1
	}

	x.Config.ResponseFormat = strings.TrimSpace(x.Config.ResponseFormat)
	if x.Config.ResponseFormat == "" {
		x.Config.ResponseFormat = openai.CreateImageResponseFormatURL
	}
	x.Config.Quality = strings.TrimSpace(x.Config.Quality)
	if x.Config.Quality == "" {
		x.Config.Quality = openai.CreateImageQualityStandard
	}
	x.Config.Size = strings.TrimSpace(x.Config.Size)
	if x.Config.Size == "" {
		x.Config.Size = openai.CreateImageSize1024x1024
	}
	x.Config.Style = strings.TrimSpace(x.Config.Style)
	if x.Config.Style == "" {
		x.Config.Style = openai.CreateImageStyleVivid
	}

	x.Config.Key = strings.TrimSpace(x.Config.Key)
	c := openai.DefaultConfig(x.Config.Key)
	c.BaseURL = x.Config.Url
	client := openai.NewClientWithConfig(c)
	x.AiClient = client

	return nil
}

// generateImage 生成图像的 URL
func (x *GenerateImageNode) generateImage(ctx types.RuleContext, prompt string) ([]string, error) {
	resp, err := x.AiClient.CreateImage(
		ctx.GetContext(),
		openai.ImageRequest{
			Model:          x.Config.Model,
			Prompt:         prompt,
			N:              x.Config.N,
			ResponseFormat: x.Config.ResponseFormat,
			Quality:        x.Config.Quality,
			Size:           x.Config.Size,
			Style:          x.Config.Style,
		},
	)
	if err != nil {
		return nil, err
	}

	var responses []string
	if x.Config.ResponseFormat == openai.CreateImageResponseFormatB64JSON {
		for _, item := range resp.Data {
			responses = append(responses, item.B64JSON)
		}
	} else {
		for _, item := range resp.Data {
			responses = append(responses, item.URL)
		}
	}

	return responses, nil
}

// OnMsg 处理消息
func (x *GenerateImageNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	prompt := x.Config.Prompt
	if !x.promptTemplate.IsNotVar() {
		prompt = x.promptTemplate.Execute(base.NodeUtils.GetEvnAndMetadata(ctx, msg))
	}
	responses, err := x.generateImage(ctx, prompt)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}

	msg.SetData(str.ToString(responses))
	ctx.TellSuccess(msg)
}

// Destroy 销毁节点
func (x *GenerateImageNode) Destroy() {
}
