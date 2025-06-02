package action

import (
	"encoding/json"
	"fmt"
	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego/utils/maps"
	"github.com/rulego/rulego/utils/str"
	"github.com/sashabaranov/go-openai"
	"regexp"
	"strings"
)

func init() {
	_ = rulego.Registry.Register(&TextGenerateNode{})
}

// 定义正则表达式
// <think>[\s\S]*?</think>：匹配 <think> 和 </think> 之间的所有内容，包括换行符
var re = regexp.MustCompile(`<think>[\s\S]*?</think>`)

// NodeConfiguration 组件配置
type NodeConfiguration struct {
	Url          string        `json:"url"`          // 请求地址
	Key          string        `json:"key"`          // API Key
	Model        string        `json:"model"`        // 模型名称
	SystemPrompt string        `json:"systemPrompt"` // 系统提示，用于预先定义模型的基础行为框架和响应风格。可以使用${} 占位符变量
	Messages     []ChatMessage `json:"messages"`     // 上下文/用户消息列表
	Images       []string      `json:"images"`       // 允许模型输入图片，并根据图像内容的理解回答用户问题
	Params       Params        `json:"params"`       //大模型参数
}

// Params 大模型参数
type Params struct {
	Temperature      float32  `json:"temperature"`      //采样温度控制输出的随机性。温度值在 [0.0, 2.0] 范围内，值越高，输出越随机和创造性；值越低，输出越稳定。
	TopP             float32  `json:"topP"`             // 采样方法的取值范围为 [0.0,1.0]。top_p 值确定模型从概率最高的前p%的候选词中选取 tokens；当 top_p 为 0 时，此参数无效。
	PresencePenalty  float32  `json:"presencePenalty"`  //存在惩罚 对文本中已有的标记的对数概率施加惩罚。取值范围[0.0,1.0]
	FrequencyPenalty float32  `json:"frequencyPenalty"` //频率惩罚 对文本中出现的标记的对数概率施加惩罚。取值范围[0.0,1.0]
	MaxTokens        int      `json:"maxTokens"`        // 最大输出长度
	Stop             []string `json:"stop"`             // 模型停止输出的标记
	ResponseFormat   string   `json:"responseFormat"`   // 输出结果的格式。支持：text、json_object、json_schema。默认为 text。
	JsonSchema       string   `json:"jsonSchema"`       // JSON Schema
	KeepThink        bool     `json:"keepThink"`        //是否保留思考过程，只对text响应格式生效
}

// ChatMessage 上下文消息/用户消息
type ChatMessage struct {
	Role    string `json:"role"`    // 消息角色 user/assistant
	Content string `json:"content"` // 消息内容。可以使用${} 占位符变量
}

// ChatMessageTemplate 上下文消息/用户消息模板
type ChatMessageTemplate struct {
	Role            string
	ContentTemplate str.Template
}

// TextGenerateNode 向模型提供指令、查询或任何基于文本的输入，并得到大模型文本响应
type TextGenerateNode struct {
	Config               NodeConfiguration
	Client               *openai.Client
	systemPromptTemplate str.Template
	chatMessageTemplates []ChatMessageTemplate
	imagesTemplates      []str.Template
	hasVar               bool // 是否包含变量占位符
	responseFormat       openai.ChatCompletionResponseFormatType
}

func (x *TextGenerateNode) Type() string {
	return "ai/llm"
}

func (x *TextGenerateNode) New() types.Node {
	return &TextGenerateNode{
		Config: NodeConfiguration{
			Url:   "https://ai.gitee.com/v1",
			Key:   "",
			Model: openai.O1Mini,
			Params: Params{
				Temperature: 0.6,
				TopP:        0.75,
			},
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
	for _, item := range x.Config.Images {
		tmpl := str.NewTemplate(item)
		if !tmpl.IsNotVar() {
			x.hasVar = true
		}
		x.imagesTemplates = append(x.imagesTemplates, str.NewTemplate(item))
	}

	x.Config.Params.ResponseFormat = strings.TrimSpace(x.Config.Params.ResponseFormat)
	if x.Config.Params.ResponseFormat == "" {
		x.Config.Params.ResponseFormat = string(openai.ChatCompletionResponseFormatTypeText)
	}
	x.responseFormat = openai.ChatCompletionResponseFormatType(x.Config.Params.ResponseFormat)
	if x.responseFormat != openai.ChatCompletionResponseFormatTypeText &&
		x.responseFormat != openai.ChatCompletionResponseFormatTypeJSONObject &&
		x.responseFormat != openai.ChatCompletionResponseFormatTypeJSONSchema {
		x.Config.Params.ResponseFormat = string(openai.ChatCompletionResponseFormatTypeText)
	}
	return err
}

func (x *TextGenerateNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	systemPrompt := x.Config.SystemPrompt
	var evn map[string]interface{}
	if x.hasVar {
		evn = base.NodeUtils.GetEvnAndMetadata(ctx, msg)
	}
	systemPrompt = x.systemPromptTemplate.Execute(evn)

	//发送消息，并获取回复
	content, err := x.sendCompletionMessage(ctx, evn, systemPrompt, x.chatMessageTemplates, x.imagesTemplates)
	if err != nil {
		ctx.TellFailure(msg, err)
	} else {
		if x.responseFormat == openai.ChatCompletionResponseFormatTypeJSONObject ||
			x.responseFormat == openai.ChatCompletionResponseFormatTypeJSONSchema {
			msg.DataType = types.JSON
		} else {
			msg.DataType = types.TEXT
		}
		msg.SetData(content)
		ctx.TellSuccess(msg)
	}
}

func (x *TextGenerateNode) Destroy() {
}

func (x *TextGenerateNode) sendCompletionMessage(ctx types.RuleContext, evn map[string]interface{}, systemPrompt string, messagesTemplates []ChatMessageTemplate, imagesTemplates []str.Template) (string, error) {
	var messages []openai.ChatCompletionMessage
	if systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}
	messageLen := len(messagesTemplates)
	imageLen := len(imagesTemplates)
	for index, item := range messagesTemplates {
		content := item.ContentTemplate.Execute(evn)
		//是否是最后一条用户消息
		if index == (messageLen-1) && imageLen > 0 {
			var multiContent []openai.ChatMessagePart
			//增加图片消息
			for _, imageItemTpl := range imagesTemplates {
				imageUrl := imageItemTpl.Execute(evn)
				multiContent = append(multiContent, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    imageUrl,
						Detail: openai.ImageURLDetailAuto,
					},
				})
			}
			//增加用户消息
			multiContent = append(multiContent, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeText,
				Text: content,
			})
			messages = append(messages, openai.ChatCompletionMessage{
				Role:         item.Role,
				MultiContent: multiContent,
			})
		} else {
			messages = append(messages, openai.ChatCompletionMessage{
				Role:    item.Role,
				Content: content,
			})
		}
	}
	var responseFormatJSONSchema *openai.ChatCompletionResponseFormatJSONSchema
	if x.Config.Params.JsonSchema != "" {
		var schemaRawMessage = json.RawMessage{}
		err := json.Unmarshal([]byte(x.Config.Params.JsonSchema), &schemaRawMessage)
		if err != nil {
			return "", err
		}
		responseFormatJSONSchema = &openai.ChatCompletionResponseFormatJSONSchema{
			Schema: schemaRawMessage,
		}
	}

	resp, err := x.Client.CreateChatCompletion(
		ctx.GetContext(),
		openai.ChatCompletionRequest{
			Model:            x.Config.Model,
			Messages:         messages,
			Temperature:      x.Config.Params.Temperature,
			TopP:             x.Config.Params.TopP,
			PresencePenalty:  x.Config.Params.PresencePenalty,
			FrequencyPenalty: x.Config.Params.FrequencyPenalty,
			MaxTokens:        x.Config.Params.MaxTokens,
			Stop:             x.Config.Params.Stop,
			ResponseFormat: &openai.ChatCompletionResponseFormat{
				Type:       openai.ChatCompletionResponseFormatType(x.Config.Params.ResponseFormat),
				JSONSchema: responseFormatJSONSchema,
			},
		},
	)
	if err != nil {
		return "", err
	}
	var combinedContent string
	for _, choice := range resp.Choices {
		combinedContent += choice.Message.Content
	}

	if x.responseFormat == openai.ChatCompletionResponseFormatTypeText {
		if !x.Config.Params.KeepThink {
			combinedContent = strings.TrimLeft(re.ReplaceAllString(combinedContent, ""), "\n")
		}
	} else {
		// 去掉思考过程
		combinedContent = strings.TrimLeft(re.ReplaceAllString(combinedContent, ""), "\n")
		// 去掉开头和结尾的 ```json 和 ```
		combinedContent = strings.TrimPrefix(combinedContent, "```json\n")
		combinedContent = strings.TrimSuffix(combinedContent, "\n```")
	}

	return combinedContent, err
}
