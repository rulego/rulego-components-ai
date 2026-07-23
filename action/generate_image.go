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

// GenerateImageNodeConfiguration contains the node's configuration information
type GenerateImageNodeConfiguration struct {
	Url            string `json:"url" label:"API URL" desc:"Image generation API base URL. Supports OpenAI-compatible endpoints" required:"true"` // The basic URL of the OpenAI API
	Key            string `json:"key" label:"API Key" desc:"API key for authentication" required:"true"`                                          // The key to the OpenAI API
	Model          string `json:"model" label:"Model" desc:"Model name, e.g. dall-e-3" required:"true"`                                           // The model name used
	Prompt         string `json:"prompt" label:"Prompt" desc:"Image generation prompt. Supports ${} placeholder variables" required:"true"`       // Image generation prompts
	N              int    `json:"n" label:"Number of Images" desc:"Number of images to generate. Default is 1"`                                   // The number of generated images
	ResponseFormat string `json:"response_format" label:"Response Format" desc:"Response format: url or b64_json. Default is url"`                // Response format
	Quality        string `json:"quality" label:"Quality" desc:"Image quality: hd or standard. Default is standard"`                              // Image quality
	Size           string `json:"size" label:"Size" desc:"Image size, e.g. 256x256, 512x512, 1024x1024, 1792x1024, 1024x1792"`                    // Image size
	Style          string `json:"style" label:"Style" desc:"Image style: vivid or natural. Default is vivid"`                                     // Image style
}

// GenerateImageNode is a node used to generate images
type GenerateImageNode struct {
	Config         GenerateImageNodeConfiguration
	AiClient       *openai.Client
	promptTemplate str.Template
}

// Type returns the node type
func (x *GenerateImageNode) Type() string {
	return "ai/createImage"
}

// New creates a new GenerateImageNode instance
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

// Init initializes the node
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

// generateImage generates the image URL
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

// OnMsg processes a message
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

// Destroy the node
func (x *GenerateImageNode) Destroy() {
}

// Desc returns the component description
func (x *GenerateImageNode) Desc() string {
	return "Generate images using an AI model via image generation API. Supports configurable prompt, size, quality, and style. Routes image URLs or base64 data to Success/Failure chain."
}
