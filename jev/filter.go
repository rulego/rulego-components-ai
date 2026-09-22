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

package jev

import (
	"fmt"
	"strings"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/utils/maps"
)

// FilterConfiguration ai/jevFilter 节点配置。
// 连接与输入字段复用 CommonConfig；固定默认值与 ai/jev 一致，不重复暴露。
type FilterConfiguration struct {
	CommonConfig `json:",squash"`

	Question string `json:"question" label:"Question" desc:"The claim to verify against the input, e.g. '该命令会修改或删除数据，具有破坏性'. Probability >= 0.5 routes True" required:"true"`
}

// FilterNode ai/jevFilter 过滤器节点：判断一句陈述对消息内容是否成立，
// True/False 两条线，契约同 jsFilter。
type FilterNode struct {
	Config FilterConfiguration

	sharedRuntime
}

// Type 组件类型
func (x *FilterNode) Type() string {
	return "ai/jevFilter"
}

// New 创建新的组件实例
func (x *FilterNode) New() types.Node {
	return &FilterNode{
		Config: FilterConfiguration{
			CommonConfig: CommonConfig{
				Url:   DefaultURL,
				Model: DefaultModel,
			},
			Question: "该命令会修改或删除数据，具有破坏性",
		},
	}
}

// Init 初始化
func (x *FilterNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	if err := maps.Map2Struct(configuration, &x.Config); err != nil {
		return err
	}
	if strings.TrimSpace(x.Config.Question) == "" {
		return fmt.Errorf("question is required")
	}
	return x.initCommon(&x.Config.CommonConfig)
}

// OnMsg 处理消息
func (x *FilterNode) OnMsg(ctx types.RuleContext, msg types.RuleMsg) {
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

	resp, err := x.client.Query(ctx.GetContext(), x.Config.Url, key, &Request{
		Model:     x.Config.Model,
		State:     state,
		Questions: map[string]interface{}{AnswerQuestionID: noulQuestion(strings.TrimSpace(x.Config.Question))},
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

	writeDecision(msg.GetMetadata(), FilterMetadataPrefix, resp, &a)

	relation, err := noulRelation(&a)
	if err != nil {
		ctx.TellFailure(msg, err)
		return
	}
	ctx.TellNext(msg, relation)
}

// Destroy 销毁资源
func (x *FilterNode) Destroy() {
	// 无需清理
}

// Desc returns the component description
func (x *FilterNode) Desc() string {
	return "jsFilter-style gate via TypeSafe AI System One (Jev): verify one statement against the input, " +
		"route True when the noul probability >= 0.5, otherwise False. " +
		"No scripting: write a natural-language statement instead of code"
}
