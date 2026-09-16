// agent_node.go ai/agent 的纯标准库轻量实现(与 agent/ 的 eino 版共用类型名)。
//
// 实现只用 net/http + encoding/json,不经 eino→sonic,32 位平台可编译。
// 同 binary 同时引入 agent 与本包时先注册者生效(all 捆绑包固定 eino 版优先),
// 本包单独引入(或 eino 不可用的 32 位构建)时顶上同名。
//
// 工具来源:RuleConfig UDF 里的 types.MCPToolProvider(注册 key 即
// types.MCPToolProviderKey);宿主不注入则纯对话。skillsDir 非空时追加
// 内置 skill 工具(SKILL.md 读取)并把技能清单注入 systemPrompt。
//
// 原生 OpenAI delta 协议中 tool_calls 是一等增量(按 index 累积),不存在
// "先叙述后工具调用被误路由"一类流转换问题。
package lite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/utils/el"
	"github.com/rulego/rulego/utils/maps"

	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego-components-ai/utils/doomloop"
	"github.com/rulego/rulego-components-ai/utils/token"
)

// NodeType 节点类型标识,与 eino 版 agent 共用同名。同 binary 同时引入
// 两包时先注册者生效;eino 版缺席的构建(32 位等)由本实现顶上。
const NodeType = "ai/agent"

// 默认值,与 eino 版保持一致(同名组件相互替换,已实现字段的缺省行为不得分叉):
// maxStep=agent.DefaultMaxStep、工具输出截断=按 rune、temperature/topP=
// config.DefaultTemperature/DefaultTopP(零值视为未设置,套默认)。
const (
	defaultMaxStep       = 50
	defaultMaxToolOutput = 50000
	skillToolName        = "skill"
)

// 采样参数默认值与 config.DefaultTemperature/DefaultTopP 同值;不直接引用那两个
// float32 常量——转 float64 会把 0.7 变成 0.699999988… 进请求体。
const (
	defaultTemperature = 0.7
	defaultTopP        = 0.9
)

func init() {
	// eino 版同名组件在场时注册失败(已存在),本包让位。
	_ = rulego.Registry.Register(&AgentLiteNode{})
}

// AgentLiteConfig 节点配置(模板 DSL 的 configuration)。
// url/key/model/systemPrompt/maxStep/maxToolOutputLength/maxRetries/images/params
// 与 eino 版 ChatAgentConfig 同名同义;skillsDir/tools/skills 为 lite 特有。
type AgentLiteConfig struct {
	Url                 string   `json:"url"`
	Key                 string   `json:"key"`
	Model               string   `json:"model"`
	MaxStep             int      `json:"maxStep"`
	MaxToolOutputLength int      `json:"maxToolOutputLength"`
	MaxRetries          int      `json:"maxRetries"`
	SystemPrompt        string   `json:"systemPrompt"`
	Images              []string `json:"images"`
	SkillsDir           string   `json:"skillsDir"`
	// Tools 工具名允许列表:空/缺省=不过滤(provider 全量);非空=只保留列表内的
	// provider 工具,且列表外工具即使被模型点名调用也拒绝执行。skill 工具不受此
	// 约束,由 skills/skillsDir 决定。字符串条目是两实现通用写法(eino 版按名
	// 解析);其对象描述符格式本实现不承接,Init 显式报错。
	Tools []string `json:"tools,omitempty"`
	// Skills 技能名允许列表:空/缺省=skillsDir 下全部启用技能;非空=只载入列表内
	// 技能(未勾选的不进 system prompt,skill 工具也读不到)。与 Tools 同语义。
	Skills []string `json:"skills,omitempty"`
	// Failover 备用模型端点:主端点重试耗尽后按序切换,每端点独立熔断冷却;
	// 端点 model 为空时沿用主 model。eino 版端点的 params 覆盖不承接。
	Failover []FailoverEndpoint `json:"failover,omitempty"`
	// CircuitCooldownSec 熔断基础冷却秒数,0=默认 60;持续失败逐次翻倍封顶 10 分钟。
	CircuitCooldownSec int `json:"circuitCooldownSec,omitempty"`
	Params             struct {
		Temperature float64 `json:"temperature"`
		TopP        float64 `json:"topP"`
		MaxTokens   int     `json:"maxTokens"`
	} `json:"params"`
}

// failoverTpl 备用端点的模板三元组。
type failoverTpl struct {
	url, key, model el.Template
}

// AgentLiteNode ReAct 智能体节点。
type AgentLiteNode struct {
	config       AgentLiteConfig
	urlTpl       el.Template
	keyTpl       el.Template
	modelTpl     el.Template
	skillsDirTpl el.Template
	promptTpl    el.Template
	failoverTpls []failoverTpl
	router       *chatRouter
	skills       []skillEntry
	provider     types.MCPToolProvider
	maxStep      int
	maxToolOut   int
	tracker      *token.TokenTracker
}

func (n *AgentLiteNode) New() types.Node { return &AgentLiteNode{} }
func (n *AgentLiteNode) Type() string    { return NodeType }
func (n *AgentLiteNode) Destroy()        {}

func (n *AgentLiteNode) Init(rc types.Config, cfg types.Configuration) error {
	if err := checkToolsShape(cfg); err != nil {
		return err
	}
	if err := maps.Map2Struct(cfg, &n.config); err != nil {
		return err
	}
	n.maxStep = n.config.MaxStep
	if n.maxStep <= 0 {
		n.maxStep = defaultMaxStep
	}
	n.maxToolOut = n.config.MaxToolOutputLength
	if n.maxToolOut <= 0 {
		n.maxToolOut = defaultMaxToolOutput
	}
	// 采样参数零值套默认,与 eino 版 applyDefaultLLMParams 一致
	//(需要确定性输出请用 0.001 一类的极小值,0 会被默认值覆盖)。
	if n.config.Params.Temperature == 0 {
		n.config.Params.Temperature = defaultTemperature
	}
	if n.config.Params.TopP == 0 {
		n.config.Params.TopP = defaultTopP
	}
	n.tracker = token.NewTokenTracker()
	n.router = newChatRouter(breakerCooldown(n.config.CircuitCooldownSec))

	// ${global.*} 解析:env 即引擎 Properties(initRuleConfig 从配置 Global 注入)。
	env := map[string]any{}
	if rc.Properties != nil {
		env["global"] = rc.Properties.Values()
	}
	if err := parseTpl([]tplPair{
		{n.config.Url, &n.urlTpl},
		{n.config.Key, &n.keyTpl},
		{n.config.Model, &n.modelTpl},
		{n.config.SkillsDir, &n.skillsDirTpl},
	}); err != nil {
		return err
	}
	for _, f := range n.config.Failover {
		var ft failoverTpl
		if err := parseTpl([]tplPair{
			{f.Url, &ft.url},
			{f.Key, &ft.key},
			{f.Model, &ft.model},
		}); err != nil {
			return err
		}
		n.failoverTpls = append(n.failoverTpls, ft)
	}
	if n.config.SystemPrompt != "" {
		tpl, err := el.NewTemplate(n.config.SystemPrompt)
		if err != nil {
			return fmt.Errorf("%s: systemPrompt template: %w", NodeType, err)
		}
		n.promptTpl = tpl
	}

	if t := n.skillsDirTpl; t != nil {
		if d := t.ExecuteAsString(env); d != "" {
			n.skills = filterSkills(loadSkills(d), n.config.Skills)
		}
	}
	if p, ok := rc.GetUdf(types.MCPToolProviderKey, "").(types.MCPToolProvider); ok {
		n.provider = p
	}
	return nil
}

// tplPair 待解析的模板配置对(raw 为空跳过)。
type tplPair struct {
	raw string
	dst *el.Template
}

// checkToolsShape tools 为对象数组(eino 版工具描述符格式)时给出可读错误,
// 替代 mapstructure 的类型转换报错——同名替换场景下这是最常见的迁移坑;
// 字符串条目两边通用,无需改写。
func checkToolsShape(cfg types.Configuration) error {
	raw, ok := cfg["tools"]
	if !ok {
		return nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	for _, item := range list {
		if _, isObj := item.(map[string]interface{}); isObj {
			return fmt.Errorf("%s: tools 为 eino 版工具描述符格式(对象数组),本实现只支持工具名允许列表(字符串数组,两实现通用写法);对象描述符请改写为工具名,或经宿主工具提供者组织,见 agent/lite 包文档", NodeType)
		}
	}
	return nil
}

// parseTpl 解析一组模板,失败时报出节点类型与原文。
func parseTpl(pairs []tplPair) error {
	for _, t := range pairs {
		if t.raw == "" {
			continue
		}
		tpl, err := el.NewTemplate(t.raw)
		if err != nil {
			return fmt.Errorf("%s: template %q: %w", NodeType, t.raw, err)
		}
		*t.dst = tpl
	}
	return nil
}

// tplString nil 安全的模板执行(nil 模板返回空串)。
func tplString(t el.Template, env map[string]any) string {
	if t == nil {
		return ""
	}
	return t.ExecuteAsString(env)
}

// toolDefs 汇总工具定义:provider 工具(按 Tools 允许列表过滤)+ skill 工具。
// 宿主未注入 provider 且无技能时返回空——节点退化为纯对话。
func (n *AgentLiteNode) toolDefs() []types.MCPToolDefinition {
	var defs []types.MCPToolDefinition
	if n.provider != nil {
		if list, err := n.provider.ListToolDefinitions(); err == nil {
			defs = append(defs, filterTools(list, n.config.Tools)...)
		}
	}
	if len(n.skills) > 0 {
		defs = append(defs, types.MCPToolDefinition{
			Name:        skillToolName,
			Description: "读取领域技能/操作规程全文。回答某领域问题前若下方可用技能列表里有相关技能,先调用本工具获取完整流程,再按流程执行。",
			InputSchema: []byte(`{"type":"object","properties":{"name":{"type":"string","description":"技能名"}},"required":["name"]}`),
		})
	}
	return defs
}

// filterTools 按允许列表过滤 provider 工具;空列表=不过滤。
func filterTools(defs []types.MCPToolDefinition, allow []string) []types.MCPToolDefinition {
	if len(allow) == 0 {
		return defs
	}
	set := make(map[string]bool, len(allow))
	for _, name := range allow {
		set[name] = true
	}
	out := make([]types.MCPToolDefinition, 0, len(defs))
	for _, d := range defs {
		if set[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// filterSkills 按允许列表过滤技能;空列表=不过滤(全部启用技能)。
func filterSkills(defs []skillEntry, allow []string) []skillEntry {
	if len(allow) == 0 {
		return defs
	}
	set := make(map[string]bool, len(allow))
	for _, name := range allow {
		set[name] = true
	}
	out := make([]skillEntry, 0, len(defs))
	for _, d := range defs {
		if set[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// toOpenAITools 转成 OpenAI tools 数组。
func toOpenAITools(defs []types.MCPToolDefinition) []Tool {
	out := make([]Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  json.RawMessage(d.InputSchema),
			},
		})
	}
	return out
}

func (n *AgentLiteNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	runCtx := context.Background()
	if c := ctx.GetContext(); c != nil {
		runCtx = c
	}
	env := ctx.GetEnv(msg, true)
	model := tplString(n.modelTpl, env)
	if model == "" {
		ctx.TellFailure(msg, fmt.Errorf("%s: 对话模型未配置:请设置 model 或对应全局变量后重试", NodeType))
		return
	}

	// 解析 OpenAI chat 请求体。
	var req struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(msg.GetData()), &req); err != nil {
		ctx.TellFailure(msg, fmt.Errorf("请求体不是合法的 chat completions JSON: %w", err))
		return
	}

	systemPrompt := appendSkillSection(tplString(n.promptTpl, env), n.skills)
	stream := msg.GetMetadata().GetValue("stream") == "true"
	chat := New(tplString(n.urlTpl, env), tplString(n.keyTpl, env))
	chat.SetMaxRetries(n.config.MaxRetries)
	eps := []chatEndpoint{{client: chat}}
	for _, ft := range n.failoverTpls {
		if url := tplString(ft.url, env); url != "" {
			c := New(url, tplString(ft.key, env))
			c.SetMaxRetries(n.config.MaxRetries)
			eps = append(eps, chatEndpoint{client: c, model: tplString(ft.model, env)})
		}
	}
	tools := toOpenAITools(n.toolDefs())

	messages := make([]Message, 0, len(req.Messages)+8)
	if systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, req.Messages...)
	messages = applyPresetImages(messages, resolveImageRefs(n.config.Images))

	n.loop(ctx, msg, runCtx, eps, model, tools, messages, stream, doomloop.NewDoomLoopDetector())
}

// resolveImageRefs 本地文件路径转为 data: base64 URL(http(s)/data: 引用原样)。
// 与 eino 版 images 的输入口径一致(本地文件可用);eino 额外的压缩重编码
// 与落盘给工具引用的管线不承接。读不到的路径原样透传,由服务端报错。
func resolveImageRefs(images []string) []string {
	if len(images) == 0 {
		return images
	}
	out := make([]string, len(images))
	for i, img := range images {
		out[i] = resolveImageRef(img)
	}
	return out
}

func resolveImageRef(img string) string {
	if strings.HasPrefix(img, "http://") || strings.HasPrefix(img, "https://") ||
		strings.HasPrefix(img, "data:") {
		return img
	}
	data, err := os.ReadFile(img)
	if err != nil {
		return img
	}
	mimeType := http.DetectContentType(data[:min(len(data), 512)])
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// applyPresetImages 预置图片(配置 images)以 image_url parts 追加到末条 user 消息;
// 消息已有 parts(入参自带多模态)则并入其后。无 user 消息时忽略。
func applyPresetImages(messages []Message, images []string) []Message {
	if len(images) == 0 {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		parts := messages[i].ContentParts
		if len(parts) == 0 && messages[i].Content != "" {
			parts = []ContentPart{TextPart(messages[i].Content)}
		}
		for _, img := range images {
			parts = append(parts, ImagePart(img))
		}
		messages[i].ContentParts = parts
		return messages
	}
	return messages
}

// loop ReAct 主循环:LLM → 工具 → LLM … 直到无工具调用或步数耗尽。
// detector 为本次请求私有(会话级),跨轮次累积 doom 历史;
// eps 为容灾端点序列(主端点在前),LLM 调用统一走 n.router。
func (n *AgentLiteNode) loop(ctx types.RuleContext, msg types.RuleMsg, runCtx context.Context,
	eps []chatEndpoint, model string, tools []Tool, messages []Message, stream bool, detector *doomloop.DoomLoopDetector) {

	req := Request{
		Model:    model,
		Messages: messages,
		Tools:    tools,
		Params: Params{
			Temperature: n.config.Params.Temperature,
			TopP:        n.config.Params.TopP,
			MaxTokens:   n.config.Params.MaxTokens,
		},
	}

	for step := 0; step < n.maxStep; step++ {
		// 每轮重挂最新消息列表:上一轮的 assistant(tool_calls) 与 role:tool 已追加,
		// 构造在循环外会复用陈旧消息。
		req.Messages = messages
		if stream {
			content, calls, usage, err := n.streamTurn(ctx, msg, runCtx, eps, req)
			if err != nil {
				ctx.TellFailure(msg, err)
				return
			}
			n.tracker.Record(usage.PromptTokens, usage.CompletionTokens)
			if len(calls) == 0 {
				n.finishStream(ctx, msg, content, model, &usage)
				return
			}
			// 记录本轮 assistant(带工具调用),执行工具,继续下一轮。
			messages = append(messages, Message{Role: "assistant", Content: content, ToolCalls: calls})
			messages = n.execTools(ctx, msg, calls, messages, true, detector)
			continue
		}

		resp, err := n.router.Complete(runCtx, eps, req)
		if err != nil {
			ctx.TellFailure(msg, err)
			return
		}
		n.tracker.Record(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		choice := resp.Choices[0]
		if len(choice.Message.ToolCalls) == 0 {
			final := msg.Copy()
			final.SetData(choice.Message.Content)
			n.putUsage(final, &resp.Usage, model)
			ctx.TellSuccess(final)
			return
		}
		messages = append(messages, Message{Role: "assistant", Content: choice.Message.Content, ToolCalls: choice.Message.ToolCalls})
		messages = n.execTools(ctx, msg, choice.Message.ToolCalls, messages, false, detector)
	}
	// 步数耗尽:如实收尾,不假装成功。
	exhausted := "已达到最大工具调用轮次,未能给出最终回答;请缩小问题范围后重试。"
	if stream {
		n.finishStream(ctx, msg, exhausted, model, nil)
		return
	}
	final := msg.Copy()
	final.SetData(exhausted)
	ctx.TellSuccess(final)
}

// streamTurn 消费一轮流式响应:正文/思考 delta 逐帧转发,工具调用增量累积。
// 返回本轮完整正文、合并后的工具调用与 usage。
func (n *AgentLiteNode) streamTurn(ctx types.RuleContext, msg types.RuleMsg, runCtx context.Context,
	eps []chatEndpoint, req Request) (string, []ToolCall, Usage, error) {

	var content strings.Builder
	acc := newToolCallAccumulator()

	finish, usage, err := n.router.Stream(runCtx, eps, req, func(d StreamDelta) error {
		if len(d.ToolCalls) > 0 {
			acc.add(d.ToolCalls)
		}
		if d.ReasoningContent != "" || d.Content != "" {
			content.WriteString(d.Content)
			frame := msg.Copy()
			frame.SetData(d.Content)
			frame.Metadata.PutValue(config.KeyChunk, config.ValueTrue)
			if d.ReasoningContent != "" {
				frame.Metadata.PutValue(config.KeyReasoningContent, d.ReasoningContent)
			}
			ctx.TellNext(frame, types.Stream)
		}
		return nil
	})
	if err != nil {
		return "", nil, usage, err
	}
	_ = finish
	return content.String(), acc.result(), usage, nil
}

// execTools 逐个执行工具调用并追加 role:tool 消息。stream=true 时额外推送
// AGUI 过程帧(START/RESULT/ERROR);非流式静默执行,绝不发任何流式帧
// (否则污染非 SSE 响应导致客户端解析失败)。
func (n *AgentLiteNode) execTools(ctx types.RuleContext, msg types.RuleMsg,
	calls []ToolCall, messages []Message, stream bool, detector *doomloop.DoomLoopDetector) []Message {

	for _, call := range calls {
		result := n.callTool(ctx, msg, call, stream, detector)
		messages = append(messages, Message{Role: "tool", ToolCallID: call.ID, Content: result})
	}
	return messages
}

// callTool 执行单个工具并返回给 LLM 的结果文本。doom 检测:BeforeCall 的
// 重复警告前缀进结果、AfterCall 的连续失败警告追加在尾部,提示 LLM 改换调用方式。
func (n *AgentLiteNode) callTool(ctx types.RuleContext, msg types.RuleMsg, call ToolCall,
	stream bool, detector *doomloop.DoomLoopDetector) string {

	name := call.Function.Name
	args := call.Function.Arguments
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	if stream {
		n.toolFrame(ctx, msg, "TOOL_CALL_START", call.ID, name, args, "")
	}

	warn := detector.BeforeCall(name, args)
	out, failed := n.runTool(ctx, name, args)
	if failed {
		if stream {
			n.toolFrame(ctx, msg, "TOOL_CALL_ERROR", call.ID, name, "", out)
		}
	} else if stream {
		n.toolFrame(ctx, msg, "TOOL_CALL_RESULT", call.ID, name, "", out)
	}

	if afterWarn := detector.AfterCall(name, args, failed); afterWarn != "" {
		out += "\n" + afterWarn
	}
	if warn != "" {
		out = warn + "\n" + out
	}
	return out
}

// runTool 执行工具并截断过长输出;failed=true 时 out 为错误文本。
func (n *AgentLiteNode) runTool(ctx types.RuleContext, name, args string) (out string, failed bool) {
	var argsMap map[string]interface{}
	if err := json.Unmarshal([]byte(args), &argsMap); err != nil {
		return fmt.Sprintf("工具参数不是合法 JSON: %v", err), true
	}
	result, err := n.invoke(ctx, name, argsMap)
	if err != nil {
		return err.Error(), true
	}
	// 按 rune 截断避免劈开 UTF-8 字符,后缀格式与 eino 版 truncateResult 一致。
	runes := []rune(result)
	if len(runes) > n.maxToolOut {
		result = string(runes[:n.maxToolOut]) + fmt.Sprintf("...(truncated, original: %d bytes)", len(result))
	}
	return result, false
}

func (n *AgentLiteNode) invoke(ctx types.RuleContext, name string, args map[string]interface{}) (string, error) {
	if name == skillToolName {
		return n.callSkill(args)
	}
	// 允许列表非空时执行层二次把关:模型点名列表外工具(幻觉/被诱导)不触达 provider。
	if !n.toolAllowed(name) {
		return "", fmt.Errorf("工具 %s 未开放给本智能体(不在工具允许列表),已拒绝执行;可用工具: %s",
			name, strings.Join(n.config.Tools, ", "))
	}
	if n.provider == nil {
		return "", fmt.Errorf("工具 %s 不可用:工具提供者未注册", name)
	}
	// 链上下文透传给工具提供者:宿主经 types.WithContext 注入的调用方身份等
	// 值(如 edge 的员工 id)在 provider 侧可取;无链上下文时退回 Background。
	toolCtx := context.Background()
	if ctx != nil {
		if c := ctx.GetContext(); c != nil {
			toolCtx = c
		}
	}
	return n.provider.CallTool(toolCtx, name, args)
}

// toolAllowed 允许列表为空=全量开放;非空=仅列表内工具。
func (n *AgentLiteNode) toolAllowed(name string) bool {
	if len(n.config.Tools) == 0 {
		return true
	}
	for _, t := range n.config.Tools {
		if t == name {
			return true
		}
	}
	return false
}

func (n *AgentLiteNode) callSkill(args map[string]interface{}) (string, error) {
	name, _ := args["name"].(string)
	for _, s := range n.skills {
		if s.Name == name {
			return s.Content, nil
		}
	}
	return "", fmt.Errorf("技能 %q 不存在,可用: %s", name, n.skillNames())
}

// toolFrame 推送一个 AGUI 工具过程帧(metadata tool_call=true,data=事件 JSON)。
func (n *AgentLiteNode) toolFrame(ctx types.RuleContext, msg types.RuleMsg, event, id, name, args, content string) {
	payload := map[string]string{
		"type":         event,
		"toolCallId":   id,
		"toolCallName": name,
	}
	if event == "TOOL_CALL_START" {
		payload["arguments"] = args
	} else {
		payload["content"] = content
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	frame := msg.Copy()
	frame.SetData(string(data))
	frame.Metadata.PutValue(config.KeyChunk, config.ValueTrue)
	frame.Metadata.PutValue(config.KeyToolCall, config.ValueTrue)
	ctx.TellNext(frame, types.Stream)
}

// finishStream 收尾流式回合:end 帧 data 恒为空(正文已按 delta 发出,再带
// 全文会 SSE 重复),full_content 的 Success 消息携带全文供运行日志/下游消费。
func (n *AgentLiteNode) finishStream(ctx types.RuleContext, msg types.RuleMsg, content, model string, usage *Usage) {
	end := msg.Copy()
	end.SetData("")
	end.Metadata.PutValue(config.KeyStreamCompleted, config.ValueTrue)
	n.putUsage(end, usage, model)
	ctx.TellNext(end, types.Stream)

	full := msg.Copy()
	full.SetData(content)
	full.Metadata.PutValue(config.KeyFullContent, config.ValueTrue)
	n.putUsage(full, usage, model)
	ctx.TellSuccess(full)
}

// putUsage 把本轮 LLM 响应的 usage 累加写回 metadata(多轮 ReAct 每轮各调,
// 覆盖会让上游只读到最后一轮);model 仍为覆盖(同一链内不变)。
// 节点级 tracker 另行累计会话总用量,不依赖本方法。
func (n *AgentLiteNode) putUsage(m types.RuleMsg, u *Usage, model string) {
	if model != "" {
		m.Metadata.PutValue("model", model)
	}
	if u == nil {
		return
	}
	putAccum(m, config.KeyPromptTokens, u.PromptTokens)
	putAccum(m, config.KeyCompletionTokens, u.CompletionTokens)
	putAccum(m, config.KeyTotalTokens, u.TotalTokens)
}

// putAccum 读旧值累加后写回;旧值解析失败按 0(不因脏数据丢本轮用量)。
func putAccum(m types.RuleMsg, key string, add int) {
	old, _ := strconv.Atoi(m.Metadata.GetValue(key))
	m.Metadata.PutValue(key, fmt.Sprint(old+add))
}

func (n *AgentLiteNode) skillNames() string {
	names := make([]string, 0, len(n.skills))
	for _, s := range n.skills {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

// ---- 工具调用增量累积 ----

// toolCallAccumulator 按 index 合并流式 tool_calls 增量(id/name 首帧出现,
// arguments 逐帧追加;不带 index 的实现按出现顺序)。
type toolCallAccumulator struct {
	order []int
	items map[int]*accItem
}

type accItem struct {
	id, name string
	args     strings.Builder
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{items: map[int]*accItem{}}
}

func (a *toolCallAccumulator) add(tcs []ToolCallDelta) {
	for _, tc := range tcs {
		idx := tc.Index
		if idx < 0 {
			idx = len(a.order)
		}
		it, ok := a.items[idx]
		if !ok {
			it = &accItem{}
			a.items[idx] = it
			a.order = append(a.order, idx)
		}
		if tc.ID != "" {
			it.id = tc.ID
		}
		if tc.Name != "" {
			it.name = tc.Name
		}
		it.args.WriteString(tc.Arguments)
	}
}

func (a *toolCallAccumulator) result() []ToolCall {
	idx := append([]int(nil), a.order...)
	sort.Ints(idx)
	out := make([]ToolCall, 0, len(idx))
	for _, i := range idx {
		it := a.items[i]
		out = append(out, ToolCall{
			ID:       it.id,
			Type:     "function",
			Function: FuncCall{Name: it.name, Arguments: it.args.String()},
		})
	}
	return out
}
