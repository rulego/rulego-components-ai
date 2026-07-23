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

// Define a regular expression
// <think>[\s\S]*?</think>: Matches everything between <think> and </think>, including line breaks
var re = regexp.MustCompile(`<think>[\s\S]*?</think>`)

// NodeConfiguration component configuration
type NodeConfiguration struct {
	Url          string        `json:"url" label:"API URL" desc:"LLM API base URL. Supports OpenAI-compatible endpoints" required:"true"`                                       // Request address
	Key          string        `json:"key" label:"API Key" desc:"API key for authentication" required:"true"`                                                                   // API Key
	Model        string        `json:"model" label:"Model" desc:"Model name, e.g. gpt-4o, o1-mini" required:"true"`                                                             // Model name
	SystemPrompt string        `json:"systemPrompt" label:"System Prompt" desc:"System prompt to define model behavior and response style. Supports ${} placeholder variables"` // System notification
	Messages     []ChatMessage `json:"messages" label:"Messages" desc:"Chat message list for conversation context. Each item has role and content fields"`                      // Context/user message list
	Images       []string      `json:"images" label:"Images" desc:"Image URLs for multimodal input. Supports ${} placeholder variables"`                                        // Image input
	Params       Params        `json:"params" label:"Parameters" desc:"LLM generation parameters including temperature, maxTokens, etc."`                                       // Large model parameters
}

// Params large model parameters
type Params struct {
	Temperature      float32  `json:"temperature" label:"Temperature" desc:"Sampling temperature in [0.0, 2.0]. Higher values produce more random and creative output"` // Sampling temperature
	TopP             float32  `json:"topP" label:"Top P" desc:"Nucleus sampling threshold in [0.0, 1.0]. Selects tokens from top p% probability candidates"`            // Top P
	PresencePenalty  float32  `json:"presencePenalty" label:"Presence Penalty" desc:"Penalty for tokens already present in text. Range [0.0, 1.0]"`                     // There is punishment
	FrequencyPenalty float32  `json:"frequencyPenalty" label:"Frequency Penalty" desc:"Penalty based on token frequency in text. Range [0.0, 1.0]"`                     // Frequent penalties
	MaxTokens        int      `json:"maxTokens" label:"Max Tokens" desc:"Maximum number of tokens in the output"`                                                       // Maximum output length
	Stop             []string `json:"stop" label:"Stop Sequences" desc:"List of strings that cause the model to stop generating"`                                       // Stop marking
	ResponseFormat   string   `json:"responseFormat" label:"Response Format" desc:"Output format: text, json_object, or json_schema. Default is text"`                  // Output format
	JsonSchema       string   `json:"jsonSchema" label:"JSON Schema" desc:"JSON Schema for structured output when responseFormat is json_schema"`                       // JSON Schema
	KeepThink        bool     `json:"keepThink" label:"Keep Think" desc:"Keep thinking process in output. Only applies to text response format"`                        // Keep the thought process in place
}

// ChatMessage Context Messages / User Messages
type ChatMessage struct {
	Role    string `json:"role" label:"Role" desc:"Message role: user or assistant"`                           // Message role
	Content string `json:"content" label:"Content" desc:"Message content. Supports ${} placeholder variables"` // News content
}

// ChatMessageTemplate Contextual message/user message template
type ChatMessageTemplate struct {
	Role            string
	ContentTemplate str.Template
}

// TextGenerateNode provides the model with instructions, queries, or any text-based input, and receives a large model text response
type TextGenerateNode struct {
	Config               NodeConfiguration
	Client               *openai.Client
	systemPromptTemplate str.Template
	chatMessageTemplates []ChatMessageTemplate
	imagesTemplates      []str.Template
	hasVar               bool // Whether variable placeholders are included
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

	//Send messages and get replies
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

// Desc returns the component description
func (x *TextGenerateNode) Desc() string {
	return "Send prompts to an AI LLM model via chat completion API. Supports system/user messages, multimodal image input, and configurable generation parameters. Routes result to Success/Failure chain."
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
		//Is it the last user message?
		if index == (messageLen-1) && imageLen > 0 {
			var multiContent []openai.ChatMessagePart
			//Added image messages
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
			//Increase user messages
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
		// Eliminate the process of thinking
		combinedContent = strings.TrimLeft(re.ReplaceAllString(combinedContent, ""), "\n")
		// Remove the opening ```json and closing ``` fences
		combinedContent = strings.TrimPrefix(combinedContent, "```json\n")
		combinedContent = strings.TrimSuffix(combinedContent, "\n```")
	}

	return combinedContent, err
}
