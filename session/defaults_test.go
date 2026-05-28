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

package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProcessToolCallArguments(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValid bool // 结果是否应该是有效 JSON
		checkFunc func(t *testing.T, result string)
	}{
		{
			name:      "空字符串返回空对象",
			input:     "",
			wantValid: true,
			checkFunc: func(t *testing.T, result string) {
				if result != "{}" {
					t.Errorf("expected '{}', got '%s'", result)
				}
			},
		},
		{
			name:      "短参数直接返回",
			input:     `{"operation":"file","path":"test.txt"}`,
			wantValid: true,
			checkFunc: func(t *testing.T, result string) {
				if result != `{"operation":"file","path":"test.txt"}` {
					t.Errorf("expected unchanged, got '%s'", result)
				}
			},
		},
		{
			name:      "大content字段被截断",
			input:     `{"operation":"file","path":"test.txt","content":"` + strings.Repeat("A", 1000) + `"}`,
			wantValid: true,
			checkFunc: func(t *testing.T, result string) {
				var params map[string]interface{}
				if err := json.Unmarshal([]byte(result), &params); err != nil {
					t.Errorf("result is not valid JSON: %v", err)
					return
				}
				content, ok := params["content"].(string)
				if !ok {
					t.Error("content field missing or not string")
					return
				}
				if len(content) > MaxToolCallArgumentSize+50 { // 允许截断标记的额外长度
					t.Errorf("content not truncated, len=%d", len(content))
				}
				// 检查其他字段保留
				if params["operation"] != "file" {
					t.Error("operation field lost")
				}
				if params["path"] != "test.txt" {
					t.Error("path field lost")
				}
			},
		},
		{
			name:      "无效JSON返回空对象",
			input:     `{invalid json}`,
			wantValid: true,
			checkFunc: func(t *testing.T, result string) {
				if result != "{}" {
					t.Errorf("expected '{}', got '%s'", result)
				}
			},
		},
		{
			name:      "new_content字段被截断",
			input:     `{"operation":"edit","path":"test.txt","new_content":"` + strings.Repeat("B", 1000) + `"}`,
			wantValid: true,
			checkFunc: func(t *testing.T, result string) {
				var params map[string]interface{}
				if err := json.Unmarshal([]byte(result), &params); err != nil {
					t.Errorf("result is not valid JSON: %v", err)
					return
				}
				content, ok := params["new_content"].(string)
				if !ok {
					t.Error("new_content field missing or not string")
					return
				}
				if len(content) > MaxToolCallArgumentSize+50 {
					t.Errorf("new_content not truncated, len=%d", len(content))
				}
			},
		},
		{
			name:      "多个大字段都被截断",
			input:     `{"content":"` + strings.Repeat("C", 1000) + `","new_content":"` + strings.Repeat("D", 1000) + `"}`,
			wantValid: true,
			checkFunc: func(t *testing.T, result string) {
				var params map[string]interface{}
				if err := json.Unmarshal([]byte(result), &params); err != nil {
					t.Errorf("result is not valid JSON: %v", err)
					return
				}
				for _, field := range []string{"content", "new_content"} {
					if content, ok := params[field].(string); ok {
						if len(content) > MaxToolCallArgumentSize+50 {
							t.Errorf("%s not truncated, len=%d", field, len(content))
						}
					}
				}
			},
		},
		{
			name:      "超大参数返回简化版本",
			input:     `{"operation":"file","path":"test.txt","unknown_field":"` + strings.Repeat("E", 5000) + `"}`,
			wantValid: true,
			checkFunc: func(t *testing.T, result string) {
				var params map[string]interface{}
				if err := json.Unmarshal([]byte(result), &params); err != nil {
					t.Errorf("result is not valid JSON: %v", err)
					return
				}
				// 应该保留 operation 和 path
				if params["operation"] != "file" {
					t.Error("operation field lost in simplified result")
				}
				if params["path"] != "test.txt" {
					t.Error("path field lost in simplified result")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessToolCallArguments(tt.input)

			// 验证结果是有效 JSON
			if tt.wantValid {
				if !json.Valid([]byte(result)) {
					t.Errorf("result is not valid JSON: %s", result)
					return
				}
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestProcessToolResult(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLen   int  // 期望长度（大致）
		truncated bool // 是否应该被截断
	}{
		{
			name:      "短结果不截断",
			input:     "short result",
			wantLen:   len("short result"),
			truncated: false,
		},
		{
			name:      "长结果被截断",
			input:     string(make([]byte, 5000)),
			wantLen:   MaxToolResultSize + 50, // 允许截断标记的额外长度
			truncated: true,
		},
		{
			name:      "刚好等于阈值不截断",
			input:     string(make([]byte, MaxToolResultSize)),
			wantLen:   MaxToolResultSize,
			truncated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessToolResult(tt.input)

			if tt.truncated {
				if len(result) > tt.wantLen {
					t.Errorf("result too long: got %d, want <= %d", len(result), tt.wantLen)
				}
			} else {
				if len(result) != tt.wantLen {
					t.Errorf("result length mismatch: got %d, want %d", len(result), tt.wantLen)
				}
			}
		})
	}
}

// TestIsExecutableToolCallArgs 测试工具调用参数是否满足最基本的执行条件。
func TestIsExecutableToolCallArgs(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
		expected  bool
	}{
		{
			name:      "empty arguments",
			toolName:  "bash",
			arguments: "",
			expected:  false,
		},
		{
			name:      "empty json object",
			toolName:  "bash",
			arguments: "{}",
			expected:  false,
		},
		{
			name:      "null json",
			toolName:  "bash",
			arguments: "null",
			expected:  false,
		},
		{
			name:      "missing bash command",
			toolName:  "bash",
			arguments: `{"args":["pwd"]}`,
			expected:  false,
		},
		{
			name:      "missing skill name",
			toolName:  "skill",
			arguments: `{"path":"demo-skill"}`,
			expected:  false,
		},
		{
			name:      "valid bash command",
			toolName:  "bash",
			arguments: `{"command":"echo","args":["hello"]}`,
			expected:  true,
		},
		{
			name:      "invalid skill name field",
			toolName:  "skill",
			arguments: `{"name":"camera-rotate"}`,
			expected:  false,
		},
		{
			name:      "valid legacy skill field",
			toolName:  "skill",
			arguments: `{"skill":"camera-snapshot"}`,
			expected:  true,
		},
		{
			name:      "unknown tool keeps compatible behavior",
			toolName:  "custom_tool",
			arguments: `{"foo":"bar"}`,
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsExecutableToolCallArgs(tt.toolName, tt.arguments); got != tt.expected {
				t.Fatalf("IsExecutableToolCallArgs() = %v, want %v", got, tt.expected)
			}
		})
	}
}
