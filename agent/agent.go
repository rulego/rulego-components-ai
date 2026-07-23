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

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego-components-ai/utils/token"
	"github.com/rulego/rulego/api/types"
)

// ============================================
// Configure the structure
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

// SubAgentConfig Subagent configuration
type SubAgentConfig struct {
	Name        string              `json:"name" label:"Name" desc:"Sub-agent name used as tool name in the parent agent" required:"true"`
	Description string              `json:"description" label:"Description" desc:"Sub-agent description exposed to the LLM for tool selection"`
	Type        string              `json:"type" label:"Type" desc:"Agent type: rulechain or builtin" required:"true"`
	TargetId    string              `json:"targetId" label:"Target ID" desc:"Target rule chain ID when type is rulechain"`
	Config      types.Configuration `json:"config" label:"Config" desc:"Initialization configuration passed to the sub-agent tool"`
}

// ============================================
// Core interface
// ============================================

// Agent core agent interface
type Agent interface {
	// Name returns the agent name
	Name() string
	// Description Returns a description of the agent
	Description() string
	// Tools returns a list of available tools
	Tools() []*schema.ToolInfo
}

// AgentExecutor agent executor interface
type AgentExecutor interface {
	Agent
	// Generate executes synchronously
	Generate(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
	// Stream execution
	Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error)
}

// ToolWrapper interface
type ToolWrapper interface {
	Wrap(tool tool.InvokableTool, opts ToolWrapOptions) tool.InvokableTool
}

// ToolWrapOptions tool-wrapping options
type ToolWrapOptions struct {
	Name                string
	AgentId             string
	AgentName           string
	ToolType            aspect.ToolType
	TargetId            string
	AspectManager       *aspect.AspectManager
	MaxStep             int
	MaxToolOutputLength int // Tool output maximum length
	Logger              types.Logger
	MetricsCollector    *token.MetricsCollector
}

// ============================================
// Public constants
// ============================================

const (
	// DefaultMaxStep The default maximum number of steps
	DefaultMaxStep = 50
	// MaxToolOutputLength tool outputs the maximum length
	MaxToolOutputLength = 50000
	// DefaultToolTimeout Default Tool Timeout (seconds)
	DefaultToolTimeout = 120
)

// ============================================
// Utility function
// ============================================

// generateShortID generates a short random ID string
func generateShortID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// maskAPIKey hides the middle part of the API Key, showing only the first 4 bits and the last 4 bits
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// truncateResult Truncate the tool's output to prevent exceeding context limits
// maxLen is the maximum rune count; truncate by rune to avoid corrupting UTF-8 characters
func truncateResult(result string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = MaxToolOutputLength
	}
	runes := []rune(result)
	if len(runes) <= maxLen {
		return result
	}
	return string(runes[:maxLen]) + fmt.Sprintf("...(truncated, original: %d bytes)", len(result))
}
