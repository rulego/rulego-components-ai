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
	"strings"
	"testing"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test/assert"
)

func TestFilterNode_Type(t *testing.T) {
	assert.Equal(t, "ai/jevFilter", (&FilterNode{}).Type())
}

func TestFilter_TrueFalse(t *testing.T) {
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

			relation, outMsg, err := runNode(t, &FilterNode{}, map[string]interface{}{
				"url":      srv.URL,
				"key":      "test-key",
				"question": "该命令会修改或删除数据，具有破坏性",
			}, "rm -rf /tmp/cache", "", nil)

			assert.Nil(t, err)
			assert.Equal(t, tc.wantRel, relation)
			md := outMsg.GetMetadata()
			assert.True(t, strings.HasPrefix(md.GetValue("jevFilter.answer"), "0."))
			assert.Equal(t, "jev-1.13.0-test", md.GetValue("jevFilter.model"))
			// 请求形态：noul 问题携带陈述
			q := captured[0].raw["questions"].(map[string]interface{})["answer"].(map[string]interface{})
			assert.Equal(t, "noul", q["type"])
			assert.Equal(t, "该命令会修改或删除数据，具有破坏性", q["instructions"])
			assert.Equal(t, "Bearer test-key", captured[0].auth)
		})
	}
}

func TestFilter_MetadataPrefixDistinct(t *testing.T) {
	var captured []capturedRequest
	srv := newMockServer(t, nil, fixedResponse(noulAnswer(1)), &captured)
	defer srv.Close()

	// 与 ai/jev 同链共存时键不冲突
	_, outMsg, err := runNode(t, &FilterNode{}, map[string]interface{}{
		"url":      srv.URL,
		"key":      "k",
		"question": "s",
	}, "data", "", nil)

	assert.Nil(t, err)
	md := outMsg.GetMetadata()
	assert.Equal(t, "1", md.GetValue("jevFilter.answer"))
	assert.Equal(t, "", md.GetValue("jev.answer"))
}

func TestFilter_Init_Validation(t *testing.T) {
	node := &FilterNode{}
	err := node.Init(types.NewConfig(), map[string]interface{}{
		"url": "http://x",
	})
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "question is required"))
}

func TestFilter_New_Defaults(t *testing.T) {
	n := (&FilterNode{}).New().(*FilterNode)
	assert.Equal(t, DefaultURL, n.Config.Url)
	assert.NotEqual(t, "", n.Config.Question)
}
