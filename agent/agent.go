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
	"math/rand"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego-components-ai/utils/token"
	"github.com/rulego/rulego/api/types"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// ============================================
// 配置结构体
// ============================================

// ChatAgentConfig extends NodeConfiguration with Agent specifics
type ChatAgentConfig struct {
	config.LLMConfig    `json:",squash"`
	MaxStep             int `json:"maxStep" label:"Max Steps" desc:"Maximum number of reasoning-tool loops the agent can perform"`
	MaxToolOutputLength int `json:"maxToolOutputLength" label:"Max Tool Output Length" desc:"Truncate tool output beyond this length to prevent context overflow. Default 50000"`
}

// Desc returns the component description
func (ChatAgentConfig) Desc() string {
	return "ReAct AI agent that iteratively reasons and calls tools to answer queries. Routes to Success/Failure"
}

// SubAgentConfig 子代理配置
type SubAgentConfig struct {
	Name        string              `json:"name" label:"Name" desc:"Sub-agent name used as tool name in the parent agent" required:"true"`
	Description string              `json:"description" label:"Description" desc:"Sub-agent description exposed to the LLM for tool selection"`
	Type        string              `json:"type" label:"Type" desc:"Agent type: rulechain or builtin" required:"true"`
	TargetId    string              `json:"targetId" label:"Target ID" desc:"Target rule chain ID when type is rulechain"`
	Config      types.Configuration `json:"config" label:"Config" desc:"Initialization configuration passed to the sub-agent tool"`
}

// ============================================
// 核心接口
// ============================================

// Agent 核心智能体接口
type Agent interface {
	// Name 返回智能体名称
	Name() string
	// Description 返回智能体描述
	Description() string
	// Tools 返回可用工具列表
	Tools() []*schema.ToolInfo
}

// AgentExecutor 智能体执行器接口
type AgentExecutor interface {
	Agent
	// Generate 同步执行
	Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
	// Stream 流式执行
	Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error)
}

// ToolWrapper 工具包装器接口
type ToolWrapper interface {
	Wrap(tool tool.InvokableTool, opts ToolWrapOptions) tool.InvokableTool
}

// ToolWrapOptions 工具包装选项
type ToolWrapOptions struct {
	Name                string
	AgentId             string
	AgentName           string
	ToolType            aspect.ToolType
	TargetId            string
	AspectManager       *aspect.AspectManager
	MaxStep             int
	MaxToolOutputLength int // 工具输出最大长度
	Logger              types.Logger
	MetricsCollector    *token.MetricsCollector
}

// ============================================
// 公开常量
// ============================================

const (
	// DefaultMaxStep 默认最大步数
	DefaultMaxStep = 50
	// MaxToolOutputLength 工具输出最大长度
	MaxToolOutputLength = 50000
	// DefaultToolTimeout 默认工具超时时间（秒）
	DefaultToolTimeout = 120
)

// ============================================
// 工具函数
// ============================================

// generateShortID 生成一个短的随机 ID 字符串
func generateShortID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// maskAPIKey 隐藏 API Key 的中间部分，只显示前4位和后4位
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// truncateResult 截断工具输出结果，防止超出上下文限制
func truncateResult(result string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = MaxToolOutputLength
	}
	if len(result) > maxLen {
		return result[:maxLen] + fmt.Sprintf("...(truncated, original: %d bytes)", len(result))
	}
	return result
}
