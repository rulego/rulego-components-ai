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

// Package jev 提供 ai/jev 与 ai/jevFilter 两个节点，接入 TypeSafe AI 的
// System One 模型（Jev）：不生成文本，输出带校准置信度的类型化决策。
//
// ai/jev 问一个问题，按答案路由。三种答案形态：
//
//   - choice 选项：一组「出口名+描述」，按胜出选项路由。意图分类即此形态。
//   - noul   是否：问题作为陈述，概率 >= 0.5 走 True，否则走 False。
//   - score  档位：一组有序档位，按最近档位路由，metadata 额外带连续分数。
//
// ai/jevFilter 只判断一句陈述是否成立，True/False 两条线，语义同 jsFilter。
package jev

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego/utils/el"
	"github.com/rulego/rulego/utils/maps"
	"github.com/rulego/rulego/utils/str"
)

func init() {
	_ = rulego.Registry.Register(&JevNode{})
	_ = rulego.Registry.Register(&FilterNode{})
}

const (
	// DefaultURL TypeSafe AI System One 端点，私有网关可改写。
	DefaultURL = "https://api.typesafe.ai/v1/systemone"
	// DefaultModel 默认模型名。
	DefaultModel = "jev-latest"
	// DefaultTimeout 单次 HTTP 请求超时。
	DefaultTimeout = 10 * time.Second
	// DefaultMaxRetries 429/529/网络错误的额外重试次数。
	DefaultMaxRetries = 2
	// GateThreshold noul 概率的 True/False 决策边界。
	GateThreshold = 0.5
	// DefaultMinConfidence 置信度门默认值，负数显式关闭。
	DefaultMinConfidence = 0.5
	// AnswerQuestionID 请求里唯一问题的 id，答案按它返回。
	AnswerQuestionID = "answer"
	// MetadataPrefix ai/jev 写 metadata 的键前缀。
	MetadataPrefix = "jev"
	// FilterMetadataPrefix ai/jevFilter 写 metadata 的键前缀。
	FilterMetadataPrefix = "jevFilter"

	// AnswerChoice 多选一：选项表即路由出口，按胜出选项名连线
	AnswerChoice = "choice"
	// AnswerNoul 是否：问题当作一句陈述，概率 >= GateThreshold 走 True 否则走 False，不填选项
	AnswerNoul = "noul"
	// AnswerScore 档位：选项按顺序作为评分标尺，按最近档位名连线，metadata 带连续分数
	AnswerScore = "score"

	// MaxChoiceOptions choice 形态的选项数上限（System One 单问题上限）。
	MaxChoiceOptions = 255
	// MaxScoreLevels score 形态的档位数上限。
	MaxScoreLevels = 10

	// OutputMetadata 结果写入 msg.Metadata（默认）。
	OutputMetadata = "metadata"
	// OutputData 结果合并进 msg.Data 负荷的 "jev" 键。
	OutputData = "data"
)

// CommonConfig 两个节点共享的连接与输入配置。
// squash 使 mapstructure（Map2Struct）与 encoding/json 都按平铺字段处理。
type CommonConfig struct {
	Url   string `json:"url" label:"API URL" desc:"System One endpoint, e.g. https://api.typesafe.ai/v1/systemone" required:"true"`
	Key   string `json:"key" label:"API Key" desc:"System One API key. Supports ${global.xxx} templates" required:"true"`
	Model string `json:"model" label:"Model" desc:"Model name, e.g. jev-latest"`
	Input string `json:"input" label:"Input Expression" desc:"State expression. Supports ${msg.key} and ${metadata.key}. Empty uses msg.GetData()"`
}

// Configuration ai/jev 节点配置。
// 超时(10s)、重试(2)、门控阈值(0.5)、state 形态(自动识别 JSON)为固定默认值，不暴露配置。
type Configuration struct {
	CommonConfig `json:",squash"`

	Question   string   `json:"question" label:"Question" desc:"The single question to ask about the input, e.g. '选择最匹配的意图' or '该命令具有破坏性'" required:"true"`
	AnswerType string   `json:"answerType" label:"Answer Type" desc:"choice: options, route by winning option; noul: yes/no statement, route True/False; score: ordered levels, route by nearest level"`
	Options    []Option `json:"options" label:"Options" desc:"choice: option list (also the route relations); score: 2-10 ordered levels; ignored by noul"`

	ExtraQuestions []ExtraQuestion `json:"extraQuestions" label:"Extra Questions" desc:"Asked in parallel with the main question in one request; answers are tagged only and never affect routing"`

	OutputTo string `json:"outputTo" label:"Output To" desc:"metadata: write results to msg.Metadata (default, payload untouched); data (alias msg): replace msg.Data with the results JSON object"`

	MinConfidence float64 `json:"minConfidence" label:"Min Confidence" desc:"Answers below this confidence go to the Default relation instead of the winning option. Default 0.5, negative disables"`
}

// ExtraQuestion 附加问题定义，答案按键 id 写入结果（位置由 outputTo 决定）。
type ExtraQuestion struct {
	ID       string   `json:"id" label:"ID" desc:"Answer key in the results (metadata: jev.<id>, data: <id>)" required:"true"`
	Type     string   `json:"type" label:"Type" desc:"noul: yes/no; choice: pick one; score: rubric rating" required:"true"`
	Question string   `json:"question" label:"Question" desc:"What to ask about the input" required:"true"`
	Options  []Option `json:"options" label:"Options" desc:"choice: option->description; score: 2-10 ordered levels; ignored by noul"`
}

// Option 选项定义。choice 时 name 是路由关系类型、description 是选项判据；
// score 时 name 是档位名（按顺序）、description 可选作档位说明。
type Option struct {
	Name        string `json:"name" label:"Name" desc:"Option or level name, used as route relation type" required:"true"`
	Description string `json:"description" label:"Description" desc:"What this option/level means, helps the model decide"`
}

// JevNode ai/jev 决策节点
type JevNode struct {
	Config Configuration

	sharedRuntime
	answerType       string
	outputTo         string
	routeLevels      []string // score 档位名，用于路由
	extraQuestionIDs []string
	extraDocs        []map[string]interface{} // 与 extraQuestionIDs 一一对应的问题文档
}

// Type 组件类型
func (x *JevNode) Type() string {
	return "ai/jev"
}

// New 创建新的组件实例
func (x *JevNode) New() types.Node {
	return &JevNode{
		Config: Configuration{
			CommonConfig: CommonConfig{
				Url:   DefaultURL,
				Model: DefaultModel,
			},
			AnswerType:    AnswerChoice,
			MinConfidence: DefaultMinConfidence,
			Question:      "选择与输入内容最匹配的意图",
			Options: []Option{
				{Name: "createRule", Description: "创建条件触发的自动化联动规则"},
				{Name: "control", Description: "控制设备开关或调节参数"},
				{Name: "query", Description: "查询设备当前状态或数值"},
			},
		},
	}
}

// Init 初始化
func (x *JevNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	if err := maps.Map2Struct(configuration, &x.Config); err != nil {
		return err
	}

	// 零值视为未配置；负数显式关闭置信度门
	if x.Config.MinConfidence == 0 {
		x.Config.MinConfidence = DefaultMinConfidence
	}

	x.answerType = strings.ToLower(strings.TrimSpace(x.Config.AnswerType))
	if x.answerType == "" {
		x.answerType = AnswerChoice
	}
	switch x.answerType {
	case AnswerChoice, AnswerNoul, AnswerScore:
	default:
		return fmt.Errorf("unsupported answerType '%s', expected choice|noul|score", x.Config.AnswerType)
	}

	// msg 是 data 的习惯别名，二者同指消息负荷
	x.outputTo = strings.ToLower(strings.TrimSpace(x.Config.OutputTo))
	if x.outputTo == "" {
		x.outputTo = OutputMetadata
	}
	if x.outputTo == "msg" {
		x.outputTo = OutputData
	}
	switch x.outputTo {
	case OutputMetadata, OutputData:
	default:
		return fmt.Errorf("unsupported outputTo '%s', expected metadata|data", x.Config.OutputTo)
	}

	if strings.TrimSpace(x.Config.Question) == "" {
		return fmt.Errorf("question is required")
	}

	if err := x.validateOptions(); err != nil {
		return err
	}

	for i := range x.Config.ExtraQuestions {
		q := &x.Config.ExtraQuestions[i]
		q.ID = strings.TrimSpace(q.ID)
		q.Question = strings.TrimSpace(q.Question)
	}
	if err := x.validateExtras(); err != nil {
		return err
	}

	return x.initCommon(&x.Config.CommonConfig)
}

// validateOptions 按答案形态校验主问题选项
func (x *JevNode) validateOptions() error {
	levels, err := validateAnswerOptions(x.answerType, x.Config.Options)
	if err != nil {
		return err
	}
	x.routeLevels = levels
	return nil
}

// validateAnswerOptions 按答案形态校验选项表；score 返回档位名
func validateAnswerOptions(answerType string, opts []Option) ([]string, error) {
	switch answerType {
	case AnswerNoul:
		return nil, nil
	case AnswerChoice:
		if len(opts) < 2 {
			return nil, fmt.Errorf("choice requires at least 2 options")
		}
		if len(opts) > MaxChoiceOptions {
			return nil, fmt.Errorf("choice supports at most %d options", MaxChoiceOptions)
		}
		seen := make(map[string]struct{}, len(opts))
		for i, o := range opts {
			name := strings.TrimSpace(o.Name)
			if name == "" {
				return nil, fmt.Errorf("options[%d]: name is required", i)
			}
			if _, dup := seen[name]; dup {
				return nil, fmt.Errorf("options[%d]: duplicate option name '%s'", i, name)
			}
			seen[name] = struct{}{}
		}
	default: // score
		if len(opts) < 2 || len(opts) > MaxScoreLevels {
			return nil, fmt.Errorf("score requires 2-%d ordered levels in options", MaxScoreLevels)
		}
		levels := make([]string, len(opts))
		for i, o := range opts {
			name := strings.TrimSpace(o.Name)
			if name == "" {
				return nil, fmt.Errorf("options[%d]: name is required", i)
			}
			levels[i] = name
		}
		return levels, nil
	}
	return nil, nil
}

// validateExtras 校验附加问题并预构建问题文档
func (x *JevNode) validateExtras() error {
	if len(x.Config.ExtraQuestions) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(x.Config.ExtraQuestions))
	for i, q := range x.Config.ExtraQuestions {
		if q.ID == "" {
			return fmt.Errorf("extraQuestions[%d]: id is required", i)
		}
		if q.ID == AnswerQuestionID {
			return fmt.Errorf("extraQuestions[%d]: id '%s' is reserved", i, AnswerQuestionID)
		}
		if _, dup := seen[q.ID]; dup {
			return fmt.Errorf("extraQuestions[%d]: duplicate id '%s'", i, q.ID)
		}
		seen[q.ID] = struct{}{}
		if q.Question == "" {
			return fmt.Errorf("extraQuestions[%d] '%s': question is required", i, q.ID)
		}
		t := strings.ToLower(strings.TrimSpace(q.Type))
		if t == "" {
			t = AnswerChoice
		}
		switch t {
		case AnswerChoice, AnswerNoul, AnswerScore:
		default:
			return fmt.Errorf("extraQuestions[%d] '%s': unsupported answerType '%s', expected noul|choice|score", i, q.ID, q.Type)
		}
		if _, err := validateAnswerOptions(t, q.Options); err != nil {
			return fmt.Errorf("extraQuestions[%d] '%s': %v", i, q.ID, err)
		}
		x.extraQuestionIDs = append(x.extraQuestionIDs, q.ID)
		x.extraDocs = append(x.extraDocs, buildQuestionDoc(t, q.Question, q.Options))
	}
	return nil
}

// buildQuestion 按答案形态生成主问题文档
func (x *JevNode) buildQuestion() map[string]interface{} {
	return buildQuestionDoc(x.answerType, strings.TrimSpace(x.Config.Question), x.Config.Options)
}

// buildQuestionDoc 按答案形态生成 API 问题文档
func buildQuestionDoc(answerType, question string, opts []Option) map[string]interface{} {
	switch answerType {
	case AnswerNoul:
		return noulQuestion(question)
	case AnswerScore:
		criteria := make([]interface{}, len(opts))
		for i, o := range opts {
			// 档位说明作为评分标尺，缺省用档位名
			if desc := strings.TrimSpace(o.Description); desc != "" {
				criteria[i] = desc
			} else {
				criteria[i] = strings.TrimSpace(o.Name)
			}
		}
		return map[string]interface{}{"type": AnswerScore, "instructions": question, "criteria": criteria}
	default:
		criteria := make(map[string]interface{}, len(opts))
		for _, o := range opts {
			criteria[strings.TrimSpace(o.Name)] = o.Description
		}
		return map[string]interface{}{"type": AnswerChoice, "instructions": question, "criteria": criteria}
	}
}

// noulQuestion 构造是否型问题文档
func noulQuestion(statement string) map[string]interface{} {
	return map[string]interface{}{"type": AnswerNoul, "instructions": statement}
}

// OnMsg 处理消息
func (x *JevNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
	evn := x.env(ctx, msg)

	state, err := x.buildState(evn, msg)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}

	key, err := x.apiKey(evn)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}

	// 主问题 + 附加问题合并进一次请求，System One 并行评估
	questions := map[string]interface{}{AnswerQuestionID: x.buildQuestion()}
	for i, id := range x.extraQuestionIDs {
		questions[id] = x.extraDocs[i]
	}

	resp, err := x.client.Query(ctx.GetContext(), x.Config.Url, key, &Request{
		Model:     x.Config.Model,
		State:     state,
		Questions: questions,
	}, DefaultMaxRetries)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}

	a, ok := resp.Answers[AnswerQuestionID]
	if !ok {
		ctx.TellFailure(msg, fmt.Errorf("no answer returned"))
		return
	}

	if x.outputTo == OutputData {
		x.writeToPayload(msg, resp, &a)
	} else {
		md := msg.GetMetadata()
		writeDecision(md, MetadataPrefix, resp, &a)
		for _, id := range x.extraQuestionIDs {
			// 附加问题只打标：缺答案不报错，主问题路由不受影响
			if ea, ok := resp.Answers[id]; ok {
				writeAnswer(md, MetadataPrefix, id, &ea)
			}
		}
		if a.Type == AnswerScore && a.Score != nil {
			// 档位名同时写入 metadata，下游可按连线之外的方式消费
			md.PutValue(MetadataPrefix+".level", x.nearestLevel(&a))
		}
	}

	relation, err := x.routeRelation(&a)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}
	ctx.TellNext(msg, relation)
}

// routeRelation 从答案计算关系类型。
// choice 取胜出项，noul 按阈值取 True/False，score 取最近档位；
// 置信度低于 minConfidence 时走 Default 线（同 switch 的兜底线惯例，可连升级分支）。
func (x *JevNode) routeRelation(a *Answer) (string, error) {
	switch a.Type {
	case AnswerChoice:
		if a.Choice == "" {
			return "", fmt.Errorf("empty choice returned")
		}
		if a.Confidence != nil && *a.Confidence < x.Config.MinConfidence {
			return types.DefaultRelationType, nil
		}
		return a.Choice, nil
	case AnswerNoul:
		return noulRelation(a)
	case AnswerScore:
		if a.Score == nil {
			return "", fmt.Errorf("no score returned")
		}
		if a.Confidence != nil && *a.Confidence < x.Config.MinConfidence {
			return types.DefaultRelationType, nil
		}
		return x.nearestLevel(a), nil
	default:
		return "", fmt.Errorf("unsupported answer type '%s'", a.Type)
	}
}

// nearestLevel 连续分数四舍五入到最近档位，越界钳制
func (x *JevNode) nearestLevel(a *Answer) string {
	idx := int(math.Round(*a.Score))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(x.routeLevels) {
		idx = len(x.routeLevels) - 1
	}
	return x.routeLevels[idx]
}

// Destroy 销毁资源
func (x *JevNode) Destroy() {
	// 无需清理
}

// Desc returns the component description
func (x *JevNode) Desc() string {
	return "Ask one question via TypeSafe AI System One (Jev) and route by the answer. " +
		"Answer types: choice (options), noul (yes/no, True/False), score (ordered levels). " +
		"Asks extra questions in parallel for tagging only. " +
		"outputTo metadata writes results to msg.Metadata (default); outputTo data/msg replaces msg.Data with the results JSON. " +
		"Low-confidence answers go to the Default relation"
}

// sharedRuntime 两个节点共享的连接与输入模板：初始化、state 构造、key 渲染。
type sharedRuntime struct {
	client        *Client
	key           string // 原始 key，配置了模板时运行期渲染覆盖
	inputTemplate el.Template
	keyTemplate   el.Template
	hasVar        bool
}

// initCommon 补齐默认值并初始化模板
func (r *sharedRuntime) initCommon(cfg *CommonConfig) error {
	cfg.Url = strings.TrimSpace(cfg.Url)
	if cfg.Url == "" {
		cfg.Url = DefaultURL
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	cfg.Input = strings.TrimSpace(cfg.Input)
	if cfg.Input != "" {
		tmpl, err := el.NewTemplate(cfg.Input)
		if err != nil {
			return fmt.Errorf("invalid input expression: %v", err)
		}
		r.inputTemplate = tmpl
		if tmpl.HasVar() {
			r.hasVar = true
		}
	}
	// key 支持运行期渲染（${global.xxx}），无变量时按原值发送
	r.key = strings.TrimSpace(cfg.Key)
	if r.key != "" {
		if tmpl, err := el.NewTemplate(r.key); err != nil {
			return fmt.Errorf("invalid key template: %v", err)
		} else if tmpl.HasVar() {
			r.keyTemplate = tmpl
			r.hasVar = true
		}
	}
	r.client = NewClient(DefaultTimeout)
	return nil
}

// env 仅在存在变量模板时构造求值环境
func (r *sharedRuntime) env(ctx types.RuleContext, msg types.RuleMsg) map[string]interface{} {
	if !r.hasVar {
		return nil
	}
	return base.NodeUtils.GetEvnAndMetadata(ctx, msg)
}

// apiKey 返回本次请求用的 key，无模板时为配置原值
func (r *sharedRuntime) apiKey(evn map[string]interface{}) (string, error) {
	if r.keyTemplate == nil {
		return r.key, nil
	}
	v, err := r.keyTemplate.Execute(evn)
	if err != nil {
		return "", fmt.Errorf("failed to execute key template: %v", err)
	}
	return str.ToString(v), nil
}

// buildState 构造请求 state。System One 的 state 原生支持对象/数组：
// 消息体是 JSON 对象/数组时自动传结构化，否则传文本。
func (r *sharedRuntime) buildState(evn map[string]interface{}, msg types.RuleMsg) (interface{}, error) {
	var text string
	if r.inputTemplate != nil {
		v, err := r.inputTemplate.Execute(evn)
		if err != nil {
			return nil, fmt.Errorf("failed to execute input template: %v", err)
		}
		text = str.ToString(v)
	} else {
		text = msg.GetData()
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty state: input data is empty")
	}

	if msg.GetDataType() == types.JSON || isJSONContainer(text) {
		if v, err := parseStructuredState(text); err == nil {
			return v, nil
		}
	}
	return text, nil
}

func parseStructuredState(text string) (interface{}, error) {
	var v interface{}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, fmt.Errorf("failed to parse JSON state: %v", err)
	}
	switch v.(type) {
	case map[string]interface{}, []interface{}:
		return v, nil
	default:
		return nil, fmt.Errorf("JSON state must be an object or array")
	}
}

func isJSONContainer(text string) bool {
	return strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[")
}

// writeDecision 把模型信息与主问题答案写入 metadata
func writeDecision(md *types.Metadata, prefix string, resp *Response, a *Answer) {
	md.PutValue(prefix+".model", resp.Model)
	md.PutValue(prefix+".usage", fmt.Sprintf("%d/%d", resp.Usage.InputTokens, resp.Usage.OutputTokens))
	writeAnswer(md, prefix, "answer", a)
}

// writeAnswer 把单个答案写入 metadata：<prefix>.<key> 及可选的置信度与分布
func writeAnswer(md *types.Metadata, prefix, key string, a *Answer) {
	md.PutValue(prefix+"."+key, a.PrimaryValue())
	if a.Confidence != nil {
		md.PutValue(prefix+"."+key+".confidence", formatFloat(*a.Confidence))
	}
	if a.Probabilities != nil {
		if b, err := json.Marshal(a.Probabilities); err == nil {
			md.PutValue(prefix+"."+key+".probabilities", string(b))
		}
	}
}

// noulRelation 是否型答案按阈值取 True/False
func noulRelation(a *Answer) (string, error) {
	if a.Noul == nil {
		return "", fmt.Errorf("no noul probability returned")
	}
	if *a.Noul >= GateThreshold {
		return types.True, nil
	}
	return types.False, nil
}

// writeToPayload 用结果对象整体替换 msg.Data（outputTo=data）。
// 值为原生类型（数值/对象），metadata 模式下才是字符串形态。
func (x *JevNode) writeToPayload(msg types.RuleMsg, resp *Response, a *Answer) {
	result := map[string]interface{}{
		"model": resp.Model,
		"usage": map[string]int{"input": resp.Usage.InputTokens, "output": resp.Usage.OutputTokens},
	}
	result["answer"] = answerValue(a)
	if a.Confidence != nil {
		result["answer.confidence"] = *a.Confidence
	}
	if a.Probabilities != nil {
		result["answer.probabilities"] = a.Probabilities
	}
	if a.Type == AnswerScore && a.Score != nil {
		result["level"] = x.nearestLevel(a)
	}
	for _, id := range x.extraQuestionIDs {
		ea, ok := resp.Answers[id]
		if !ok {
			continue
		}
		result[id] = answerValue(&ea)
		if ea.Confidence != nil {
			result[id+".confidence"] = *ea.Confidence
		}
		if ea.Probabilities != nil {
			result[id+".probabilities"] = ea.Probabilities
		}
	}

	if b, err := json.Marshal(result); err == nil {
		msg.SetData(string(b))
		msg.SetDataType(types.JSON)
	}
}

// answerValue 答案主值：choice 为选项名，noul/score 为数值（payload 用原生类型）
func answerValue(a *Answer) interface{} {
	switch a.Type {
	case AnswerNoul:
		if a.Noul != nil {
			return *a.Noul
		}
	case AnswerScore:
		if a.Score != nil {
			return *a.Score
		}
	}
	return a.Choice
}
