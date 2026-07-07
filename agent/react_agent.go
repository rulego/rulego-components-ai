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

// applyDefaultLLMParams 应用默认 LLM 参数。
// 注意：Temperature 等参数的零值（0）会被覆盖为默认值。
// 如需确定性输出（即 Temperature 真正为 0），请使用极小值如 0.001。
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
	// 会话切换创建的新模型也需要重试包装
	chatModel := WrapModelWithDynamicSupport(baseChatModel, x.Config.LLMConfig, ModelOptions{
		Logger:     ruleConfig.Logger,
		WrapRetry:  true,
		MaxRetries: x.Config.MaxRetries,
	})
	x.chatModel = chatModel

	// 5. 初始化切面执行器（必须在 createTools 之前）
	x.aspectExecutor = NewAgentAspectExecutor(ruleConfig.Logger)
	x.tokenTracker = token.NewTokenTracker()
	x.metricsCollector = token.NewMetricsCollector()

	// 6. 创建工具（skillLister 在包装前提取，避免 VisualToolWrapper 遮蔽接口）
	tools, toolInfoList, skillLister, err := x.createTools(ruleConfig, chatModel)
	if err != nil {
		return fmt.Errorf("failed to create tools: %v", err)
	}

	// 7. 构建技能列表的 MessageModifier
	var messageModifier func(ctx context.Context, input []*schema.Message) []*schema.Message
	if skillLister != nil {
		messageModifier = BuildSkillModifier(skillLister)
	}
	// 8. 创建 React Agent
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

	// workDir 不在 agent 节点声明：统一由业务层经 common.WithWorkDir 按需注入 ctx（如
	// triggerGenerate 按项目注入外部/内部 workDir、主 agent 派子 agent 注入父目录），工具按
	// common.WorkDirFromCtx 解析；未注入时工具回退自身 config.workDir（NewResolverCache 默认根）。

	// 文件安全策略由全局注入；per-agent 不再单独配置。
	allowDirs := common.GetDefaultAllowDirs()
	if len(allowDirs) > 0 {
		runCtx = common.WithAllowDirs(runCtx, allowDirs)
	}
	if common.GetDefaultAllowCrossDir() {
		runCtx = common.WithAllowCrossDir(runCtx, true)
	}

	// 注入步数计数器
	stepCounter := int32(0)
	runCtx = WithStepCounter(runCtx, &stepCounter)

	// 注入 doom-loop 检测器（agent 级共享，跨工具抓死循环）
	runCtx = WithDoomLoopDetector(runCtx, NewDoomLoopDetector())

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
		ctx = InjectSessionExtraFieldsToContext(ctx, agentInput.Metadata)
		return x.agent.Generate(ctx, msgs)
	})

	if err != nil {
		ctx.TellFailure(msg, fmt.Errorf("react agent generate failed: %v", err))
		return
	}

	// 处理输出
	msg.SetData(output.Content)
	msg.DataType = types.TEXT
	// session_aspect.Before 已在 ExecuteSync 内注入 session_model 到 agentInput.Metadata，
	// 此处（注入后）解析响应模型，确保回显会话级切换后的模型而非节点默认模型
	responseModel := resolveResponseModel(x.Config.Model, agentInput.Metadata)
	BuildTokenMetadata(msg, output.TokenUsage, responseModel)
	// 如果 Around 切面拦截了请求（如 CommandAspect），传递切面输出的元数据
	if output.SkippedAI {
		transferOutputMetadata(msg, output)
	}
	ctx.TellSuccess(msg)
}

// executeStream 流式执行
func (x *ReactAgentNode) executeStream(ctx types.RuleContext, msg types.RuleMsg, runCtx context.Context, opts ExecuteOptions, agentInput *aspect.AgentInput, adkInput *adk.AgentInput) {
	// 会话级模型需在 session_aspect 注入 session_model 后才能解析（注入发生在下方 ExecuteStream
	// 内部的 Before 阶段）。用闭包延迟解析，每次取值读最新的 agentInput.Metadata，
	// 确保 SSE 回显会话级切换后的模型而非节点默认模型。
	getResponseModel := func() string {
		return resolveResponseModel(x.Config.Model, agentInput.Metadata)
	}
	// 创建 SSE 处理器
	sseHandler := NewSSEHandler(ctx, msg)
	// 统一流式 TellNext 队列：chunk（onChunk）和工具事件（sendEvent）都入队，
	// 单 goroutine 串行 TellNext 保序。有界容量：瞬时慢消费由缓冲吸收，不反压 LLM 读取（避免
	// "Error in input stream"）；缓冲满（前端失活）则 abort 上游流止损，不重新引入背压也不无限吃内存。
	streamCtx, cancel := context.WithCancel(runCtx)
	defer cancel()
	// full 模式重放是突发流量（整条流缓冲后瞬间涌出）：缓冲满时阻塞反压（等前端消费，不丢数据），
	// 超时才 abort；off 模式是平滑实时流：满则立即 abort，不反压 LLM（避免 "Error in input stream"）。
	var tellQueue *StreamTellQueue
	if x.Config.LLMConfig.StreamRetryMode == config.StreamRetryFull {
		tellQueue = NewStreamTellQueueWithBlock(ctx, DefaultStreamTellQueueCap, cancel, DefaultStreamTellBlockTimeout, x.logger)
	} else {
		tellQueue = NewStreamTellQueue(ctx, DefaultStreamTellQueueCap, cancel, x.logger)
	}
	defer tellQueue.Close() // panic/异常兜底：防消费 goroutine 泄漏（正常路径由 Wait 关闭，幂等不冲突）
	sseHandler.UseQueue(tellQueue)
	streamCtx = WithSSECallback(streamCtx, sseHandler.Callback())

	output, err := x.aspectExecutor.ExecuteStream(streamCtx, opts, agentInput, adkInput.Messages,
		func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
			// 注入 session_model 到 context（用于动态模型切换）
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
	// 流结束：等队列把 chunk/工具事件全部 TellNext 完，再发结束消息，保证顺序。
	tellQueue.Wait()

	// 缓冲满止损：流被中途 abort（前端失活）。ExecuteStream 会吞掉 cancel 错误（返回 nil err），
	// 故需单独检查 Aborted()，发 Failure 让前端知道是中断而非正常完成——避免把截断内容当成功。
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

	// 如果 Around 切面拦截了请求（如 CommandAspect），使用简化的流式响应
	// 只发送一条结束消息，避免多次写入响应头
	if output.SkippedAI {
		// 发送单条流式结束消息（包含完整内容）
		endMsg := msg.Copy()
		endMsg.SetData(output.Content)
		endMsg.DataType = types.TEXT
		BuildStreamEndMetadata(endMsg)
		BuildTokenMetadata(endMsg, output.TokenUsage, getResponseModel())
		// 传递切面输出的元数据（如 CommandAspect 设置的 _isCommandResponse）
		transferOutputMetadata(endMsg, output)
		ctx.TellSuccess(endMsg)
		return
	}

	// 正常的 AI 流式响应流程
	// 发送流式结束消息（包含 token 统计，这样 [DONE] 前会发送 usage）
	endMsg := msg.Copy()
	endMsg.SetData("")
	endMsg.DataType = types.TEXT
	BuildStreamEndMetadata(endMsg)
	BuildTokenMetadata(endMsg, output.TokenUsage, getResponseModel())
	ctx.TellNext(endMsg, types.Stream)

	// 发送最终完成消息（用于下游处理，如日志）
	finalMsg := msg.Copy()
	finalMsg.SetData(output.Content)
	finalMsg.DataType = types.TEXT
	BuildStreamEndMetadata(finalMsg)
	finalMsg.Metadata.PutValue(config.KeyFullContent, config.ValueTrue)
	BuildTokenMetadata(finalMsg, output.TokenUsage, getResponseModel())
	ctx.TellNext(finalMsg, types.Success)
}

// Destroy 销毁节点
func (x *ReactAgentNode) Destroy() {
	// Agent 不需要显式清理
}

// skillPromptMarker 将原始 system prompt 与技能提示词分隔开。
// MessageModifier 接收的是累积消息（state.Messages 浅拷贝），
// 每轮需要从 system message 中提取原始内容再注入最新技能列表，避免重复累积。
const skillPromptMarker = "\n<!-- SKILL_LIST -->\n"

// BuildSkillModifier 构建技能列表的 MessageModifier。
// 每次模型调用前，从 DynamicSkillLister 获取最新技能列表并注入 system prompt。
// 参考 eino NewPersonaModifier（react.go:208-216）：创建新切片 + 新对象，不修改原始消息。
func BuildSkillModifier(skillTool aitool.DynamicSkillLister) func(ctx context.Context, input []*schema.Message) []*schema.Message {
	return func(ctx context.Context, input []*schema.Message) []*schema.Message {
		// 1. 获取最新技能列表（触发 MultiBackend 指纹检查 → 热更新）
		skillList, err := skillTool.ListSkills(ctx)
		if err != nil || skillList == "" {
			return input
		}

		// 2. 组装技能提示词（用 marker 与原始内容分隔）
		skillPrompt := skillPromptMarker + skillTool.GetSkillInstruction() + "\n" + skillList

		// 3. 创建新切片，不修改原始消息（避免浅拷贝状态污染）
		result := make([]*schema.Message, 0, len(input)+1)
		systemFound := false

		for _, msg := range input {
			if msg.Role == schema.System && !systemFound {
				systemFound = true
				// 提取原始内容（去掉上轮注入的技能提示词），再注入最新列表
				originalContent := ExtractOriginalSystemContent(msg.Content)
				newMsg := schema.SystemMessage(originalContent + skillPrompt)
				result = append(result, newMsg)
			} else {
				result = append(result, msg)
			}
		}

		// 4. 没有 system message，创建新的
		if !systemFound {
			result = append([]*schema.Message{schema.SystemMessage(skillPrompt)}, result...)
		}

		return result
	}
}

// ExtractOriginalSystemContent 提取原始 system prompt（marker 之前的部分）。
// 如果没有 marker，说明是首次注入，返回原始内容。
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

// transferOutputMetadata 将切面输出的元数据传递到消息元数据
// 用于传递 CommandAspect 等切面设置的元数据（如 _isCommandResponse）
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
