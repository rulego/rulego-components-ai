/*
 * Copyright 2023 The RuleGo Authors.
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

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/config"
	aitool "github.com/rulego/rulego-components-ai/tool"
	mcpadapter "github.com/rulego/rulego-components-ai/tool/mcp"
	"github.com/rulego/rulego/api/types"
)

// DefaultAgentInputSchema 子智能体的默认输入参数 schema
const DefaultAgentInputSchema = `{
	"type": "object",
	"properties": {
		"messages": {
			"type": "array",
			"description": "OpenAI format messages array. When user provides images, you MUST include image_url content parts with the image URL or base64 data.",
			"items": {
				"type": "object",
				"properties": {
					"role": {
						"type": "string",
						"enum": ["user", "assistant", "system"]
					},
					"content": {
						"oneOf": [
							{
								"type": "string",
								"description": "Simple text message"
							},
							{
								"type": "array",
								"description": "Multimodal content array with text and images",
								"items": {
									"oneOf": [
										{
											"type": "object",
											"properties": {
												"type": {
													"type": "string",
													"const": "text"
												},
												"text": {
													"type": "string",
													"description": "Text content"
												}
											},
											"required": ["type", "text"]
										},
										{
											"type": "object",
											"properties": {
												"type": {
													"type": "string",
													"const": "image_url"
												},
												"image_url": {
													"type": "object",
													"properties": {
														"url": {
															"type": "string",
															"description": "Image URL or base64 data URI (data:image/...;base64,...)"
														}
													},
													"required": ["url"]
												}
											},
											"required": ["type", "image_url"]
										}
									]
								}
							}
						]
					}
				},
				"required": ["role", "content"]
			}
		},
		"message": {
			"type": "string",
			"description": "Simple text message (alternative to messages array)"
		}
	}
}`

// ============================================
// Model Factory
// ============================================

// ModelOptions 模型创建选项
type ModelOptions struct {
	Logger     types.Logger
	WrapRetry  bool
	MaxRetries int
}

// CreateChatModel 创建聊天模型
func CreateChatModel(llmConfig config.LLMConfig, opts ...ModelOptions) (model.ToolCallingChatModel, error) {
	llmConfig.Url = strings.TrimSpace(llmConfig.Url)
	if llmConfig.Url == "" {
		return nil, fmt.Errorf("URL is missing")
	}
	llmConfig.Key = strings.TrimSpace(llmConfig.Key)

	// 应用默认参数
	temperature := llmConfig.Params.Temperature
	if temperature == 0 {
		temperature = config.DefaultTemperature
	}
	topP := llmConfig.Params.TopP
	if topP == 0 {
		topP = config.DefaultTopP
	}
	frequencyPenalty := llmConfig.Params.FrequencyPenalty
	if frequencyPenalty == 0 {
		frequencyPenalty = config.DefaultFrequencyPenalty
	}
	presencePenalty := llmConfig.Params.PresencePenalty
	if presencePenalty == 0 {
		presencePenalty = config.DefaultPresencePenalty
	}

	// maxTokens=0 表示不设置，使用模型默认值
	var maxCompletionTokens *int
	if llmConfig.Params.MaxTokens > 0 {
		maxCompletionTokens = &llmConfig.Params.MaxTokens
	}

	openaiConfig := &openai.ChatModelConfig{
		BaseURL:             llmConfig.Url,
		APIKey:              llmConfig.Key,
		Model:               llmConfig.Model,
		Temperature:         &temperature,
		TopP:                &topP,
		MaxCompletionTokens: maxCompletionTokens,
		FrequencyPenalty:    &frequencyPenalty,
		PresencePenalty:     &presencePenalty,
	}

	// Handle Stop sequences
	if len(llmConfig.Params.Stop) > 0 {
		openaiConfig.Stop = llmConfig.Params.Stop
	}

	// 合并用户自定义的 ExtraFields
	if len(llmConfig.Params.ExtraFields) > 0 {
		if openaiConfig.ExtraFields == nil {
			openaiConfig.ExtraFields = make(map[string]any)
		}
		for k, v := range llmConfig.Params.ExtraFields {
			openaiConfig.ExtraFields[k] = v
		}
	}

	baseModel, err := openai.NewChatModel(context.Background(), openaiConfig)
	if err != nil {
		return nil, err
	}

	var chatModel model.ToolCallingChatModel = baseModel

	// 可选：包装重试逻辑
	if len(opts) > 0 && opts[0].WrapRetry && opts[0].MaxRetries > 0 {
		chatModel = NewRetryChatModelWrapper(chatModel, opts[0].MaxRetries, opts[0].Logger)
	}

	return chatModel, nil
}

// ============================================
// Tool Factory
// ============================================

// ToolOptions 工具创建选项
type ToolOptions struct {
	RuleConfig     types.Config
	RuleEnginePool types.RuleEnginePool
	WrapVisual     bool
	WrapOptions    ToolWrapOptions
	Logger         types.Logger
}

// CreateTools 批量创建工具
func CreateTools(toolsConfig []config.Tool, opts ToolOptions) ([]tool.BaseTool, []*schema.ToolInfo, error) {
	var tools []tool.BaseTool
	var toolInfoList []*schema.ToolInfo

	for _, toolConfig := range toolsConfig {
		if toolConfig.Type == config.ToolTypeMCP {
			// MCP 类型：一条 config 展开为多个工具
			mcpTools, mcpInfos, err := createMCPTools(toolConfig, opts)
			if err != nil {
				return nil, nil, err
			}
			tools = append(tools, mcpTools...)
			toolInfoList = append(toolInfoList, mcpInfos...)
		} else {
			// 其他类型：单个工具
			t, info, err := CreateTool(toolConfig, opts)
			if err != nil {
				return nil, nil, err
			}
			tools = append(tools, t)
			toolInfoList = append(toolInfoList, info)
		}
	}

	return tools, toolInfoList, nil
}

// createMCPTools 从 MCP 工具配置创建工具列表。
// 支持 self（进程内）和远程（http/stdio）两种模式。
// tools 字段为可选过滤器：nil/空 表示自动发现全部工具。
func createMCPTools(toolConfig config.Tool, opts ToolOptions) ([]tool.BaseTool, []*schema.ToolInfo, error) {
	server := ""
	var filterTools []string

	if toolConfig.Config != nil {
		if s, ok := toolConfig.Config["server"].(string); ok {
			server = s
		}
		if ts, ok := toolConfig.Config["tools"].([]interface{}); ok {
			for _, t := range ts {
				if s, ok := t.(string); ok {
					filterTools = append(filterTools, s)
				}
			}
		}
	}

	var tools []tool.BaseTool
	var err error

	switch {
	case server == "self":
		tools, err = createSelfMCPTools(opts.RuleConfig, filterTools)
	case server != "":
		tools, err = createRemoteMCPTools(server, filterTools)
	default:
		return nil, nil, fmt.Errorf("mcp 工具配置缺少 server 字段")
	}

	if err != nil {
		return nil, nil, err
	}

	// 获取工具信息
	var infos []*schema.ToolInfo
	for _, t := range tools {
		info, e := t.Info(context.Background())
		if e != nil {
			return nil, nil, e
		}
		infos = append(infos, info)
	}

	// 可选：包装可视化
	if opts.WrapVisual {
		for i, t := range tools {
			if invokable, ok := t.(tool.InvokableTool); ok {
				wrapOpts := opts.WrapOptions
				wrapOpts.Name = infos[i].Name
				wrapOpts.ToolType = aspect.ToolTypeMCP
				wrapOpts.TargetId = infos[i].Name
				tools[i] = NewVisualToolWrapper(invokable, wrapOpts)
			}
		}
	}

	return tools, infos, nil
}

// createSelfMCPTools 进程内模式：通过 RuleConfig UDF 获取 MCPToolProvider。
func createSelfMCPTools(ruleConfig types.Config, toolNames []string) ([]tool.BaseTool, error) {
	provider, ok := ruleConfig.GetUdf(types.MCPToolProviderKey, "").(types.MCPToolProvider)
	if !ok {
		return nil, fmt.Errorf("mcp_tool_provider 未在 RuleConfig UDF 中注册，self 模式无法使用")
	}
	return mcpadapter.CreateToolsFromProvider(provider, toolNames)
}

// createRemoteMCPTools 远程模式：通过 MCP 协议的 tools/list 自动发现工具。
func createRemoteMCPTools(server string, toolNames []string) ([]tool.BaseTool, error) {
	return mcpadapter.CreateToolsFromRemote(server, toolNames)
}

// CreateTool 创建单个工具
func CreateTool(toolConfig config.Tool, opts ToolOptions) (tool.BaseTool, *schema.ToolInfo, error) {
	var toolInstance tool.BaseTool
	var err error

	switch toolConfig.Type {
	case config.ToolTypeAgent:
		// agent 类型：自动从目标智能体配置获取名称和描述
		toolConfig = fillAgentToolInfo(toolConfig, opts.RuleEnginePool, opts.Logger)
		toolInstance = NewRuleGoTool(toolConfig)

	case config.ToolTypeRuleChain:
		// rulechain 类型：使用配置中的名称和描述
		toolInstance = NewRuleGoTool(toolConfig)

	case config.ToolTypeBuiltin:
		var t tool.BaseTool
		var ok bool

		// 1. 优先通过工厂创建独立实例（支持自定义配置，且避免共享状态）
		if def, okDef := aitool.Registry.GetDef(toolConfig.Name); okDef && def.Factory != nil {
			cfg := toolConfig.Config
			if cfg == nil {
				cfg = map[string]interface{}{}
			}
			if instance, err := def.Factory(cfg); err == nil {
				t = instance
				ok = true
			}
		}

		// 2. 从 RuleConfig UDF 获取
		if !ok {
			t, ok = aitool.GetToolFromConfig(opts.RuleConfig, toolConfig.Name)
		}

		// 3. 从全局注册表获取预注册的共享实例（轻量无状态工具）
		if !ok {
			t, ok = aitool.Registry.Get(toolConfig.Name)
		}

		if !ok {
			return nil, nil, fmt.Errorf("builtin tool not found: %s", toolConfig.Name)
		}
		toolInstance = t

	default:
		return nil, nil, fmt.Errorf("unsupported tool type: %s", toolConfig.Type)
	}

	// 获取工具信息
	toolInfo, err := toolInstance.Info(context.Background())
	if err != nil {
		return nil, nil, err
	}

	// 可选：包装可视化逻辑
	if opts.WrapVisual {
		if invokable, ok := toolInstance.(tool.InvokableTool); ok {
			wrapOpts := opts.WrapOptions
			wrapOpts.Name = toolConfig.Name
			// 确定工具类型
			switch toolConfig.Type {
			case config.ToolTypeBuiltin:
				wrapOpts.ToolType = aspect.ToolTypeBuiltin
				wrapOpts.TargetId = toolConfig.Name
			case config.ToolTypeRuleChain:
				wrapOpts.ToolType = aspect.ToolTypeRuleChain
				wrapOpts.TargetId = toolConfig.TargetId
			case config.ToolTypeAgent:
				wrapOpts.ToolType = aspect.ToolTypeSubAgent
				wrapOpts.TargetId = toolConfig.TargetId
			default:
				wrapOpts.ToolType = aspect.ToolTypeUnknown
				wrapOpts.TargetId = toolConfig.Name
			}
			toolInstance = NewVisualToolWrapper(invokable, wrapOpts)
		}
	}

	return toolInstance, toolInfo, nil
}

// fillAgentToolInfo 从目标智能体配置中填充工具名称、描述和参数
func fillAgentToolInfo(toolConfig config.Tool, ruleEnginePool types.RuleEnginePool, logger types.Logger) config.Tool {
	// 如果名称、描述和参数都已提供，直接返回
	if toolConfig.Name != "" && toolConfig.Description != "" && toolConfig.Parameters != "" {
		return toolConfig
	}

	// 获取目标规则链定义
	if toolConfig.TargetId == "" || ruleEnginePool == nil {
		return toolConfig
	}

	targetEngine, ok := ruleEnginePool.Get(toolConfig.TargetId)
	if !ok || targetEngine == nil {
		if logger != nil {
			logger.Warnf("fillAgentToolInfo: target agent not found: %s", toolConfig.TargetId)
		}
		return toolConfig
	}

	// 获取规则链定义
	chainDef := targetEngine.Definition()

	// 填充名称（如果未提供）
	if toolConfig.Name == "" {
		toolConfig.Name = chainDef.RuleChain.Name
	}

	// 填充描述（如果未提供）
	if toolConfig.Description == "" {
		if desc, ok := chainDef.RuleChain.GetAdditionalInfo("description"); ok {
			toolConfig.Description = fmt.Sprintf("%v", desc)
		}
	}

	// 填充参数（如果未提供）
	if toolConfig.Parameters == "" {
		toolConfig.Parameters = getAgentInputSchema(chainDef)
	}

	return toolConfig
}

// getAgentInputSchema 获取智能体的输入参数定义
func getAgentInputSchema(chainDef types.RuleChain) string {
	// 尝试从 additionalInfo.inputSchema 获取
	if inputSchema, ok := chainDef.RuleChain.GetAdditionalInfo("inputSchema"); ok {
		switch v := inputSchema.(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]interface{}:
			if bytes, err := json.Marshal(v); err == nil && len(bytes) > 2 {
				return string(bytes)
			}
		}
	}

	// 默认使用 OpenAI 标准消息格式
	return DefaultAgentInputSchema
}

// ============================================
// Agent Factory
// ============================================

// AgentOptions Agent 创建选项
type AgentOptions struct {
	Name         string
	Description  string
	SystemPrompt string
	MaxStep      int
	ToolsConfig  compose.ToolsNodeConfig
	Logger       types.Logger
	// MessageModifier 在每次模型调用前修改消息列表。
	// 用于动态注入内容（如技能列表）到 system prompt。
	// 在 MessageRewriter 之后执行。
	MessageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
}

// CreateReactAgent 创建 React Agent
func CreateReactAgent(ctx context.Context, chatModel model.ToolCallingChatModel, opts AgentOptions) (*react.Agent, error) {
	maxStep := opts.MaxStep
	if maxStep <= 0 {
		maxStep = DefaultMaxStep
	}

	cfg := &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig:      opts.ToolsConfig,
		MaxStep:          maxStep,
		// 提供自定义的 StreamToolCallChecker，用于处理先输出内容再输出工具调用的模型
		StreamToolCallChecker: createStreamToolCallChecker(opts.Logger),
		// 在每次模型调用前清洗 tool_calls 的 Arguments 字段，
		// 避免空字符串因 omitempty 被省略导致部分 API（如 DashScope）返回 400 错误
		MessageRewriter: sanitizeToolCallArguments,
		// 动态注入技能列表等运行时内容到 system prompt
		MessageModifier: opts.MessageModifier,
	}

	return react.NewAgent(ctx, cfg)
}

// sanitizeToolCallArguments 确保 assistant 消息中所有 tool_calls 的 Arguments 字段不为空。
// 部分模型 API（如 DashScope/Qwen）要求 function.arguments 必须存在且为有效 JSON，
// 而 eino schema.FunctionCall.Arguments 和 go-openai FunctionCall.Arguments 均使用了
// json:"arguments,omitempty" 标签，空字符串会在序列化时被省略导致 400 错误。
// 注意：此函数创建消息的浅拷贝，不修改传入的原始消息。
func sanitizeToolCallArguments(_ context.Context, msgs []*schema.Message) []*schema.Message {
	result := make([]*schema.Message, len(msgs))
	for i, msg := range msgs {
		newMsg := *msg // 浅拷贝
		if newMsg.Role == schema.Assistant && len(newMsg.ToolCalls) > 0 {
			newMsg.ToolCalls = make([]schema.ToolCall, len(msg.ToolCalls))
			copy(newMsg.ToolCalls, msg.ToolCalls)
			for j := range newMsg.ToolCalls {
				args := strings.TrimSpace(newMsg.ToolCalls[j].Function.Arguments)
				if args == "" || args == "null" {
					newMsg.ToolCalls[j].Function.Arguments = "{}"
				}
			}
		}
		result[i] = &newMsg
	}
	return result
}

// createStreamToolCallChecker 创建流式工具调用检查器
func createStreamToolCallChecker(logger types.Logger) func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	return func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
		defer sr.Close()

		hasToolCall := false
		chunkCount := 0

		for {
			msg, err := sr.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				return false, err
			}

			chunkCount++

			// 防止无限循环
			if chunkCount > config.MaxStreamChunks {
				if logger != nil {
					logger.Warnf("StreamToolCallChecker: MaxStreamChunks (%d) exceeded", config.MaxStreamChunks)
				}
				break
			}

			// 流式阶段只要已经出现具名工具调用，就应该进入工具执行流程。
			// 参数完整性由后续执行入口统一校验，避免流式增量参数被误判成“无工具调用”。
			if hasStreamToolCalls(msg.ToolCalls) {
				hasToolCall = true
			}
		}

		return hasToolCall, nil
	}
}

// hasStreamToolCalls 判断流式消息中是否已经出现具名工具调用。
func hasStreamToolCalls(toolCalls []schema.ToolCall) bool {
	for _, toolCall := range toolCalls {
		if strings.TrimSpace(toolCall.Function.Name) != "" {
			return true
		}
	}
	return false
}

// buildToolsConfig 构建工具配置
func buildToolsConfig(tools []tool.BaseTool) compose.ToolsNodeConfig {
	return compose.ToolsNodeConfig{
		Tools:               tools,
		ExecuteSequentially: false, // 固定为并行执行
		// UnknownToolsHandler 处理 LLM 幻觉产生的未知工具调用
		// 当 LLM 返回的工具名称为空或不在已注册工具列表中时，此处理器会被调用
		UnknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
			if name == "" {
				return "错误：工具名称为空，请检查工具调用格式是否正确。", nil
			}
			return fmt.Sprintf("错误：未知工具 '%s'，请使用已注册的工具。", name), nil
		},
	}
}

