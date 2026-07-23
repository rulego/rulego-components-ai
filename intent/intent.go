/*
 * Copyright 2026 The RuleGo Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package intent

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego/utils/el"
	"github.com/rulego/rulego/utils/maps"
	"github.com/rulego/rulego/utils/str"
	"github.com/sashabaranov/go-openai"
)

func init() {
	_ = rulego.Registry.Register(&IntentNode{})
}

// Intent recognition results are keynames in metadata
const IntentMetadataKey = "intent"

// IntentConfiguration LLM-based intent recognition configuration
type IntentConfiguration struct {
	Url           string   `json:"url" label:"API URL" desc:"LLM API base URL, e.g. https://ai.gitee.com/v1. Supports ${include()} for file references" required:"true"`
	Key           string   `json:"key" label:"API Key" desc:"LLM API key for authentication" required:"true"`
	Model         string   `json:"model" label:"Model" desc:"LLM model name, e.g. Qwen2.5-72B-Instruct" required:"true"`
	Input         string   `json:"input" label:"Input Expression" desc:"User input expression. Supports ${msg.key} and ${metadata.key}. Empty uses msg.GetData()"`
	Intents       []Intent `json:"intents" label:"Intents" desc:"Predefined intent list for classification" required:"true"`
	DefaultIntent string   `json:"defaultIntent" label:"Default Intent" desc:"Fallback intent when no match found"`
	SystemPrompt  string   `json:"systemPrompt" label:"System Prompt" desc:"Custom system prompt. Supports ${include()} for file references. Empty uses built-in default"`
	Temperature   float32  `json:"temperature" label:"Temperature" desc:"Model temperature parameter, lower values for more deterministic output"`
	MaxTokens     int      `json:"maxTokens" label:"Max Tokens" desc:"Maximum output tokens. 0 uses model default"`
}

// Intent intent definition
type Intent struct {
	Name        string `json:"name" label:"Intent Name" desc:"Unique intent name used as route relation type" required:"true"`
	Description string `json:"description" label:"Description" desc:"Intent description for prompt generation" required:"true"`
}

// IntentNode Intent Identification Node
type IntentNode struct {
	Config               IntentConfiguration
	Client               *openai.Client
	systemPromptTemplate el.Template
	userInputTemplate    el.Template
	hasVar               bool
}

// Type returns the component type
func (x *IntentNode) Type() string {
	return "ai/intent"
}

// New: Create a new component instance
func (x *IntentNode) New() types.Node {
	return &IntentNode{
		Config: IntentConfiguration{
			Url:           "https://ai.gitee.com/v1",
			Key:           "",
			Model:         "Qwen2.5-72B-Instruct",
			DefaultIntent: types.DefaultRelationType,
			Temperature:   0.1,
			MaxTokens:     0,
			Intents: []Intent{
				{Name: "createRule", Description: "创建联动规则"},
				{Name: "control", Description: "控制设备"},
				{Name: "query", Description: "查询设备状态"},
			},
		},
	}
}

// Init initializes the component
func (x *IntentNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	err := maps.Map2Struct(configuration, &x.Config)
	if err != nil {
		return err
	}

	x.Config.Url = strings.TrimSpace(x.Config.Url)
	if x.Config.Url == "" {
		return fmt.Errorf("URL is required")
	}

	x.Config.Key = strings.TrimSpace(x.Config.Key)
	if x.Config.Key == "" {
		return fmt.Errorf("API Key is required")
	}

	if len(x.Config.Intents) == 0 {
		return fmt.Errorf("at least one intent must be defined")
	}

	// Initialize OpenAI-compatible clients
	c := openai.DefaultConfig(x.Config.Key)
	c.BaseURL = x.Config.Url
	x.Client = openai.NewClientWithConfig(c)

	// Initialize the system prompt template
	if x.Config.SystemPrompt != "" {
		x.systemPromptTemplate, err = el.NewTemplate(x.Config.SystemPrompt)
		if err != nil {
			return err
		}
		if x.systemPromptTemplate.HasVar() {
			x.hasVar = true
		}
	}

	// Initialize the user input template
	x.Config.Input = strings.TrimSpace(x.Config.Input)
	if x.Config.Input != "" {
		if tmpl, err := el.NewTemplate(x.Config.Input); err != nil {
			return fmt.Errorf("invalid input expression: %v", err)
		} else {
			x.userInputTemplate = tmpl
			if x.userInputTemplate.HasVar() {
				x.hasVar = true
			}
		}
	}

	return nil
}

// OnMsg processes a message
func (x *IntentNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	var evn map[string]interface{}
	if x.hasVar {
		evn = base.NodeUtils.GetEvnAndMetadata(ctx, msg)
	}

	// Retrieves user input text
	var userInput string
	if x.userInputTemplate != nil {
		if v, err := x.userInputTemplate.Execute(evn); err != nil {
			ctx.TellFailure(msg, fmt.Errorf("failed to execute user input template: %v", err))
			return
		} else {
			userInput = str.ToString(v)
		}
	} else {
		userInput = msg.GetData()
	}

	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		ctx.TellFailure(msg, fmt.Errorf("empty user input"))
		return
	}

	// Intent recognition
	intentName, err := x.recognizeIntent(userInput, evn)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}

	// Write the recognition results into metadata
	msg.GetMetadata().PutValue(IntentMetadataKey, intentName)

	// Routing based on the recognized intent: matches to the predefined intent map name; otherwise, use the default relationship type
	if x.isValidIntent(intentName) {
		ctx.TellNext(msg, intentName)
	} else {
		ctx.TellNext(msg, types.DefaultRelationType)
	}
}

// recognizeIntent calls the large model to recognize intent, returning only the intent name
func (x *IntentNode) recognizeIntent(userInput string, evn map[string]interface{}) (string, error) {
	var prompt string
	if x.systemPromptTemplate != nil {
		prompt = x.systemPromptTemplate.ExecuteAsString(evn)
	} else {
		prompt = x.buildDefaultPrompt()
	}

	// Build the request
	req := openai.ChatCompletionRequest{
		Model: x.Config.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: prompt},
			{Role: openai.ChatMessageRoleUser, Content: userInput},
		},
		Temperature: x.Config.Temperature,
	}
	if x.Config.MaxTokens > 0 {
		req.MaxTokens = x.Config.MaxTokens
	}

	resp, err := x.Client.CreateChatCompletion(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("failed to call AI Model: %v", err)
	}

	if len(resp.Choices) == 0 {
		return x.Config.DefaultIntent, nil
	}

	// Extract intent name: Clean up responses, only take intent names
	content := cleanIntentResponse(resp.Choices[0].Message.Content)

	// Verify whether intent is in a predefined list
	if !x.isValidIntent(content) {
		return x.Config.DefaultIntent, nil
	}

	return content, nil
}

// cleanIntentResponse: Cleans model responses and extracts intent names
func cleanIntentResponse(content string) string {
	content = strings.TrimSpace(content)
	// Extract the contents of the markdown code block: ''`xxx\ncontent\n`''
	if match := reCodeBlockContent.FindStringSubmatch(content); len(match) > 1 {
		content = match[1]
	}
	content = strings.TrimSpace(content)
	// Remove quotation marks
	content = strings.Trim(content, "\"'`")
	content = strings.TrimSpace(content)
	// Only the first line is taken
	if idx := strings.Index(content, "\n"); idx >= 0 {
		content = content[:idx]
	}
	return strings.TrimSpace(content)
}

// buildDefaultPrompt Constructs a default prompt (concise, only requires the intent name)
func (x *IntentNode) buildDefaultPrompt() string {
	var intentNames []string
	var intentDetails []string
	for _, intent := range x.Config.Intents {
		intentNames = append(intentNames, intent.Name)
		intentDetails = append(intentDetails, fmt.Sprintf("- %s: %s", intent.Name, intent.Description))
	}

	return fmt.Sprintf(`你是一个意图分类器。根据用户输入，判断属于以下哪个意图：

%s

规则：
- 只输出意图名称，不要输出任何其他内容
- 意图必须是以下之一：%s
- 无法判断时输出：%s`, strings.Join(intentDetails, "\n"), strings.Join(intentNames, "、"), x.Config.DefaultIntent)
}

// isValidIntent checks whether the intent is valid
func (x *IntentNode) isValidIntent(intent string) bool {
	for _, definedIntent := range x.Config.Intents {
		if definedIntent.Name == intent {
			return true
		}
	}
	return false
}

// Destroy resources
func (x *IntentNode) Destroy() {
	// Release resources
}

// Desc returns the component description
func (x *IntentNode) Desc() string {
	return "Classify user intent via LLM and route to matching connection. Sends user input with predefined intent list to LLM, routes to matched intent name or default"
}

// Regex: extracting content from the markdown code block
var reCodeBlockContent = regexp.MustCompile("(?s)```(?:\\w+)?\\s*\\n?(.*?)\\n?```")
