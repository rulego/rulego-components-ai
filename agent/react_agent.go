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

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/config"
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
	chatModel            model.ToolCallingChatModel // 保存 chatModel 引用，用于动态模型切换
}

// Type 返回组件类型
func (x *ReactAgentNode) Type() string {
	return "ai/agent"
}

// New 创建 ReactAgentNode 实例
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

// applyDefaultLLMParams 应用默认 LLM 参数
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

// Init 初始化节点
func (x *ReactAgentNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	// 1. 解析配置
	err := maps.Map2Struct(configuration, &x.Config)
	if err != nil {
		return err
	}

	x.logger = ruleConfig.Logger

	if x.logger != nil {
		x.logger.Debugf("ReactAgentNode.Init: Model=%s, URL=%s, Key=%s", x.Config.Model, x.Config.Url, maskAPIKey(x.Config.Key))
	}

	x.applyDefaultLLMParams()

	// 2. 初始化模板
	if err := x.initTemplates(); err != nil {
		return err
	}

	// 3. 获取节点信息
	x.initNodeInfo(configuration)

	// 4. 创建聊天模型
	baseChatModel, err := CreateChatModel(x.Config.LLMConfig, ModelOptions{
		Logger:     ruleConfig.Logger,
		WrapRetry:  true,
		MaxRetries: x.Config.MaxRetries,
	})
	if err != nil {
		return fmt.Errorf("failed to create chat model: %v", err)
	}

	// 4.1 包装模型以支持动态模型切换（会话级模型切换）
	chatModel := WrapModelWithDynamicSupport(baseChatModel, x.Config.LLMConfig, ModelOptions{
		Logger:     ruleConfig.Logger,
		WrapRetry:  false, // 不重复包装重试，baseChatModel 已经有重试功能
		MaxRetries: 0,
	})
	x.chatModel = chatModel

	// 5. 初始化切面执行器（必须在 createTools 之前）
	x.aspectExecutor = NewAgentAspectExecutor(ruleConfig.Logger)
	x.tokenTracker = token.NewTokenTracker()
	x.metricsCollector = token.NewMetricsCollector()

	// 6. 创建工具
	tools, toolInfoList, err := x.createTools(ruleConfig, chatModel)
	if err != nil {
		return fmt.Errorf("failed to create tools: %v", err)
	}

	// 7. 创建 React Agent
	maxStep := x.Config.MaxStep
	if maxStep <= 0 {
		maxStep = DefaultMaxStep
	}

	agent, err := CreateReactAgent(context.Background(), chatModel, AgentOptions{
		MaxStep:     maxStep,
		ToolsConfig: buildToolsConfig(tools),
		Logger:      ruleConfig.Logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create react agent: %v", err)
	}

	x.agent = agent
	x.tools = toolInfoList

	return nil
}

// initTemplates 初始化模板
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

// initNodeInfo 初始化节点信息
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

// createTools 创建工具
func (x *ReactAgentNode) createTools(ruleConfig types.Config, chatModel interface{}) ([]tool.BaseTool, []*schema.ToolInfo, error) {
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

// OnMsg 处理消息
func (x *ReactAgentNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	// 1. 转换输入
	adkInput, err := ConvertRuleMsgToAgentInput(ctx, msg, x.systemPromptTemplate, x.hasVar, x.Config.SystemPrompt, x.presetMessagesTmpls, x.Config.Model, x.id, x.logger)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}

	// 2. 构建执行上下文
	runCtx := x.buildRunContext(ctx, msg)

	// 3. 获取规则链 ID
	chainId := ""
	if ctx.RuleChain() != nil {
		chainId = ctx.RuleChain().GetNodeId().Id
	}
	if chainId == "" {
		chainId = x.name
	}

	// 4. 构建切面输入
	resolvedSystemPrompt := extractResolvedSystemPrompt(adkInput)
	agentInput := x.buildAgentInput(adkInput, msg, resolvedSystemPrompt)

	// 5. 根据模式执行
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

// buildRunContext 构建执行上下文
func (x *ReactAgentNode) buildRunContext(ctx types.RuleContext, msg types.RuleMsg) context.Context {
	runCtx := context.WithValue(ctx.GetContext(), config.ShareRuleContextKey, ctx)
	runCtx = context.WithValue(runCtx, config.KeyRuleConfig, ctx.Config())

	// 注入步数计数器
	stepCounter := int32(0)
	runCtx = WithStepCounter(runCtx, &stepCounter)

	// 获取规则链 ID
	chainId := ""
	if ctx.RuleChain() != nil {
		chainId = ctx.RuleChain().GetNodeId().Id
	}

	// 注入 Emitter
	runCtx = InjectEmitter(runCtx, chainId)

	// 注入切面管理器
	if x.aspectExecutor != nil {
		runCtx = InjectAspectManager(runCtx, x.aspectExecutor.Manager())
	}

	return runCtx
}

// extractResolvedSystemPrompt 从 AgentInput 的消息列表中提取已解析的系统提示词
func extractResolvedSystemPrompt(adkInput *adk.AgentInput) string {
	for _, m := range adkInput.Messages {
		if m.Role == schema.System {
			return m.Content
		}
	}
	return ""
}

// buildAgentInput 构建切面输入
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

	// 从消息 Extra 字段复制
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

// isStreamMode 检查是否为流式模式
func (x *ReactAgentNode) isStreamMode(msg types.RuleMsg) bool {
	return msg.Metadata.GetValue(config.KeyStream) == config.ValueTrue
}

// executeSync 同步执行
func (x *ReactAgentNode) executeSync(ctx types.RuleContext, msg types.RuleMsg, runCtx context.Context, opts ExecuteOptions, agentInput *aspect.AgentInput, adkInput *adk.AgentInput) {
	output, err := x.aspectExecutor.ExecuteSync(runCtx, opts, agentInput, adkInput.Messages, func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		// 注入 session_model 到 context（用于动态模型切换）
		ctx = InjectSessionModelToContext(ctx, agentInput.Metadata)
		return x.agent.Generate(ctx, msgs)
	})

	if err != nil {
		ctx.TellFailure(msg, fmt.Errorf("react agent generate failed: %v", err))
		return
	}

	// 处理输出
	msg.SetData(output.Content)
	msg.DataType = types.TEXT
	BuildTokenMetadata(msg, output.TokenUsage, x.Config.Model)
	ctx.TellSuccess(msg)
}

// executeStream 流式执行
func (x *ReactAgentNode) executeStream(ctx types.RuleContext, msg types.RuleMsg, runCtx context.Context, opts ExecuteOptions, agentInput *aspect.AgentInput, adkInput *adk.AgentInput) {
	// 创建 SSE 处理器
	sseHandler := NewSSEHandler(ctx, msg)
	runCtx = WithSSECallback(runCtx, sseHandler.Callback())

	output, err := x.aspectExecutor.ExecuteStream(runCtx, opts, agentInput, adkInput.Messages,
		func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
			// 注入 session_model 到 context（用于动态模型切换）
			ctx = InjectSessionModelToContext(ctx, agentInput.Metadata)
			return x.agent.Stream(ctx, msgs)
		},
		func(chunk string, isFirst bool) {
			chunkMsg := msg.Copy()
			chunkMsg.SetData(chunk)
			chunkMsg.DataType = types.TEXT
			BuildStreamChunkMetadata(chunkMsg, isFirst)
			ctx.TellNext(chunkMsg, types.Stream)
		},
	)

	if err != nil {
		endMsg := msg.Copy()
		endMsg.SetData(err.Error())
		endMsg.DataType = types.TEXT
		BuildStreamEndMetadata(endMsg)
		ctx.TellNext(endMsg, types.Stream, types.Failure)
		return
	}

	// 如果 Around 切面拦截了请求（如 CommandAspect），使用简化的流式响应
	// 只发送一条结束消息，避免多次写入响应头
	if output.SkippedAI {
		// 发送单条流式结束消息（包含完整内容）
		endMsg := msg.Copy()
		endMsg.SetData(output.Content)
		endMsg.DataType = types.TEXT
		BuildStreamEndMetadata(endMsg)
		BuildTokenMetadata(endMsg, output.TokenUsage, x.Config.Model)
		ctx.TellSuccess(endMsg)
		return
	}

	// 正常的 AI 流式响应流程
	// 发送流式结束消息
	endMsg := msg.Copy()
	endMsg.SetData("")
	endMsg.DataType = types.TEXT
	BuildStreamEndMetadata(endMsg)
	ctx.TellNext(endMsg, types.Stream)

	// 发送最终完成消息
	finalMsg := msg.Copy()
	finalMsg.SetData(output.Content)
	finalMsg.DataType = types.TEXT
	BuildStreamEndMetadata(finalMsg)
	finalMsg.Metadata.PutValue(config.KeyFullContent, config.ValueTrue)
	BuildTokenMetadata(finalMsg, output.TokenUsage, x.Config.Model)
	ctx.TellNext(finalMsg, types.Success)
}

// Destroy 销毁节点
func (x *ReactAgentNode) Destroy() {
	// Agent 不需要显式清理
}
