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
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/config"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego-components-ai/tool/common"
	"github.com/rulego/rulego-components-ai/utils/token"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego/utils/el"
	"github.com/rulego/rulego/utils/maps"
)

func init() {
	_ = rulego.Registry.Register(&ReactAgentNode{})
}

// ReactAgentNodeConfig extends ChatAgentConfig
type ReactAgentNodeConfig struct {
	ChatAgentConfig `json:",squash"`
}

// ReactAgentNode implements Eino ReAct Agent
type ReactAgentNode struct {
	Config               ReactAgentNodeConfig
	agent                *react.Agent
	tools                []*schema.ToolInfo
	name                 string
	id                   string
	description          string
	systemPromptTemplate el.Template
	presetMessagesTmpls  []ChatMessageTemplate
	hasVar               bool
	aspectExecutor       *AgentAspectExecutor
	logger               types.Logger
	tokenTracker         *token.TokenTracker
	metricsCollector     *token.MetricsCollector
	ruleEnginePool       types.RuleEnginePool
	chatModel            model.ToolCallingChatModel // Save the chatModel reference for dynamic model switching
}

// Type returns the component type
func (x *ReactAgentNode) Type() string {
	return "ai/agent"
}

// New Create a ReactAgentNode instance
func (x *ReactAgentNode) New() types.Node {
	return &ReactAgentNode{
		Config: ReactAgentNodeConfig{
			ChatAgentConfig: ChatAgentConfig{
				LLMConfig: config.LLMConfig{
					Params: config.ModelParams{
						Temperature:      0.7,
						TopP:             0.9,
						FrequencyPenalty: 0.5,
						PresencePenalty:  0.5,
					},
				},
				MaxStep: 150,
			},
		},
	}
}

// applyDefaultLLMParams Apply the default LLM parameters.
// Note: Zero values (0) for parameters like Temperature will be overwritten to default values.
// If you need a deterministic output (i.e., Temperature is truly 0), use a minimum value such as 0.001.
func (x *ReactAgentNode) applyDefaultLLMParams() {
	if x.Config.Params.Temperature == 0 {
		x.Config.Params.Temperature = config.DefaultTemperature
	}
	if x.Config.Params.TopP == 0 {
		x.Config.Params.TopP = config.DefaultTopP
	}
	if x.Config.Params.FrequencyPenalty == 0 {
		x.Config.Params.FrequencyPenalty = config.DefaultFrequencyPenalty
	}
	if x.Config.Params.PresencePenalty == 0 {
		x.Config.Params.PresencePenalty = config.DefaultPresencePenalty
	}
}

// Init initializes the node
func (x *ReactAgentNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	// 1. Analyze configuration
	err := maps.Map2Struct(configuration, &x.Config)
	if err != nil {
		return err
	}

	x.logger = ruleConfig.Logger

	if x.logger != nil {
		x.logger.Debugf("ReactAgentNode.Init: Model=%s, URL=%s, Key=%s", x.Config.Model, x.Config.Url, maskAPIKey(x.Config.Key))
	}

	x.applyDefaultLLMParams()

	// 2. Initialize the template
	if err := x.initTemplates(); err != nil {
		return err
	}

	// 3. Obtain node information
	x.initNodeInfo(configuration)

	// 4. Create a chat model
	baseChatModel, err := CreateChatModel(x.Config.LLMConfig, ModelOptions{
		Logger:     ruleConfig.Logger,
		WrapRetry:  true,
		MaxRetries: x.Config.MaxRetries,
	})
	if err != nil {
		return fmt.Errorf("failed to create chat model: %v", err)
	}

	// 4.1 Wrapping Models to Support Dynamic Model Switching (Session-Level Model Switching)
	// The new model created by session switching also needs to be repackaged
	chatModel := WrapModelWithDynamicSupport(baseChatModel, x.Config.LLMConfig, ModelOptions{
		Logger:     ruleConfig.Logger,
		WrapRetry:  true,
		MaxRetries: x.Config.MaxRetries,
	})
	x.chatModel = chatModel

	// 5. Initialize the facet actuator (must be before createTools)
	x.aspectExecutor = NewAgentAspectExecutor(ruleConfig.Logger)
	x.tokenTracker = token.NewTokenTracker()
	x.metricsCollector = token.NewMetricsCollector()

	// 6. Create tools (skillLister extracts before wrapping, avoiding VisualToolWrapper masking interfaces)
	tools, toolInfoList, skillLister, err := x.createTools(ruleConfig, chatModel)
	if err != nil {
		return fmt.Errorf("failed to create tools: %v", err)
	}

	// 7. Build the MessageModifier for the skill list
	var messageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
	if skillLister != nil {
		messageModifier = BuildSkillModifier(skillLister)
	}
	// 8. Create a React Agent
	maxStep := x.Config.MaxStep
	if maxStep <= 0 {
		maxStep = DefaultMaxStep
	}

	agent, err := CreateReactAgent(context.Background(), chatModel, AgentOptions{
		MaxStep:         maxStep,
		ToolsConfig:     buildToolsConfig(tools),
		Logger:          ruleConfig.Logger,
		MessageModifier: messageModifier,
	})
	if err != nil {
		return fmt.Errorf("failed to create react agent: %v", err)
	}

	x.agent = agent
	x.tools = toolInfoList

	return nil
}

// initTemplates initializes the template
func (x *ReactAgentNode) initTemplates() error {
	if x.Config.SystemPrompt != "" {
		tmpl, err := el.NewTemplate(x.Config.SystemPrompt)
		if err != nil {
			return fmt.Errorf("failed to create system prompt template: %v", err)
		}
		x.systemPromptTemplate = tmpl
		if tmpl.HasVar() {
			x.hasVar = true
		}
	}

	if len(x.Config.Messages) > 0 {
		for _, msg := range x.Config.Messages {
			tmpl, err := el.NewTemplate(msg.GetContentAsString())
			if err != nil {
				return fmt.Errorf("failed to create message template: %v", err)
			}
			x.presetMessagesTmpls = append(x.presetMessagesTmpls, ChatMessageTemplate{
				Role:            msg.Role,
				ContentTemplate: tmpl,
			})
		}
	}
	return nil
}

// initNodeInfo initializes node information
func (x *ReactAgentNode) initNodeInfo(configuration types.Configuration) {
	self := base.NodeUtils.GetSelfDefinition(configuration)
	x.id = self.Id
	x.name = self.Name
	if desc, ok := self.GetAdditionalInfo("description"); ok {
		x.description = fmt.Sprintf("%v", desc)
	}

	chainCtx := base.NodeUtils.GetChainCtx(configuration)
	if chainCtx != nil {
		x.ruleEnginePool = chainCtx.GetRuleEnginePool()
	}
}

// createTools Create tools
func (x *ReactAgentNode) createTools(ruleConfig types.Config, chatModel interface{}) ([]tool.BaseTool, []*schema.ToolInfo, aitool.DynamicSkillLister, error) {
	return CreateTools(x.Config.Tools, ToolOptions{
		RuleConfig:     ruleConfig,
		RuleEnginePool: x.ruleEnginePool,
		WrapVisual:     true,
		WrapOptions: ToolWrapOptions{
			AgentId:             x.name,
			AgentName:           x.name,
			AspectManager:       x.aspectExecutor.Manager(),
			MaxStep:             x.Config.MaxStep,
			MaxToolOutputLength: x.Config.MaxToolOutputLength,
			Logger:              x.logger,
			MetricsCollector:    x.metricsCollector,
		},
		Logger: x.logger,
	})
}

// OnMsg processes a message
func (x *ReactAgentNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	// 1. Convert input
	adkInput, err := ConvertRuleMsgToAgentInput(ctx, msg, x.systemPromptTemplate, x.hasVar, x.Config.SystemPrompt, x.presetMessagesTmpls, x.Config.Model, x.id, x.logger)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}

	// 2. Build execution context
	runCtx := x.buildRunContext(ctx, msg)

	// 3. Obtain the rule chain ID
	chainId := ""
	if ctx.RuleChain() != nil {
		chainId = ctx.RuleChain().GetNodeId().Id
	}
	if chainId == "" {
		chainId = x.name
	}

	// 4. Build the Facet Input
	resolvedSystemPrompt := extractResolvedSystemPrompt(adkInput)
	agentInput := x.buildAgentInput(adkInput, msg, resolvedSystemPrompt)

	// 5. Execute according to the pattern
	opts := ExecuteOptions{
		ChainId:    chainId,
		AgentName:  x.name,
		Msg:        msg,
		SessionKey: msg.Metadata.GetValue("sessionKey"),
	}

	if x.isStreamMode(msg) {
		x.executeStream(ctx, msg, runCtx, opts, agentInput, adkInput)
	} else {
		x.executeSync(ctx, msg, runCtx, opts, agentInput, adkInput)
	}
}

// buildRunContext constructs the execution context
func (x *ReactAgentNode) buildRunContext(ctx types.RuleContext, msg types.RuleMsg) context.Context {
	runCtx := context.WithValue(ctx.GetContext(), config.ShareRuleContextKey, ctx)
	runCtx = context.WithValue(runCtx, config.KeyRuleConfig, ctx.Config())

	// workDir is not declared at agent nodes: uniformly handled by the business layer through common.WithWorkDir injects ctx on demand (e.g.,
	// triggerGenerate injects external/internal workDir by project, and sub-agents are injected into the parent directory by the main agent). The tool is used by
	// common.WorkDirFromCtx analysis; If not injected, the tool reverts its own config.workDir (NewResolverCache default root).

	// File security policies are injected globally; per-agent is no longer configured separately.
	allowDirs := common.GetDefaultAllowDirs()
	if len(allowDirs) > 0 {
		runCtx = common.WithAllowDirs(runCtx, allowDirs)
	}
	if common.GetDefaultAllowCrossDir() {
		runCtx = common.WithAllowCrossDir(runCtx, true)
	}

	// Step counter injected
	stepCounter := int32(0)
	runCtx = WithStepCounter(runCtx, &stepCounter)

	// Injecting doom-loop detectors (agent-level sharing, cross-tool tightening loops)
	runCtx = WithDoomLoopDetector(runCtx, NewDoomLoopDetector())

	// Obtain the rule chain ID
	chainId := ""
	if ctx.RuleChain() != nil {
		chainId = ctx.RuleChain().GetNodeId().Id
	}

	// Inject Emitter
	runCtx = InjectEmitter(runCtx, chainId)

	// Inject the Aspect Manager
	if x.aspectExecutor != nil {
		runCtx = InjectAspectManager(runCtx, x.aspectExecutor.Manager())
	}

	return runCtx
}

// extractResolvedSystemPrompt extracts parsed system prompt words from the AgentInput message list
func extractResolvedSystemPrompt(adkInput *adk.AgentInput) string {
	for _, m := range adkInput.Messages {
		if m.Role == schema.System {
			return m.Content
		}
	}
	return ""
}

// buildAgentInput: Constructs the facet input
func (x *ReactAgentNode) buildAgentInput(adkInput *adk.AgentInput, msg types.RuleMsg, resolvedSystemPrompt string) *aspect.AgentInput {
	var originalMsgs []*schema.Message
	for _, m := range adkInput.Messages {
		if m.Role != schema.System {
			originalMsgs = append(originalMsgs, m)
		}
	}

	agentInput := &aspect.AgentInput{
		Messages:         adkInput.Messages,
		OriginalMessages: originalMsgs,
		SystemPrompt:     resolvedSystemPrompt,
		Context:          make(map[string]any),
		Metadata:         make(map[string]string),
		SessionKey:       msg.Metadata.GetValue("sessionKey"),
	}

	for k, v := range msg.Metadata.Values() {
		agentInput.Metadata[k] = v
	}

	// Copy from the Extra message field
	for _, m := range adkInput.Messages {
		if m.Extra != nil {
			for k, v := range m.Extra {
				if sv, ok := v.(string); ok {
					agentInput.Metadata[k] = sv
				}
			}
		}
	}

	return agentInput
}

// isStreamMode checks whether it is streaming mode
func (x *ReactAgentNode) isStreamMode(msg types.RuleMsg) bool {
	return msg.Metadata.GetValue(config.KeyStream) == config.ValueTrue
}

// executeSync synchronously executes
func (x *ReactAgentNode) executeSync(ctx types.RuleContext, msg types.RuleMsg, runCtx context.Context, opts ExecuteOptions, agentInput *aspect.AgentInput, adkInput *adk.AgentInput) {
	output, err := x.aspectExecutor.ExecuteSync(runCtx, opts, agentInput, adkInput.Messages, func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		// Injecting session_model into context (for dynamic model switching)
		ctx = InjectSessionModelToContext(ctx, agentInput.Metadata)
		ctx = InjectSessionExtraFieldsToContext(ctx, agentInput.Metadata)
		return x.agent.Generate(ctx, msgs)
	})

	if err != nil {
		ctx.TellFailure(msg, fmt.Errorf("react agent generate failed: %v", err))
		return
	}

	// Processing output
	msg.SetData(output.Content)
	msg.DataType = types.TEXT
	// session_aspect.Before injecting session_model into agentInput.Metadata inside ExecuteSync,
	// Here, (after injection) parses the response model to ensure the model after session-level switching is reflected rather than the node's default model
	responseModel := resolveResponseModel(x.Config.Model, agentInput.Metadata)
	BuildTokenMetadata(msg, output.TokenUsage, responseModel)
	// If the Around Aspect intercepts the request (such as CommandAspect), it passes the metadata output by the face
	if output.SkippedAI {
		transferOutputMetadata(msg, output)
	}
	ctx.TellSuccess(msg)
}

// executeStream streaming
func (x *ReactAgentNode) executeStream(ctx types.RuleContext, msg types.RuleMsg, runCtx context.Context, opts ExecuteOptions, agentInput *aspect.AgentInput, adkInput *adk.AgentInput) {
	// Session-level models can only be parsed after session_aspect injection session_model (injection occurs in the ExecuteStream below).
	// The internal Before phase). Using closure delay parsing, each value is retrieved from the latest agentInput.Metadata,
	// Ensure SSE reflects the model after session-level switching, rather than the node's default model.
	getResponseModel := func() string {
		return resolveResponseModel(x.Config.Model, agentInput.Metadata)
	}
	// Creating SSE processors
	sseHandler := NewSSEHandler(ctx, msg)
	// Unified streaming TellNext queue: both chunk (onChunk) and utility event (sendEvent) are joined,
	// Single goroutine serial TellNext preorder. Bounded capacity: Instantaneous slow consumption is absorbed by buffering, without backloading LLM reads (avoided).
	// "Error in input stream");  When buffer is full (front end inactive), ABORT stops loss upstream flow, without reintroducing backpressure or unlimited memory consumption.
	streamCtx, cancel := context.WithCancel(runCtx)
	defer cancel()
	// Full mode replay is burst traffic (the entire stream is buffered and then instantly surges); when buffering is full, blocking backpressure (waiting for frontend consumption, no data loss).
	// Only after overtime did it abort; The off mode smooths the real-time stream: when full, it immediately aborts without backloading the LLM (avoiding "Error in input stream").
	var tellQueue *StreamTellQueue
	if x.Config.LLMConfig.StreamRetryMode == config.StreamRetryFull {
		tellQueue = NewStreamTellQueueWithBlock(ctx, DefaultStreamTellQueueCap, cancel, DefaultStreamTellBlockTimeout, x.logger)
	} else {
		tellQueue = NewStreamTellQueue(ctx, DefaultStreamTellQueueCap, cancel, x.logger)
	}
	defer tellQueue.Close() // Panic/Exception Catch: Prevents consumption and goroutine leaks (normal paths are closed by Wait, powers do not conflict)
	sseHandler.UseQueue(tellQueue)
	streamCtx = WithSSECallback(streamCtx, sseHandler.Callback())

	output, err := x.aspectExecutor.ExecuteStream(streamCtx, opts, agentInput, adkInput.Messages,
		func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
			// Injecting session_model into context (for dynamic model switching)
			ctx = InjectSessionModelToContext(ctx, agentInput.Metadata)
			ctx = InjectSessionExtraFieldsToContext(ctx, agentInput.Metadata)
			return x.agent.Stream(ctx, msgs)
		},
		func(content, reasoning string, isFirst bool) {
			chunkMsg := msg.Copy()
			chunkMsg.SetData(content)
			if reasoning != "" {
				chunkMsg.Metadata.PutValue(config.KeyReasoningContent, reasoning)
			}
			chunkMsg.DataType = types.TEXT
			BuildStreamChunkMetadataWithModel(chunkMsg, isFirst, getResponseModel())
			tellQueue.Enqueue(chunkMsg)
		},
	)
	// Stream end: Wait until the queue has TellNext all chunk and tool events, then send an end message to ensure order.
	tellQueue.Wait()

	// Buffer full stop-loss: The flow is aborted midway (front end inactivated). ExecuteStream swallows cancel errors (returns nil err),
	// Therefore, you need to separately check Aborted() and send a Failure to let the frontend know it's an interrupt rather than a normal completion—avoid treating truncated content as success.
	if tellQueue.Aborted() {
		endMsg := msg.Copy()
		endMsg.SetData("流式响应中断：前端消费过慢或已断开连接")
		endMsg.DataType = types.TEXT
		BuildStreamEndMetadata(endMsg)
		ctx.TellNext(endMsg, types.Stream, types.Failure)
		return
	}

	if err != nil {
		endMsg := msg.Copy()
		endMsg.SetData(err.Error())
		endMsg.DataType = types.TEXT
		BuildStreamEndMetadata(endMsg)
		ctx.TellNext(endMsg, types.Stream, types.Failure)
		return
	}

	// If the Around Aspect intercepts a request (such as CommandAspect), use a simplified streaming response
	// Send only one end message to avoid multiple response header writes
	if output.SkippedAI {
		// Send a single stream end message (including full content)
		endMsg := msg.Copy()
		endMsg.SetData(output.Content)
		endMsg.DataType = types.TEXT
		BuildStreamEndMetadata(endMsg)
		BuildTokenMetadata(endMsg, output.TokenUsage, getResponseModel())
		// Passing metadata for the facet output (such as the _isCommandResponse set by CommandAspect
		transferOutputMetadata(endMsg, output)
		ctx.TellSuccess(endMsg)
		return
	}

	// Normal AI streaming response process
	// Send a stream end message (including token statistics, so usage is sent before [DONE])
	endMsg := msg.Copy()
	endMsg.SetData("")
	endMsg.DataType = types.TEXT
	BuildStreamEndMetadata(endMsg)
	BuildTokenMetadata(endMsg, output.TokenUsage, getResponseModel())
	ctx.TellNext(endMsg, types.Stream)

	// Send final completion messages (for downstream processing, such as logs)
	finalMsg := msg.Copy()
	finalMsg.SetData(output.Content)
	finalMsg.DataType = types.TEXT
	BuildStreamEndMetadata(finalMsg)
	finalMsg.Metadata.PutValue(config.KeyFullContent, config.ValueTrue)
	BuildTokenMetadata(finalMsg, output.TokenUsage, getResponseModel())
	ctx.TellNext(finalMsg, types.Success)
}

// Destroy the node
func (x *ReactAgentNode) Destroy() {
	// Agents do not require explicit cleanup
}

// skillPromptMarker separates the raw system prompt from the skill prompt word.
// MessageModifier receives cumulative messages (state.Messages are a shallow copy),
// Each round requires extracting the original content from the system message and injecting it into the latest skill list to avoid repeated accumulation.
const skillPromptMarker = "\n<!-- SKILL_LIST -->\n"

// BuildSkillModifier MessageModifier for building skill lists.
// Before each model call, the latest skill list is obtained from DynamicSkillLister and a system prompt is injected.
// Reference eino NewPersonaModifier (react.go:208-216): Create a new slice + a new object without modifying the original message.
func BuildSkillModifier(skillTool aitool.DynamicSkillLister) func(ctx context.Context, input []*schema.Message) []*schema.Message {
	return func(ctx context.Context, input []*schema.Message) []*schema.Message {
		// 1. Get the latest skill list (trigger MultiBackend fingerprint check → hot update)
		skillList, err := skillTool.ListSkills(ctx)
		if err != nil || skillList == "" {
			return input
		}

		// 2. Assemble skill prompts (separate them from the original content with markers)
		skillPrompt := skillPromptMarker + skillTool.GetSkillInstruction() + "\n" + skillList

		// 3. Create new slices without modifying the original message (avoid shallow copy state contamination)
		result := make([]*schema.Message, 0, len(input)+1)
		systemFound := false

		for _, msg := range input {
			if msg.Role == schema.System && !systemFound {
				systemFound = true
				// Extract the original content (remove the skill prompts injected in the previous round), then inject the latest list
				originalContent := ExtractOriginalSystemContent(msg.Content)
				newMsg := schema.SystemMessage(originalContent + skillPrompt)
				result = append(result, newMsg)
			} else {
				result = append(result, msg)
			}
		}

		// 4. If there is no system message, create a new one
		if !systemFound {
			result = append([]*schema.Message{schema.SystemMessage(skillPrompt)}, result...)
		}

		return result
	}
}

// ExtractOriginalSystemContent Extracts the original system prompt (the part before the marker).
// If there is no marker, it indicates the first injection and returns the original content.
func ExtractOriginalSystemContent(content string) string {
	idx := strings.Index(content, skillPromptMarker)
	if idx != -1 {
		return content[:idx]
	}
	return content
}

// resolveResponseModel returns the effective model name used for response metadata.
func resolveResponseModel(defaultModel string, metadata map[string]string) string {
	if metadata == nil {
		return defaultModel
	}
	if sessionModel := strings.TrimSpace(metadata[aspect.MetaSessionModel]); sessionModel != "" {
		return sessionModel
	}
	return defaultModel
}

// transferOutputMetadata passes the metadata output from the section to the message metadata
// Metadata used to pass faceted settings such as CommandAspect (e.g., _isCommandResponse)
func transferOutputMetadata(msg types.RuleMsg, output *aspect.AgentOutput) {
	for k, v := range output.Metadata {
		switch sv := v.(type) {
		case string:
			msg.Metadata.PutValue(k, sv)
		case bool:
			if sv {
				msg.Metadata.PutValue(k, config.ValueTrue)
			} else {
				msg.Metadata.PutValue(k, config.ValueFalse)
			}
		case int:
			msg.Metadata.PutValue(k, fmt.Sprintf("%d", sv))
		case float64:
			msg.Metadata.PutValue(k, fmt.Sprintf("%g", sv))
		default:
			msg.Metadata.PutValue(k, fmt.Sprintf("%v", v))
		}
	}
}
