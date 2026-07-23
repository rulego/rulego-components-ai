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
	"os"
	"strings"
	"time"

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

// DefaultAgentInputSchema The default input parameter schema for the child agent
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

// ModelOptions Model creation options
type ModelOptions struct {
	Logger     types.Logger
	WrapRetry  bool
	MaxRetries int
}

// CreateChatModel creates a chat model. Automatically assemble according to config:
//   - Create a bare OpenAI ChatModel for each endpoint (main + Failover backup) and click opts.WrapRetry Package RetryChatModelWrapper
//     (When StreamRetry = StreamRetryFull is enabled, full mid-stream retry is enabled.)
//   - When Failover is configured, wrap it with FailoverChatModelWrapper to form a "retry → backup endpoint with the same model" link.
func CreateChatModel(llmConfig config.LLMConfig, opts ...ModelOptions) (model.ToolCallingChatModel, error) {
	// Detect proxy environment variables: go http.DefaultClient reads HTTP_PROXY/HTTPS_PROXY. If LLM requests go through a proxy,
	// Poor handling of SSE long connections (buffering/timeout/disconnection) by proxies is a common environmental cause of "Error in input stream".
	if os.Getenv("HTTPS_PROXY") != "" || os.Getenv("HTTP_PROXY") != "" || os.Getenv("https_proxy") != "" || os.Getenv("http_proxy") != "" {
		if len(opts) > 0 && opts[0].Logger != nil {
			opts[0].Logger.Warnf("[CreateChatModel] HTTP_PROXY/HTTPS_PROXY environment variables detected. LLM requests will use the proxy, which may interrupt the SSE stream (Error in input stream). Clear the proxy variables unless they are required")
		}
	}
	primary, err := createEndpointModel(llmConfig, opts)
	if err != nil {
		return nil, err
	}
	if len(llmConfig.Failover) == 0 {
		return primary, nil
	}

	var logger types.Logger
	if len(opts) > 0 {
		logger = opts[0].Logger
	}
	failovers := make([]model.ToolCallingChatModel, 0, len(llmConfig.Failover))
	for i, ep := range llmConfig.Failover {
		epCfg := applyFailoverEndpoint(llmConfig, ep)
		epModel, err := createEndpointModel(epCfg, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to create failover model #%d (%s): %v", i+1, epCfg.Model, err)
		}
		// Backup endpoints use fixedModelWrapper to fix their configuration model names, without inheriting session-level session_model (for the main provider).
		// Avoid the backup provider receiving an unsupported model name triggering "Model does not exist".
		failovers = append(failovers, &fixedModelWrapper{base: epModel, fixedModel: epCfg.Model})
	}
	fo := NewFailoverChatModelWrapper(primary, failovers, logger)
	// Enable the master endpoint fuse: the main retry is exhausted and the fuse blows; during cooldown, skip the main and use it as a standby directly.
	// This prevents each request from waiting for the master retry to exhaust during long-term master failures. Cooling can be overridden by config, 0 is set to default;
	// During a main persistent fault, detection cooling doubles and caps for 10 minutes each time; if detection succeeds, it resets back to base cooling.
	fo = fo.WithCircuit(time.Duration(llmConfig.CircuitCooldownSec) * time.Second)

	// In off mode, failover only overrides creation/early disconnection: once the endpoint stream returns to the reader, the mid-stream is in the later segment
	// Broken stream will be transmitted to the front end (off has already exported the first half in real time; switching endpoints will duplicate content and cannot be rescued); Only full mode is available
	// Only then can there be complete mid-stream fault tolerance. Failover is configured but prompted once when the default is off, avoiding the mistaken belief that full fault tolerance is available.
	if llmConfig.StreamRetryMode != config.StreamRetryFull && logger != nil {
		logger.Printf("[FailoverChatModel] streamRetryMode=off: failover covers only connect/early-stream errors; mid-stream breaks pass through. Set streamRetryMode=full for mid-stream failover.")
	}
	return fo, nil
}

// applyFailoverEndpoint derives the backup endpoint configuration from the primary configuration: the entire configuration inherits the primary configuration (including Params), then overwrites with ep
// url/key/model;  ep.Params When not nil, the entire group covers the master Params (nil = inherited master). Extracted as a function so that unit tests can cover the logic.
func applyFailoverEndpoint(mainCfg config.LLMConfig, ep config.FailoverEndpoint) config.LLMConfig {
	epCfg := mainCfg
	if ep.Url != "" {
		epCfg.Url = ep.Url
	}
	if ep.Key != "" {
		epCfg.Key = ep.Key
	}
	if ep.Model != "" {
		epCfg.Model = ep.Model
	}
	if ep.Params != nil {
		epCfg.Params = *ep.Params
	}
	return epCfg
}

// createEndpointModel creates a ChatModel (naked OpenAI) for a single endpoint by clicking opts.WrapRetry decides whether to retry or wrap it,
// Then use llmConfig.StreamRetry to set the full mid-stream retry mode.
func createEndpointModel(llmConfig config.LLMConfig, opts []ModelOptions) (model.ToolCallingChatModel, error) {
	llmConfig.Url = strings.TrimSpace(llmConfig.Url)
	if llmConfig.Url == "" {
		return nil, fmt.Errorf("URL is missing")
	}
	llmConfig.Key = strings.TrimSpace(llmConfig.Key)

	// Apply default parameters
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

	// maxTokens=0 means no settings are set, using the model's default values
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

	// Merging user-defined ExtraFields (supports point path keys, such as thinking.type → thinking:{type:...})
	if len(llmConfig.Params.ExtraFields) > 0 {
		if openaiConfig.ExtraFields == nil {
			openaiConfig.ExtraFields = make(map[string]any)
		}
		for k, v := range llmConfig.Params.ExtraFields {
			setNestedExtraField(openaiConfig.ExtraFields, k, v)
		}
	}

	baseModel, err := openai.NewChatModel(context.Background(), openaiConfig)
	if err != nil {
		return nil, err
	}

	var chatModel model.ToolCallingChatModel = baseModel

	// Optional: Package retry logic. When MaxRetries <=0, the wrapper uses the default number of iterations.
	// Enable full mid-stream retries (buffered replay, sacrificing real-time playback) when StreamRetryMode=full.
	if len(opts) > 0 && opts[0].WrapRetry {
		rw := NewRetryChatModelWrapper(chatModel, opts[0].MaxRetries, opts[0].Logger)
		streamFull := llmConfig.StreamRetryMode == config.StreamRetryFull
		rw.SetStreamFull(streamFull)
		if opts[0].Logger != nil {
			modeDesc := "off（仅探测窗口内重试，窗口外断流透传）"
			if streamFull {
				modeDesc = "full（完整缓冲+重试+重放）"
			}
			opts[0].Logger.Debugf("[CreateChatModel] model=%s streamRetryMode=%q → %s", llmConfig.Model, llmConfig.StreamRetryMode, modeDesc)
		}
		chatModel = rw
	}

	return chatModel, nil
}

// setNestedExtraField expands the point path key (such as "thinking.type") into a nested map.
// Pointless keys are directly assigned. Used to convert flat ExtraFields configurations into nested structures required by the model API.
func setNestedExtraField(m map[string]any, key string, value any) {
	if !strings.Contains(key, ".") {
		m[key] = value
		return
	}
	parts := strings.Split(key, ".")
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		if next, ok := cur[p].(map[string]any); ok {
			cur = next
		} else {
			next := make(map[string]any)
			cur[p] = next
			cur = next
		}
	}
}

// ============================================
// Tool Factory
// ============================================

// ToolOptions tool creates options
type ToolOptions struct {
	RuleConfig     types.Config
	RuleEnginePool types.RuleEnginePool
	WrapVisual     bool
	WrapOptions    ToolWrapOptions
	Logger         types.Logger
}

// CreateTools batch creation tool
func CreateTools(toolsConfig []config.Tool, opts ToolOptions) ([]tool.BaseTool, []*schema.ToolInfo, aitool.DynamicSkillLister, error) {
	var tools []tool.BaseTool
	var toolInfoList []*schema.ToolInfo
	var skillLister aitool.DynamicSkillLister

	for _, toolConfig := range toolsConfig {
		if toolConfig.Type == config.ToolTypeMCP {
			// MCP Type: A single config can be expanded into multiple tools
			mcpTools, mcpInfos, err := createMCPTools(toolConfig, opts)
			if err != nil {
				return nil, nil, nil, err
			}
			tools = append(tools, mcpTools...)
			toolInfoList = append(toolInfoList, mcpInfos...)
		} else {
			// Other types: single tools
			t, info, sl, err := CreateTool(toolConfig, opts)
			if err != nil {
				return nil, nil, nil, err
			}
			if sl != nil && skillLister == nil {
				skillLister = sl
			}
			tools = append(tools, t)
			toolInfoList = append(toolInfoList, info)
		}
	}

	return tools, toolInfoList, skillLister, nil
}

// createMCPTools Create a list of tools from the MCP tool configuration.
// Supports both self (in-process) and remote (http/stdio) modes.
// The tools field is an optional filter: nil/empty to automatically discover all tools.
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

	// Obtain tool information
	var infos []*schema.ToolInfo
	for _, t := range tools {
		info, e := t.Info(context.Background())
		if e != nil {
			return nil, nil, e
		}
		infos = append(infos, info)
	}

	// Optional: Packaging visualization
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

// createSelfMCPTools In-process mode: Obtain MCPToolProvider via RuleConfig UDF.
func createSelfMCPTools(ruleConfig types.Config, toolNames []string) ([]tool.BaseTool, error) {
	provider, ok := ruleConfig.GetUdf(types.MCPToolProviderKey, "").(types.MCPToolProvider)
	if !ok {
		return nil, fmt.Errorf("mcp_tool_provider 未在 RuleConfig UDF 中注册，self 模式无法使用")
	}
	return mcpadapter.CreateToolsFromProvider(provider, toolNames)
}

// createRemoteMCPTools Remote mode: Automatically discovers tools through the MCP protocol's tools/list.
func createRemoteMCPTools(server string, toolNames []string) ([]tool.BaseTool, error) {
	return mcpadapter.CreateToolsFromRemote(server, toolNames)
}

// CreateTool creates a single tool
func CreateTool(toolConfig config.Tool, opts ToolOptions) (tool.BaseTool, *schema.ToolInfo, aitool.DynamicSkillLister, error) {
	var toolInstance tool.BaseTool
	var err error

	switch toolConfig.Type {
	case config.ToolTypeAgent:
		// agent type: Automatically retrieves a name and description from the target agent's configuration
		toolConfig = fillAgentToolInfo(toolConfig, opts.RuleEnginePool, opts.Logger)
		toolInstance = NewRuleGoTool(toolConfig)

	case config.ToolTypeRuleChain:
		// rulechain type: uses the name and description in the configuration
		toolInstance = NewRuleGoTool(toolConfig)

	case config.ToolTypeBuiltin:
		var t tool.BaseTool
		var ok bool

		// 1. Prioritize creating standalone instances via the factory (supports custom configuration and avoids shared status)
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

		// 2. Obtain it from RuleConfig UDF
		if !ok {
			t, ok = aitool.GetToolFromConfig(opts.RuleConfig, toolConfig.Name)
		}

		// 3. Obtain pre-registered shared instances from the global registry (lightweight, stateless tool)
		if !ok {
			t, ok = aitool.Registry.Get(toolConfig.Name)
		}

		if !ok {
			return nil, nil, nil, fmt.Errorf("builtin tool not found: %s", toolConfig.Name)
		}
		toolInstance = t

	default:
		return nil, nil, nil, fmt.Errorf("unsupported tool type: %s", toolConfig.Type)
	}

	// Obtain tool information
	toolInfo, err := toolInstance.Info(context.Background())
	if err != nil {
		return nil, nil, nil, err
	}

	// Optional: Packaging visualization logic
	// Check DynamicSkillLister before packaging (interfaces will be lost after packaging)
	var skillLister aitool.DynamicSkillLister
	if sl, ok := toolInstance.(aitool.DynamicSkillLister); ok {
		skillLister = sl
	}
	if opts.WrapVisual {
		if invokable, ok := toolInstance.(tool.InvokableTool); ok {
			wrapOpts := opts.WrapOptions
			wrapOpts.Name = toolConfig.Name
			// Determine the type of tool
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

	return toolInstance, toolInfo, skillLister, nil
}

// fillAgentToolInfo fills tool names, descriptions, and parameters from the target agent configuration
func fillAgentToolInfo(toolConfig config.Tool, ruleEnginePool types.RuleEnginePool, logger types.Logger) config.Tool {
	// If the name, description, and parameters are all provided, return directly
	if toolConfig.Name != "" && toolConfig.Description != "" && toolConfig.Parameters != "" {
		return toolConfig
	}

	// Obtain the definition of the target rule chain
	if toolConfig.TargetId == "" || ruleEnginePool == nil {
		return toolConfig
	}

	targetEngine, ok := ruleEnginePool.Get(toolConfig.TargetId)
	if !ok || targetEngine == nil {
		// Target agent not yet registered: Commonly loaded in alphabetical order by filename (main before researcher),
		// The caller (such as LoadAllAgents) will reload the completion a second time after all registrations, so debuging is downgraded to avoid startup noise.
		// A true configuration error (targetId misspelling) occurs when the tool is actually called by pool.Get failed, error is given, and it won't go silent.
		if logger != nil {
			logger.Debugf("fillAgentToolInfo: target agent not yet registered: %s (will be filled on reload)", toolConfig.TargetId)
		}
		return toolConfig
	}

	// Obtain the rule chain definition
	chainDef := targetEngine.Definition()

	// Fill the name (if not provided)
	if toolConfig.Name == "" {
		toolConfig.Name = chainDef.RuleChain.Name
	}

	// Fill description (if not provided)
	if toolConfig.Description == "" {
		if desc, ok := chainDef.RuleChain.GetAdditionalInfo("description"); ok {
			toolConfig.Description = fmt.Sprintf("%v", desc)
		}
	}

	// Filling parameters (if not provided)
	if toolConfig.Parameters == "" {
		toolConfig.Parameters = getAgentInputSchema(chainDef)
	}

	return toolConfig
}

// getAgentInputSchema Retrieves the input parameters defined by the agent
func getAgentInputSchema(chainDef types.RuleChain) string {
	// Try to get it from additionalInfo.inputSchema
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

	// By default, OpenAI uses the standard message format
	return DefaultAgentInputSchema
}

// ============================================
// Agent Factory
// ============================================

// AgentOptions Agent creates options
type AgentOptions struct {
	Name         string
	Description  string
	SystemPrompt string
	MaxStep      int
	ToolsConfig  compose.ToolsNodeConfig
	Logger       types.Logger
	// MessageModifier modifies the message list before each model call.
	// Used to dynamically inject content (such as skill lists) into system prompts.
	// Execute after MessageRewriter.
	MessageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
}

// CreateReactAgent Create a React Agent
func CreateReactAgent(ctx context.Context, chatModel model.ToolCallingChatModel, opts AgentOptions) (*react.Agent, error) {
	maxStep := opts.MaxStep
	if maxStep <= 0 {
		maxStep = DefaultMaxStep
	}

	cfg := &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig:      opts.ToolsConfig,
		MaxStep:          maxStep,
		// Provides a custom StreamToolCallChecker for handling models that output content first and then tool calls
		StreamToolCallChecker: createStreamToolCallChecker(opts.Logger),
		// Before each model call, the history is cleaned (eino react.go modelPreHandle phase for state.Messages executed, running before each model call):
		// 1) sanitizeToolCallArguments: Completes empty arguments to avoid omitting omitempty, which could cause some APIs (such as DashScope) to be omitted;
		// 2) dedupRepetitiveToolCalls: Folds consecutive tool_call with the same name and reference to avoid triggering the provider's deadloop guardrail
		//    (Zhipu GLM / DashScope qwen and others, "Repetitive tool calls detected" 400).
		MessageRewriter: func(ctx context.Context, msgs []*schema.Message) []*schema.Message {
			msgs = sanitizeToolCallArguments(ctx, msgs)
			msgs = dedupRepetitiveToolCalls(ctx, msgs)
			return msgs
		},
		// Dynamically inject runtime content such as skill lists into system prompts
		MessageModifier: opts.MessageModifier,
	}

	return react.NewAgent(ctx, cfg)
}

// sanitizeToolCallArguments ensures that all Arguments fields in the assistant message for tool_calls are not empty.
// Some model APIs (such as DashScope/Qwen) require function.arguments to exist and be a valid JSON,
// Both eino schema.FunctionCall.Arguments and go-openai FunctionCall.Arguments are used
// json:"arguments,omitempty" tag. The empty string is omitted during serialization, causing a 400 error.
// Note: This function creates a shallow copy of the message and does not modify the original incoming message.
func sanitizeToolCallArguments(_ context.Context, msgs []*schema.Message) []*schema.Message {
	result := make([]*schema.Message, len(msgs))
	for i, msg := range msgs {
		newMsg := *msg // Shallow copy
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

// dedupRepetitiveToolCalls Fold the consecutive (assistant tool_call + tool result) pair in the folded history,
// Avoid triggering the provider's "Repetitive tool calls detected" guardrail (consecutive same name and reference tool_call i.e., 400).
// Executed by MessageRewriter before each model call, retaining the last dedupKeepLast pair and deleting earlier whole pairs,
// Before retaining the correct tool result, add a "folded N times" prompt to feedback the LLM. Multiple ToolCall (parallel) should be kept without deduplication. Power equal.
func dedupRepetitiveToolCalls(_ context.Context, msgs []*schema.Message) []*schema.Message {
	// Keep the last few pairs when repeating consecutively. Must < provider guardrail threshold; In the test case, the provider only triggered six consecutive times,
	// "multiple consecutive rounds" means ≥3, keeping 2 fully safe. If stricter than the provider, change it to 1.
	const dedupKeepLast = 2

	type round struct {
		asstIdx int   // This round of assistant messages is subtitled
		tools   []int // The tool result message follows immediately after
		sig     string
	}

	// 1) Cutting wheel: Each assistant with ToolCalls + the tool result that follows
	rounds := make([]round, 0)
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role != schema.Assistant || len(m.ToolCalls) == 0 {
			continue
		}
		r := round{asstIdx: i}
		if len(m.ToolCalls) == 1 { // Only single calls are used to compute signatures, with multiple parallel conservative skips
			tc := m.ToolCalls[0]
			r.sig = tc.Function.Name + "\x00" + normalizeArgsKeyOrder(tc.Function.Arguments)
		}
		for j := i + 1; j < len(msgs) && msgs[j].Role == schema.Tool; j++ {
			r.tools = append(r.tools, j)
		}
		rounds = append(rounds, r)
	}
	if len(rounds) == 0 {
		return msgs
	}

	remove := make(map[int]bool)       // Messages to be deleted are captioned
	toolNotice := make(map[int]string) // The first tool result of the reserved round is prompted with the -> prefix

	// 2) Scan consecutive segments that are "same signature and adjacent to the original sequence," and delete the excess parts
	for i := 0; i < len(rounds); {
		if rounds[i].sig == "" {
			i++
			continue
		}
		end := i + 1
		for end < len(rounds) && rounds[end].sig == rounds[i].sig {
			// Adjacent determination: The last message of the previous round (assistant or its tool result) immediately follows the assistant of this round
			prev := rounds[end-1]
			prevEnd := prev.asstIdx
			if len(prev.tools) > 0 {
				prevEnd = prev.tools[len(prev.tools)-1]
			}
			if prevEnd+1 != rounds[end].asstIdx {
				break // Other messages (such as user/plaintext assistant) interrupt in the middle, and the message is no longer continuous
			}
			end++
		}
		segLen := end - i
		if segLen > dedupKeepLast {
			removeCnt := segLen - dedupKeepLast
			// Delete the previous removeCnt round within the segment, and keep the last dedupKeepLast round
			for k := i; k < end-dedupKeepLast; k++ {
				remove[rounds[k].asstIdx] = true
				for _, ti := range rounds[k].tools {
					remove[ti] = true
				}
			}
			keep := rounds[end-dedupKeepLast]
			toolName := ""
			if len(msgs[keep.asstIdx].ToolCalls) > 0 {
				toolName = msgs[keep.asstIdx].ToolCalls[0].Function.Name
			}
			notice := fmt.Sprintf("⚠️ 已折叠 %d 次对工具「%s」的连续重复调用（同名同参）。请勿再重复相同调用，重新读取相关文件确认当前状态或换用其他方法。\n\n", removeCnt, toolName)
			if len(keep.tools) > 0 {
				toolNotice[keep.tools[0]] = notice
			}
		}
		i = end
	}

	if len(remove) == 0 && len(toolNotice) == 0 {
		return msgs
	}

	// 3) Constructed results (shallow copy; Write the tool result for the prompt and copy it separately to avoid altering the original)
	result := make([]*schema.Message, 0, len(msgs)-len(remove))
	for idx, m := range msgs {
		if remove[idx] {
			continue
		}
		nm := *m
		if notice, ok := toolNotice[idx]; ok {
			nm.Content = notice + nm.Content
		}
		result = append(result, &nm)
	}
	return result
}

// createStreamToolCallChecker Creates a streaming tool call checker
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

			// Prevent infinite loops
			if chunkCount > config.MaxStreamChunks {
				if logger != nil {
					logger.Warnf("StreamToolCallChecker: MaxStreamChunks (%d) exceeded", config.MaxStreamChunks)
				}
				break
			}

			// In the streaming phase, as long as a named tool call has already appeared, the tool execution flow should begin.
			// Parameter integrity is uniformly checked by subsequent execution entry points to prevent stream incremental parameters from being mistakenly identified as "no tool call."
			if hasStreamToolCalls(msg.ToolCalls) {
				hasToolCall = true
			}
		}

		return hasToolCall, nil
	}
}

// hasStreamToolCalls checks whether a named tool call has appeared in the streaming message.
func hasStreamToolCalls(toolCalls []schema.ToolCall) bool {
	for _, toolCall := range toolCalls {
		if strings.TrimSpace(toolCall.Function.Name) != "" {
			return true
		}
	}
	return false
}

// buildToolsConfig Configuration for building tools
func buildToolsConfig(tools []tool.BaseTool) compose.ToolsNodeConfig {
	return compose.ToolsNodeConfig{
		Tools:               tools,
		ExecuteSequentially: false, // Fixed to parallel execution
		// UnknownToolsHandler handles unknown tool calls generated by LLM hallucinations
		// This processor is called when the LLM's returned tool name is empty or not listed in the registered tool list
		UnknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
			if name == "" {
				return "错误：工具名称为空，请检查工具调用格式是否正确。", nil
			}
			return fmt.Sprintf("错误：未知工具 '%s'，请使用已注册的工具。", name), nil
		},
	}
}
