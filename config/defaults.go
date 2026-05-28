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

package config

// Agent defaults
const (
	// DefaultMaxStep 默认最大步数
	DefaultMaxStep = 50

	// MaxToolOutputLength 工具输出最大长度
	MaxToolOutputLength = 50000

	// DefaultToolTimeoutSec 默认工具超时时间（秒）
	DefaultToolTimeoutSec = 120

	// DefaultMaxRetries 默认最大重试次数
	DefaultMaxRetries = 3

	// MaxStreamChunks 流式输出最大 chunk 数量，防止无限循环
	MaxStreamChunks = 10000
)

// Model parameter defaults
const (
	// DefaultTemperature 默认温度，避免输出过于随机或过于确定性
	DefaultTemperature float32 = 0.7

	// DefaultTopP 默认TopP，保持输出的多样性
	DefaultTopP float32 = 0.9

	// DefaultFrequencyPenalty 默认频率惩罚，防止重复相同内容
	DefaultFrequencyPenalty float32 = 0.5

	// DefaultPresencePenalty 默认存在惩罚，鼓励谈论新话题
	DefaultPresencePenalty float32 = 0.5
)