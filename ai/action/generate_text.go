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
	_ = rulego.Registry.Register(&TextGenerateNode{})
}

type NodeConfiguration struct {
	Url          string
	Key          string
	Model        string
	SystemPrompt string
	Messages     []ChatMessage
}

type ChatMessage struct {
	Role    string
	Content string
}

type ChatMessageTemplate struct {
	Role            string
	ContentTemplate str.Template
}

type TextGenerateNode struct {
	Config               NodeConfiguration
	Client               *openai.Client
	systemPromptTemplate str.Template
	chatMessageTemplates []ChatMessageTemplate
	hasVar               bool
}

func (x *TextGenerateNode) Type() string {
	return "ai/chat"
}

func (x *TextGenerateNode) New() types.Node {
	return &TextGenerateNode{
		Config: NodeConfiguration{
			Url:   "https://ai.gitee.com/v1",
			Key:   "",
			Model: openai.O1Mini,
		},
	}
}

func (x *TextGenerateNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	err := maps.Map2Struct(configuration, &x.Config)
	if err != nil {
		return err
	}
	x.Config.Url = strings.TrimSpace(x.Config.Url)
	if x.Config.Url == "" {
		return fmt.Errorf("URL is missing")
	}

	x.Config.Key = strings.TrimSpace(x.Config.Key)
	c := openai.DefaultConfig(x.Config.Key)
	c.BaseURL = x.Config.Url
	client := openai.NewClientWithConfig(c)
	x.Client = client
	x.systemPromptTemplate = str.NewTemplate(x.Config.SystemPrompt)
	if !x.systemPromptTemplate.IsNotVar() {
		x.hasVar = true
	}
	for _, item := range x.Config.Messages {
		tmpl := str.NewTemplate(item.Content)
		if !tmpl.IsNotVar() {
			x.hasVar = true
		}
		item.Role = strings.TrimSpace(item.Role)
		if item.Role == "" {
			item.Role = openai.ChatMessageRoleUser
		}
		x.chatMessageTemplates = append(x.chatMessageTemplates, ChatMessageTemplate{
			Role:            item.Role,
			ContentTemplate: str.NewTemplate(item.Content),
		})
	}
	return err
}

func (x *TextGenerateNode) sendCompletionMessage(ctx types.RuleContext, evn map[string]interface{}, systemPrompt string, messagesTemplates []ChatMessageTemplate) (string, error) {
	var messages []openai.ChatCompletionMessage
	if systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}
	for _, item := range messagesTemplates {
		content := item.ContentTemplate.Execute(evn)
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    item.Role,
			Content: content,
		})
	}
	resp, err := x.Client.CreateChatCompletion(
		ctx.GetContext(),
		openai.ChatCompletionRequest{
			Model:    x.Config.Model,
			Messages: messages,
		},
	)
	if err != nil {
		return "", err
	}
	var combinedContent string
	for _, choice := range resp.Choices {
		combinedContent += choice.Message.Content
	}

	return combinedContent, err
}

func (x *TextGenerateNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	systemPrompt := x.Config.SystemPrompt
	var evn map[string]interface{}
	if x.hasVar {
		evn = base.NodeUtils.GetEvnAndMetadata(ctx, msg)
	}
	systemPrompt = x.systemPromptTemplate.Execute(evn)

	//发送消息，并获取回复
	content, err := x.sendCompletionMessage(ctx, evn, systemPrompt, x.chatMessageTemplates)
	if err != nil {
		ctx.TellFailure(msg, err)
	} else {
		msg.Data = content
		ctx.TellSuccess(msg)
	}
}

func (x *TextGenerateNode) Destroy() {
}
