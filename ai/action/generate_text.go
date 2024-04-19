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
	_ = rulego.Registry.Register(&TextGenerateNode{})
}

type NodeConfiguration struct {
	Url   string
	Key   string
	Model string
}

type TextGenerateNode struct {
	Config   NodeConfiguration
	AiClient *openai.Client
}

func (x *TextGenerateNode) Type() string {
	return "ai/generate-text"
}

func (x *TextGenerateNode) New() types.Node {
	return &TextGenerateNode{
		Config: NodeConfiguration{
			Url:   "https://api.openai.com/v1/",
			Key:   "",
			Model: openai.GPT3Dot5Turbo,
		},
	}
}

func (x *TextGenerateNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	err := maps.Map2Struct(configuration, &x.Config)
	c := openai.DefaultConfig(x.Config.Key)
	c.BaseURL = x.Config.Url
	client := openai.NewClientWithConfig(c)
	x.AiClient = client
	return err
}

type GenerateTextDirective struct {
	Prompt string `json:"prompt"`
	Title  string `json:"title"`
}

type GenerateTextDynamicData struct {
	Content string `json:"content"`
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
}

func resolveDirective(msg types.RuleMsg) (*GenerateTextDirective, error) {
	var directive GenerateTextDirective

	if msg.DataType == types.JSON {
		if err := json.Unmarshal([]byte(msg.Data), &directive); err != nil {
			return nil, err
		}
		return &directive, nil
	}
	return nil, errors.New("data type is not JSON")
}

func (x *TextGenerateNode) sendCompletionMessage(ctx types.RuleContext, directive *GenerateTextDirective) (string, error) {
	resp, err := x.AiClient.CreateChatCompletion(
		ctx.GetContext(),
		openai.ChatCompletionRequest{
			Model: x.Config.Model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: directive.Prompt,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: directive.Title,
				},
			},
		},
	)
	var combinedContent string
	for _, choice := range resp.Choices {
		combinedContent += choice.Message.Content
	}

	return combinedContent, err
}

func (x *TextGenerateNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	directive, err := resolveDirective(msg)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}
	content, err := x.sendCompletionMessage(ctx, directive)
	if err != nil {
		ctx.TellFailure(msg, err)
	} else {
		data, _ := json.Marshal(GenerateTextDynamicData{
			Content: content,
			Title:   directive.Title,
			Prompt:  directive.Prompt,
		})
		msg.Data = string(data)
		ctx.TellSuccess(msg)
	}
}

func (x *TextGenerateNode) Destroy() {
	//	pass
}
