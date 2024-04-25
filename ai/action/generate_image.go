package action

import (
	"errors"
	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/utils/json"
	"github.com/rulego/rulego/utils/maps"
	"github.com/sashabaranov/go-openai"
)

func init() {
	_ = rulego.Registry.Register(&GenerateImageNode{})
}

type GenerateImageNodeConfiguration struct {
	Url   string
	Key   string
	Model string
}

type GenerateImageNode struct {
	Config   GenerateImageNodeConfiguration
	AiClient *openai.Client
}

func (x *GenerateImageNode) Type() string {
	return "ai/generate-image"
}

func (x *GenerateImageNode) New() types.Node {
	return &GenerateImageNode{
		Config: GenerateImageNodeConfiguration{
			Url:   "https://api.openai.com/v1/",
			Key:   "",
			Model: openai.CreateImageModelDallE3,
		},
	}
}

func (x *GenerateImageNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	err := maps.Map2Struct(configuration, &x.Config)
	c := openai.DefaultConfig(x.Config.Key)
	c.BaseURL = x.Config.Url
	client := openai.NewClientWithConfig(c)
	x.AiClient = client
	return err
}

type GenerateImageDirective struct {
	Prompt         string `json:"prompt"`
	Title          string `json:"title"`
	N              int    `json:"n"`
	ResponseFormat string `json:"response_format"`
	Quality        string `json:"quality"`
	Size           string `json:"size"`
	Style          string `json:"style"`
}

func (b *GenerateImageDirective) UnmarshalJSON(data []byte) error {
	type BehaviorDataAlia GenerateImageDirective
	defaultData := &BehaviorDataAlia{
		N:              1,
		ResponseFormat: "url",
		Quality:        "standard",
		Size:           "1024x1792",
		Style:          "vivid",
	}
	_ = json.Unmarshal(data, defaultData)
	*b = GenerateImageDirective(*defaultData)
	return nil
}

type GenerateImageDynamicData struct {
	Urls   []string `json:"urls"`
	Title  string   `json:"title"`
	Prompt string   `json:"prompt"`
}

func resolveImageDirective(msg types.RuleMsg) (*GenerateImageDirective, error) {
	var directive GenerateImageDirective

	if msg.DataType == types.JSON {
		if err := json.Unmarshal([]byte(msg.Data), &directive); err != nil {
			return nil, err
		}
		return &directive, nil
	}
	return nil, errors.New("data type is not JSON")
}

func (x *GenerateImageNode) generateImageURLs(ctx types.RuleContext, directive *GenerateImageDirective) ([]string, error) {
	resp, err := x.AiClient.CreateImage(
		ctx.GetContext(),
		openai.ImageRequest{
			Model:          x.Config.Model,
			Prompt:         directive.Prompt,
			N:              directive.N,
			ResponseFormat: directive.ResponseFormat,
			Quality:        directive.Quality,
			Size:           directive.Size,
			Style:          directive.Style,
		},
	)
	var urls []string
	for _, item := range resp.Data {
		urls = append(urls, item.URL)
	}

	return urls, err
}

func (x *GenerateImageNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	directive, err := resolveImageDirective(msg)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}
	urls, err := x.generateImageURLs(ctx, directive)
	if err != nil {
		ctx.TellFailure(msg, err)
	} else {
		data, _ := json.Marshal(GenerateImageDynamicData{
			Urls:   urls,
			Title:  directive.Title,
			Prompt: directive.Prompt,
		})
		msg.Data = string(data)
		ctx.TellSuccess(msg)
	}
}

func (x *GenerateImageNode) Destroy() {
	//	pass
}
