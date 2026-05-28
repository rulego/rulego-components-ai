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
	"time"
)

// Session defaults
const (
	// DefaultMaxMessages max messages per session
	DefaultMaxMessages = 200

	// DefaultMaxTokenCount max token count
	DefaultMaxTokenCount = 128000

	// DefaultKeepRecentCount for compaction
	DefaultKeepRecentCount = 10

	// DefaultTargetTokens for compaction (fallback when percent not set)
	// 当模型上下文大小未知时使用的固定阈值
	DefaultTargetTokens = 76800

	// DefaultTargetTokensPercent 触发压缩的百分比阈值
	// 例如 70 表示当 token 使用达到模型上下文的 70% 时触发压缩
	DefaultTargetTokensPercent = 70

	// DefaultMinMessagesToCompact minimum messages before compaction
	DefaultMinMessagesToCompact = 20

	// MaxToolResultSize 工具结果直接保存的最大字符数
	MaxToolResultSize = 2000

	// MaxToolCallArgumentSize 工具调用参数字段的最大字符数
	// 用于截断 content、new_content 等大字段
	MaxToolCallArgumentSize = 500
)

// Time durations
var (
	// DefaultSessionTTL session time-to-live (30 days)
	DefaultSessionTTL = 30 * 24 * time.Hour

	// DefaultSessionIdleTimeout session idle timeout (1 hour)
	DefaultSessionIdleTimeout = 1 * time.Hour
)

// ProcessToolResult 处理工具结果
// 未超过阈值直接保存，超过则截断
func ProcessToolResult(result string) string {
	if len(result) <= MaxToolResultSize {
		return result
	}
	return result[:MaxToolResultSize] + "\n...[已截断]"
}

// largeArgumentFields 需要截断的大参数字段名
var largeArgumentFields = []string{
	"content",     // write 工具
	"new_content", // edit 工具
	"search",      // edit 工具 (可能很大)
	"replace",     // edit 工具 (可能很大)
	"code",        // code 工具
	"text",        // 通用文本字段
	"body",        // HTTP body
	"data",        // 通用数据字段
}

// ProcessToolCallArguments 处理工具调用参数
// 对大字段进行截断，保持 JSON 格式有效
// 如果解析失败，返回空对象 {}
func ProcessToolCallArguments(arguments string) string {
	if arguments == "" || arguments == "null" {
		return "{}"
	}

	// 尝试解析 JSON
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		// 解析失败，返回空对象（保证是有效 JSON）
		return "{}"
	}

	// 如果参数很短，直接返回
	if len(arguments) <= MaxToolCallArgumentSize {
		return arguments
	}

	// 对大字段进行截断
	modified := false
	for _, field := range largeArgumentFields {
		if val, ok := params[field]; ok {
			if strVal, ok := val.(string); ok && len(strVal) > MaxToolCallArgumentSize {
				params[field] = strVal[:MaxToolCallArgumentSize] + "...[已截断]"
				modified = true
			}
		}
	}

	if !modified {
		// 没有修改，但原始参数太长，返回截断版本
		// 这种情况可能是其他字段很大，整体截断
		if len(arguments) > MaxToolCallArgumentSize*2 {
			// 重新序列化，如果还是太长，返回简化版本
			result, err := json.Marshal(params)
			if err != nil || len(result) > MaxToolCallArgumentSize*2 {
				// 返回只包含操作类型和路径的简化版本
				simplified := make(map[string]interface{})
				if op, ok := params["operation"]; ok {
					simplified["operation"] = op
				}
				if path, ok := params["path"]; ok {
					simplified["path"] = path
				}
				simplified["_truncated"] = true
				result, _ = json.Marshal(simplified)
				return string(result)
			}
			return string(result)
		}
		return arguments
	}

	// 重新序列化
	result, err := json.Marshal(params)
	if err != nil {
		return "{}"
	}
	return string(result)
}

// truncateString 截断字符串辅助函数
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[已截断]"
}

// isLargeField 判断字段是否需要截断
func isLargeField(field string) bool {
	field = strings.ToLower(field)
	for _, f := range largeArgumentFields {
		if strings.Contains(field, f) {
			return true
		}
	}
	return false
}
