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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/rulego/rulego/test/assert"
)

type capturedRequest struct {
	auth string
	raw  map[string]interface{}
}

// newMockServer 模拟 System One API。statuses 按调用序返回非 200 状态码，
// 超出长度的调用返回 200；answer 每次调用求值以返回固定响应。
func newMockServer(t *testing.T, statuses []int, answer func() Response, captured *[]capturedRequest) *httptest.Server {
	t.Helper()
	var calls int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1))
		body, _ := io.ReadAll(r.Body)
		var raw map[string]interface{}
		_ = json.Unmarshal(body, &raw)
		*captured = append(*captured, capturedRequest{auth: r.Header.Get("Authorization"), raw: raw})

		status := http.StatusOK
		if n <= len(statuses) {
			status = statuses[n-1]
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"mock"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(answer())
	}))
}

// runNode 初始化节点并异步执行 OnMsg，返回路由结果
func runNode(t *testing.T, node types.Node, config map[string]interface{}, data string, dataType types.DataType, metadata *types.Metadata) (string, types.RuleMsg, error) {
	t.Helper()
	if err := node.Init(types.NewConfig(), config); err != nil {
		return "", types.RuleMsg{}, err
	}
	done := make(chan struct{})
	var relation string
	var outMsg types.RuleMsg
	var err error
	ctx := test.NewRuleContext(types.NewConfig(), func(m types.RuleMsg, rel string, e error) {
		outMsg = m
		relation = rel
		err = e
		close(done)
	})
	if metadata == nil {
		metadata = types.NewMetadata()
	}
	msg := ctx.NewMsg("TEST", metadata, data)
	msg.SetDataType(dataType)
	go node.OnMsg(ctx, msg)
	select {
	case <-done:
		return relation, outMsg, err
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for OnMsg")
		return "", types.RuleMsg{}, nil
	}
}

func choiceAnswer(option string, conf float64) Answer {
	return Answer{Type: "choice", Choice: option, Confidence: &conf,
		Probabilities: map[string]float64{option: 1}}
}

func noulAnswer(p float64) Answer {
	return Answer{Type: "noul", Noul: &p}
}

func scoreAnswer(s, conf float64) Answer {
	return Answer{Type: "score", Score: &s, Confidence: &conf}
}

func fixedResponse(a Answer) func() Response {
	return func() Response {
		resp := Response{Model: "jev-1.13.0-test"}
		resp.Answers = map[string]Answer{"answer": a}
		resp.Usage.InputTokens = 392
		resp.Usage.OutputTokens = 65
		return resp
	}
}

func intentOptions() []map[string]interface{} {
	return []map[string]interface{}{
		{"name": "billing", "description": "账单问题"},
		{"name": "technical", "description": "故障与集成"},
		{"name": "sales", "description": "售前"},
	}
}

func TestNode_Type(t *testing.T) {
	assert.Equal(t, "ai/jev", (&JevNode{}).Type())
}

func TestChoice(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(choiceAnswer("technical", 0.9)), &captured)
	defer srv.Close()

	relation, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":      srv.URL,
		"key":      "test-key",
		"question": "选择与输入内容最匹配的意图",
		"options":  intentOptions(),
	}, "Stripe integration keeps failing", "", nil)

	assert.Nil(t, err)
	assert.Equal(t, "technical", relation)
	md := outMsg.GetMetadata()
	assert.Equal(t, "technical", md.GetValue("jev.answer"))
	assert.Equal(t, "0.9", md.GetValue("jev.answer.confidence"))
	assert.True(t, strings.Contains(md.GetValue("jev.answer.probabilities"), "technical"))
	assert.Equal(t, "jev-1.13.0-test", md.GetValue("jev.model"))
	assert.Equal(t, "392/65", md.GetValue("jev.usage"))

	// 请求形态：单个 choice 问题，state 为文本
	req := captured[0]
	q := req.raw["questions"].(map[string]interface{})["answer"].(map[string]interface{})
	assert.Equal(t, "choice", q["type"])
	assert.Equal(t, "选择与输入内容最匹配的意图", q["instructions"])
	criteria := q["criteria"].(map[string]interface{})
	assert.Equal(t, "故障与集成", criteria["technical"])
	assert.Equal(t, "Bearer test-key", req.auth)
	assert.Equal(t, "Stripe integration keeps failing", req.raw["state"])
}

func TestChoice_MissingConfidencePasses(t *testing.T) {
	// API 偶发省略 confidence 字段：缺失视为通过，不拦截到 Default 线
	var captured []capturedRequest
	srv := newMockServer(t, nil, func() Response {
		resp := Response{Model: "jev-test", Answers: map[string]Answer{
			"answer": {Type: "choice", Choice: "technical"},
		}}
		return resp
	}, &captured)
	defer srv.Close()

	relation, _, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":      srv.URL,
		"key":      "k",
		"question": "q",
		"options":  intentOptions(),
	}, "data", "", nil)

	assert.Nil(t, err)
	assert.Equal(t, "technical", relation)
}

func TestChoice_LowConfidence(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(choiceAnswer("technical", 0.3)), &captured)
	defer srv.Close()

	// 低置信度走 Success 线（该线专属低置信，可连升级分支）
	relation, _, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":      srv.URL,
		"key":      "test-key",
		"question": "q",
		"options":  intentOptions(),
	}, "hello", "", nil)
	assert.Nil(t, err)
	assert.Equal(t, types.DefaultRelationType, relation)

	// 负数显式关闭置信度门
	relation, _, err = runNode(t, &JevNode{}, map[string]interface{}{
		"url":           srv.URL,
		"key":           "test-key",
		"question":      "q",
		"options":       intentOptions(),
		"minConfidence": -1,
	}, "hello", "", nil)
	assert.Nil(t, err)
	assert.Equal(t, "technical", relation)
}

func TestNoul(t *testing.T) {
	cases := []struct {
		name    string
		noul    float64
		wantRel string
	}{
		{"high probability true", 0.95, types.True},
		{"low probability false", 0.2, types.False},
		{"below half is false", 0.49, types.False},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured []capturedRequest
			srv := newMockServer(t, nil, fixedResponse(noulAnswer(tc.noul)), &captured)
			defer srv.Close()

			relation, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
				"url":        srv.URL,
				"key":        "test-key",
				"question":   "该命令具有破坏性",
				"answerType": "noul",
			}, "rm -rf /tmp/cache", "", nil)

			assert.Nil(t, err)
			assert.Equal(t, tc.wantRel, relation)
			// 概率主值写入 metadata
			assert.True(t, strings.HasPrefix(outMsg.GetMetadata().GetValue("jev.answer"), "0."))
			q := captured[0].raw["questions"].(map[string]interface{})["answer"].(map[string]interface{})
			assert.Equal(t, "noul", q["type"])
			assert.Equal(t, "该命令具有破坏性", q["instructions"])
		})
	}
}

func TestScore(t *testing.T) {
	cases := []struct {
		name    string
		score   float64
		wantRel string
	}{
		{"nearest level warning", 1.4, "warning"},
		{"rounds down to info", 0.3, "info"},
		{"clamped to critical", 5.2, "critical"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured []capturedRequest
			srv := newMockServer(t, nil, fixedResponse(scoreAnswer(tc.score, 0.95)), &captured)
			defer srv.Close()

			relation, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
				"url":        srv.URL,
				"key":        "test-key",
				"question":   "评估告警严重程度",
				"answerType": "score",
				"options": []map[string]interface{}{
					{"name": "info", "description": "无需处理"},
					{"name": "warning", "description": "值班员关注"},
					{"name": "high", "description": "尽快处理"},
					{"name": "critical", "description": "立即处置"},
				},
			}, "payouts failing for 3 days", "", nil)

			assert.Nil(t, err)
			assert.Equal(t, tc.wantRel, relation)
			md := outMsg.GetMetadata()
			// 连续分数写入 metadata，档位说明作为评分标尺
			assert.Equal(t, formatFloat(tc.score), md.GetValue("jev.answer"))
			assert.Equal(t, tc.wantRel, md.GetValue("jev.level"))
			q := captured[0].raw["questions"].(map[string]interface{})["answer"].(map[string]interface{})
			assert.Equal(t, "score", q["type"])
			levels := q["criteria"].([]interface{})
			assert.Equal(t, 4, len(levels))
			assert.Equal(t, "无需处理", levels[0])
		})
	}
}

func TestScore_LowConfidence(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(scoreAnswer(1.4, 0.3)), &captured)
	defer srv.Close()

	relation, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":        srv.URL,
		"key":        "test-key",
		"question":   "评估严重程度",
		"answerType": "score",
		"options": []map[string]interface{}{
			{"name": "low"}, {"name": "mid"}, {"name": "high"},
		},
	}, "data", "", nil)

	assert.Nil(t, err)
	assert.Equal(t, types.DefaultRelationType, relation)
	// 分数与最近档位仍写入 metadata 供下游自行判断
	assert.Equal(t, "1.4", outMsg.GetMetadata().GetValue("jev.answer"))
	assert.Equal(t, "mid", outMsg.GetMetadata().GetValue("jev.level"))
}

func TestStateAuto_Structured(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(noulAnswer(1)), &captured)
	defer srv.Close()

	config := func() map[string]interface{} {
		return map[string]interface{}{
			"url":        srv.URL,
			"key":        "test-key",
			"question":   "设备温度过高",
			"answerType": "noul",
		}
	}

	// JSON 消息体：自动传结构化对象
	relation, _, err := runNode(t, &JevNode{}, config(), `{"device":"boiler","temp":42}`, types.JSON, nil)
	assert.Nil(t, err)
	assert.Equal(t, types.True, relation)
	state := captured[0].raw["state"].(map[string]interface{})
	assert.Equal(t, "boiler", state["device"])
	assert.Equal(t, float64(42), state["temp"])

	// 纯文本消息体：传字符串
	relation, _, err = runNode(t, &JevNode{}, config(), "just plain text", "", nil)
	assert.Nil(t, err)
	assert.Equal(t, types.True, relation)
	assert.Equal(t, "just plain text", captured[1].raw["state"])
}

func TestStateInvalidJsonFallsBackToText(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(noulAnswer(1)), &captured)
	defer srv.Close()

	// 坏 JSON 不报错，按文本下发
	relation, _, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":        srv.URL,
		"key":        "test-key",
		"question":   "s",
		"answerType": "noul",
	}, "{not valid json", "", nil)

	assert.Nil(t, err)
	assert.Equal(t, types.True, relation)
	assert.Equal(t, "{not valid json", captured[0].raw["state"])
}

func TestKeyTemplate(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(noulAnswer(1)), &captured)
	defer srv.Close()

	md := types.NewMetadata()
	md.PutValue("jevKey", "key-from-metadata")

	_, _, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":        srv.URL,
		"key":        "${metadata.jevKey}",
		"question":   "s",
		"answerType": "noul",
	}, "data", "", md)

	assert.Nil(t, err)
	assert.Equal(t, "Bearer key-from-metadata", captured[0].auth)
}

func TestInputTemplate(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(noulAnswer(1)), &captured)
	defer srv.Close()

	md := types.NewMetadata()
	md.PutValue("ticket", "payout broken")

	_, _, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":        srv.URL,
		"key":        "k",
		"question":   "s",
		"answerType": "noul",
		"input":      "${metadata.ticket}",
	}, "ignored body", "", md)

	assert.Nil(t, err)
	assert.Equal(t, "payout broken", captured[0].raw["state"])
}

func TestRetryOn429(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, []int{http.StatusTooManyRequests}, fixedResponse(noulAnswer(1)), &captured)
	defer srv.Close()

	relation, _, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":        srv.URL,
		"key":        "k",
		"question":   "s",
		"answerType": "noul",
	}, "data", "", nil)

	assert.Nil(t, err)
	assert.Equal(t, types.True, relation)
	assert.Equal(t, 2, len(captured))
}

func TestServerError(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, []int{http.StatusInternalServerError}, fixedResponse(noulAnswer(1)), &captured)
	defer srv.Close()

	_, _, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":        srv.URL,
		"key":        "k",
		"question":   "s",
		"answerType": "noul",
	}, "data", "", nil)

	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "500"))
}

func TestEmptyState(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(noulAnswer(1)), &captured)
	defer srv.Close()

	_, _, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":        srv.URL,
		"key":        "k",
		"question":   "s",
		"answerType": "noul",
	}, "  ", "", nil)

	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "empty state"))
}

func TestInit_Validation(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]interface{}
		wantErr string
	}{
		{"no question", map[string]interface{}{"url": "u"}, "question is required"},
		{"bad answerType", map[string]interface{}{"url": "u", "question": "q", "answerType": "essay"}, "unsupported answerType"},
		{"choice few options", map[string]interface{}{"url": "u", "question": "q", "options": []map[string]interface{}{{"name": "a"}}}, "at least 2 options"},
		{"choice duplicate", map[string]interface{}{"url": "u", "question": "q", "options": []map[string]interface{}{
			{"name": "a"}, {"name": "a"},
		}}, "duplicate option name"},
		{"choice no name", map[string]interface{}{"url": "u", "question": "q", "options": []map[string]interface{}{
			{"name": "a"}, {"description": "d"},
		}}, "name is required"},
		{"score few levels", map[string]interface{}{"url": "u", "question": "q", "answerType": "score", "options": []map[string]interface{}{{"name": "low"}}}, "2-10 ordered levels"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &JevNode{}
			err := node.Init(types.NewConfig(), tc.config)
			assert.NotNil(t, err)
			assert.True(t, strings.Contains(err.Error(), tc.wantErr),
				"expected error containing %q, got %q", tc.wantErr, err.Error())
		})
	}
}

func fixedResponseMulti(answers map[string]Answer) func() Response {
	return func() Response {
		resp := Response{Model: "jev-1.13.0-test", Answers: answers}
		resp.Usage.InputTokens = 392
		resp.Usage.OutputTokens = 65
		return resp
	}
}

func TestExtraQuestions(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponseMulti(map[string]Answer{
		"answer":   choiceAnswer("technical", 0.9),
		"isUrgent": noulAnswer(0.99),
		"severity": scoreAnswer(2.0, 0.9),
	}), &captured)
	defer srv.Close()

	relation, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":      srv.URL,
		"key":      "test-key",
		"question": "选择与输入内容最匹配的意图",
		"options":  intentOptions(),
		"extraQuestions": []map[string]interface{}{
			{"id": "isUrgent", "type": "noul", "question": "消息表达紧急"},
			{"id": "severity", "type": "score", "question": "评估严重程度",
				"options": []map[string]interface{}{{"name": "低"}, {"name": "中"}, {"name": "高"}}},
		},
	}, "Stripe integration failing", "", nil)

	assert.Nil(t, err)
	// 路由仍由主问题驱动，附加问题不影响
	assert.Equal(t, "technical", relation)
	md := outMsg.GetMetadata()
	// 三个问题一次请求并行发出
	questions := captured[0].raw["questions"].(map[string]interface{})
	assert.Equal(t, 3, len(questions))
	// 附加问题答案写入 metadata
	assert.Equal(t, "0.99", md.GetValue("jev.isUrgent"))
	assert.Equal(t, "2", md.GetValue("jev.severity"))
	assert.Equal(t, "0.9", md.GetValue("jev.severity.confidence"))
	// 附加问题的档位描述作为评分标尺
	sq := questions["severity"].(map[string]interface{})
	assert.Equal(t, []interface{}{"低", "中", "高"}, sq["criteria"])
}

func TestOutputToData(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponseMulti(map[string]Answer{
		"answer":   choiceAnswer("technical", 0.9),
		"isUrgent": noulAnswer(0.99),
	}), &captured)
	defer srv.Close()

	// 原 body 为 JSON 对象：注入 jev 键，原有键保留，数值用原生类型
	relation, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":      srv.URL,
		"key":      "k",
		"question": "q",
		"options":  intentOptions(),
		"extraQuestions": []map[string]interface{}{
			{"id": "isUrgent", "type": "noul", "question": "紧急吗"},
		},
		"outputTo": "data",
	}, `{"ticketId":"T-1"}`, types.JSON, nil)

	assert.Nil(t, err)
	assert.Equal(t, "technical", relation)
	// 负荷被结果对象整体替换，原 body 的 ticketId 不再保留
	var body map[string]interface{}
	assert.Nil(t, json.Unmarshal([]byte(outMsg.GetData()), &body))
	assert.Nil(t, body["ticketId"])
	assert.Equal(t, "technical", body["answer"])
	assert.Equal(t, 0.9, body["answer.confidence"])
	assert.Equal(t, 0.99, body["isUrgent"])
	// metadata 模式专属的键不写入
	assert.Equal(t, "", outMsg.GetMetadata().GetValue("jev.answer"))
}

func TestOutputToData_PlainBody(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponseMulti(map[string]Answer{
		"answer": choiceAnswer("billing", 0.9),
	}), &captured)
	defer srv.Close()

	// 纯文本 body 同样整体替换为结果对象
	_, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":      srv.URL,
		"key":      "k",
		"question": "q",
		"options":  intentOptions(),
		"outputTo": "data",
	}, "plain text body", "", nil)

	assert.Nil(t, err)
	var body map[string]interface{}
	assert.Nil(t, json.Unmarshal([]byte(outMsg.GetData()), &body))
	assert.Equal(t, "billing", body["answer"])
}

func TestInit_OutputToValidation(t *testing.T) {
	node := &JevNode{}
	err := node.Init(types.NewConfig(), map[string]interface{}{
		"url": "u", "question": "q", "outputTo": "file",
	})
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "unsupported outputTo"))
}

func TestOutputTo_MsgAlias(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponseMulti(map[string]Answer{
		"answer": choiceAnswer("billing", 0.9),
	}), &captured)
	defer srv.Close()

	// msg 是 data 的习惯别名，同样整体替换负荷
	_, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url": srv.URL, "key": "k", "question": "q",
		"options":  intentOptions(),
		"outputTo": "msg",
	}, "body", "", nil)

	assert.Nil(t, err)
	var body map[string]interface{}
	assert.Nil(t, json.Unmarshal([]byte(outMsg.GetData()), &body))
	assert.Equal(t, "billing", body["answer"])
	assert.Equal(t, "", outMsg.GetMetadata().GetValue("jev.answer"))
}

func TestExtraQuestions_MissingAnswerNoImpact(t *testing.T) {
	var captured []capturedRequest
	// 响应缺 isUrgent 的答案：不打标也不报错，主问题照常路由
	srv := newMockServer(t, nil, fixedResponseMulti(map[string]Answer{
		"answer": choiceAnswer("billing", 0.9),
	}), &captured)
	defer srv.Close()

	relation, outMsg, err := runNode(t, &JevNode{}, map[string]interface{}{
		"url":      srv.URL,
		"key":      "k",
		"question": "q",
		"options":  intentOptions(),
		"extraQuestions": []map[string]interface{}{
			{"id": "isUrgent", "type": "noul", "question": "紧急吗"},
		},
	}, "data", "", nil)

	assert.Nil(t, err)
	assert.Equal(t, "billing", relation)
	assert.Equal(t, "", outMsg.GetMetadata().GetValue("jev.isUrgent"))
}

func TestExtraQuestions_Validation(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]interface{}
		wantErr string
	}{
		{"no id", map[string]interface{}{"url": "u", "question": "q", "options": intentOptions(),
			"extraQuestions": []map[string]interface{}{{"type": "noul", "question": "x"}}}, "id is required"},
		{"reserved id", map[string]interface{}{"url": "u", "question": "q", "options": intentOptions(),
			"extraQuestions": []map[string]interface{}{{"id": "answer", "type": "noul", "question": "x"}}}, "reserved"},
		{"duplicate id", map[string]interface{}{"url": "u", "question": "q", "options": intentOptions(),
			"extraQuestions": []map[string]interface{}{
				{"id": "a", "type": "noul", "question": "x"}, {"id": "a", "type": "noul", "question": "y"},
			}}, "duplicate id"},
		{"no question", map[string]interface{}{"url": "u", "question": "q", "options": intentOptions(),
			"extraQuestions": []map[string]interface{}{{"id": "a", "type": "noul"}}}, "question is required"},
		{"bad type", map[string]interface{}{"url": "u", "question": "q", "options": intentOptions(),
			"extraQuestions": []map[string]interface{}{{"id": "a", "type": "essay", "question": "x"}}}, "unsupported answerType"},
		{"choice few options", map[string]interface{}{"url": "u", "question": "q", "options": intentOptions(),
			"extraQuestions": []map[string]interface{}{{"id": "a", "type": "choice", "question": "x",
				"options": []map[string]interface{}{{"name": "only"}}}}}, "at least 2 options"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &JevNode{}
			err := node.Init(types.NewConfig(), tc.config)
			assert.NotNil(t, err)
			assert.True(t, strings.Contains(err.Error(), tc.wantErr),
				"expected error containing %q, got %q", tc.wantErr, err.Error())
		})
	}
}

func TestNew_Defaults(t *testing.T) {
	node := &JevNode{}
	n := node.New().(*JevNode)
	// 出厂默认即意图分类形态，填 url/key 即用
	assert.Equal(t, AnswerChoice, n.Config.AnswerType)
	assert.Equal(t, DefaultURL, n.Config.Url)
	assert.True(t, len(n.Config.Options) >= 2)
}
