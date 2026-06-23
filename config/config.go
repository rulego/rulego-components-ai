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

package config

import (
	"strings"
	"sync"

	"github.com/rulego/rulego/api/types"
)

// LLMConfig 组件配置
type LLMConfig struct {
	Url          string        `json:"url"`          // 请求地址
	Key          string        `json:"key"`          // API Key
	Model        string        `json:"model"`        // 模型名称
	SystemPrompt string        `json:"systemPrompt"` // 系统提示，用于预先定义模型的基础行为框架和响应风格。可以使用${} 占位符变量，支持 ${include("/path/to/file")} 包含文件（使用绝对路径）
	Messages     []ChatMessage `json:"messages"`     // 上下文/用户消息列表
	Images       []string      `json:"images"`       // 允许模型输入图片，并根据图像内容的理解回答用户问题
	Params       ModelParams   `json:"params"`       //大模型参数
	Tools        []Tool        `json:"tools"`        // 工具列表
	MaxRetries   int           `json:"maxRetries"`   // 最大重试次数，0 表示不重试，默认 3。对 429/5xx/网络错误/超时自动重试
}

// ModelParams 大模型参数
type ModelParams struct {
	Temperature      float32        `json:"temperature"`      //采样温度控制输出的随机性。温度值在 [0.0, 2.0] 范围内，值越高，输出越随机和创造性；值越低，输出越稳定。
	TopP             float32        `json:"topP"`             // 采样方法的取值范围为 [0.0,1.0]。top_p 值确定模型从概率最高的前p%的候选词中选取 tokens；当 top_p 为 0 时，此参数无效。
	PresencePenalty  float32        `json:"presencePenalty"`  //存在惩罚 对文本中已有的标记的对数概率施加惩罚。取值范围[0.0,1.0]
	FrequencyPenalty float32        `json:"frequencyPenalty"` //频率惩罚 对文本中出现的标记的对数概率施加惩罚。取值范围[0.0,1.0]
	MaxTokens        int            `json:"maxTokens"`        // 最大输出长度
	Stop             []string       `json:"stop"`             // 模型停止输出的标记
	ResponseFormat   string         `json:"responseFormat"`   // 输出结果的格式。支持：text、json_object、json_schema。默认为 text。
	JsonSchema       string         `json:"jsonSchema"`       // JSON Schema
	KeepThink        bool           `json:"keepThink"`        //是否保留思考过程，只对text响应格式生效
	ExtraFields      map[string]any `json:"extraFields"`      // 扩展字段，用于传递模型特定参数。例如：thinking_type, thinking_budget_tokens, reasoning_effort 等
}

// ChatMessage 上下文消息/用户消息
// 支持 OpenAI 标准格式：content 可以是字符串或数组
type ChatMessage struct {
	Role       string         `json:"role"`                    // 消息角色 user/assistant/system/tool
	Content    interface{}    `json:"content"`                 // 消息内容。可以是字符串或 []ContentPart 数组（OpenAI 多模态格式）
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`    // assistant 消息的工具调用历史
	ToolCallID string         `json:"tool_call_id,omitempty"`  // tool 消息关联的 tool call ID
}

// ChatToolCall OpenAI 兼容的工具调用结构。
type ChatToolCall struct {
	ID       string           `json:"id"`       // 工具调用 ID
	Type     string           `json:"type"`     // 工具调用类型，通常为 function
	Function ChatFunctionCall `json:"function"` // 工具函数信息
}

// ChatFunctionCall OpenAI 兼容的函数调用结构。
type ChatFunctionCall struct {
	Name      string `json:"name"`      // 工具名称
	Arguments string `json:"arguments"` // JSON 字符串参数
}

// ContentPart OpenAI 标准的消息内容部分
type ContentPart struct {
	Type     string    `json:"type"`      // 类型：text, image_url
	Text     string    `json:"text"`      // 文本内容（type=text 时）
	ImageURL *ImageURL `json:"image_url"` // 图片URL（type=image_url 时）
}

// ImageURL 图片URL结构
type ImageURL struct {
	URL    string `json:"url"`    // 图片URL
	Detail string `json:"detail"` // 图片细节级别：auto, low, high
}

// GetContentAsString 获取消息内容作为字符串
func (m *ChatMessage) GetContentAsString() string {
	if m.Content == nil {
		return ""
	}
	switch v := m.Content.(type) {
	case string:
		return v
	case []interface{}:
		// OpenAI 数组格式，提取文本部分
		for _, item := range v {
			if part, ok := item.(map[string]interface{}); ok {
				if partType, _ := part["type"].(string); partType == "text" {
					if text, _ := part["text"].(string); text != "" {
						return text
					}
				}
			}
		}
		return ""
	default:
		return ""
	}
}

// GetContentParts 获取消息内容作为 ContentPart 数组（OpenAI 格式）
func (m *ChatMessage) GetContentParts() []ContentPart {
	if m.Content == nil {
		return nil
	}

	switch v := m.Content.(type) {
	case string:
		// 字符串格式，返回单个文本部分
		if v != "" {
			return []ContentPart{{Type: "text", Text: v}}
		}
		return nil
	case []interface{}:
		// OpenAI 数组格式
		var parts []ContentPart
		for _, item := range v {
			if part, ok := item.(map[string]interface{}); ok {
				cp := ContentPart{}
				if partType, _ := part["type"].(string); partType != "" {
					cp.Type = partType
				}
				if text, _ := part["text"].(string); text != "" {
					cp.Text = text
				}
				if imgURL, _ := part["image_url"].(map[string]interface{}); imgURL != nil {
					cp.ImageURL = &ImageURL{}
					if url, _ := imgURL["url"].(string); url != "" {
						cp.ImageURL.URL = url
					}
					if detail, _ := imgURL["detail"].(string); detail != "" {
						cp.ImageURL.Detail = detail
					}
				}
				parts = append(parts, cp)
			}
		}
		return parts
	default:
		return nil
	}
}

// IsMultimodal 检查消息是否包含多模态内容（图片）
func (m *ChatMessage) IsMultimodal() bool {
	parts := m.GetContentParts()
	for _, part := range parts {
		if part.Type == "image_url" && part.ImageURL != nil && part.ImageURL.URL != "" {
			return true
		}
	}
	return false
}

// GetAllImages 获取消息中的所有图片URL
func (m *ChatMessage) GetAllImages() []string {
	var images []string

	// 从 OpenAI 格式的 content 数组中提取
	parts := m.GetContentParts()
	for _, part := range parts {
		if part.Type == "image_url" && part.ImageURL != nil && part.ImageURL.URL != "" {
			images = append(images, part.ImageURL.URL)
		}
	}

	return images
}

// MultiTurnChatRequest 多轮对话请求结构体
type MultiTurnChatRequest struct {
	Model    string        `json:"model"`    // 模型名称，可选
	Messages []ChatMessage `json:"messages"` // 对话消息列表
	Stream   bool          `json:"stream"`   // 是否启用流式响应，可选
	Params   *ModelParams  `json:"params"`   // 大模型参数，可选
	Tools    []Tool        `json:"tools"`    // 工具列表，可选
}

// Tool 工具配置
type Tool struct {
	Type        string              `json:"type"`        // 工具类型：rulechain, builtin, agent, mcp
	Name        string              `json:"name"`        // 工具名称
	Description string              `json:"description"` // 工具描述
	TargetId    string              `json:"targetId"`    // 目标规则链ID（type=rulechain/agent时使用）
	Parameters  string              `json:"parameters"`  // 工具参数JSON Schema
	Config      types.Configuration `json:"config"`      // 工具初始化配置，支持 ${global.xxx} 变量替换
	Timeout     int64               `json:"timeout"`     // 超时时间（毫秒），默认 120000 (120秒)
}

const (
	// ToolTypeRuleChain 规则链工具类型
	ToolTypeRuleChain = "rulechain"
	// ToolTypeBuiltin 内置工具类型
	ToolTypeBuiltin = "builtin"
	// ToolTypeAgent 子智能体类型（rulechain的语义别名，用于调用子智能体）
	ToolTypeAgent = "agent"
	// ToolTypeMCP MCP 工具类型，从 MCP Server 发现并加载工具。
	// 支持 self（进程内）和远程（http/stdio）两种模式。
	// self 模式通过 RuleConfig UDF 获取 MCPToolProvider 实现零网络调用。
	// 远程模式通过 MCP 协议的 tools/list 自动发现全部工具。
	ToolTypeMCP = "mcp"

	// DefaultRole 默认角色
	DefaultRole = "user"
	// DefaultResponseFormat 默认响应格式
	DefaultResponseFormat = "text"
	// ResponseFormatJSONObject JSON对象响应格式
	ResponseFormatJSONObject = "json_object"
	// ResponseFormatJSONSchema JSON Schema响应格式
	ResponseFormatJSONSchema = "json_schema"
	// DefaultMaxStep 默认最大迭代次数 - 定义在 defaults.go 中
	// KeyStream 流式标志键
	KeyStream = "stream"
	// KeyToolCalls 工具调用键
	KeyToolCalls = "tool_calls"
	// KeyFinishReason 结束原因键
	KeyFinishReason = "finish_reason"
	// KeyChunk 数据块键
	KeyChunk = "chunk"
	// KeyStreamCompleted 流式完成键
	KeyStreamCompleted = "stream_completed"
	// KeyStreamStart 流式开始键
	KeyStreamStart = "stream_start"
	// KeyReasoningContent 思考过程内容键
	KeyReasoningContent = "reasoning_content"
	// KeyToolCall 单个工具调用键
	KeyToolCall = "tool_call"
	// KeyFullContent 完整内容键，用于标记流式请求的最终消息（包含完整合并内容）
	KeyFullContent = "full_content"
	// MsgTypeToolCall 工具调用消息类型
	MsgTypeToolCall = "TOOL_CALL"
	// TypeFunction 函数类型
	TypeFunction = "function"
	// FinishReasonToolCalls 工具调用结束原因
	FinishReasonToolCalls = "tool_calls"
	// KeyRuleConfig 规则配置键
	KeyRuleConfig = "rule_config"

	// ValueTrue 真值字符串
	ValueTrue = "true"
	// ValueFalse 假值字符串
	ValueFalse = "false"

	// ShareRuleContextKey 用于在 Context 中传递 RuleContext
	ShareRuleContextKey = "share_rule_context"
)

// Default model parameters - 定义在 defaults.go 中

// ModelCapability 模型能力类型
type ModelCapability string

const (
	// CapabilityVision 视觉能力（支持图片输入）
	CapabilityVision ModelCapability = "vision"
	// CapabilityFunctionCalling 函数调用能力
	CapabilityFunctionCalling ModelCapability = "function_calling"
	// CapabilityStreaming 流式输出能力
	CapabilityStreaming ModelCapability = "streaming"
	// CapabilityReasoning 推理能力（如 o1 系列的深度思考）
	CapabilityReasoning ModelCapability = "reasoning"
	// CapabilityEmbedding 向量嵌入能力
	CapabilityEmbedding ModelCapability = "embedding"
)

// ModelInfo 模型信息
type ModelInfo struct {
	Name         string            // 模型名称
	Capabilities []ModelCapability // 模型能力列表
}

// modelCapabilityRegistry 模型能力注册表（应用层可覆盖内置配置）
// key: 模型名称（小写）, value: 能力列表
var modelCapabilityRegistry = make(map[string][]ModelCapability)
var capabilityRegistryMutex sync.RWMutex

// defaultModelCapabilities 内置的默认模型能力配置
// 应用层可以通过 RegisterModelCapabilities 覆盖
var defaultModelCapabilities = map[string][]ModelCapability{
	// === OpenAI 系列 ===
	"gpt-4o":       {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-4-turbo":  {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-4-vision": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-4":        {CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-4-32k":    {CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-3.5":      {CapabilityFunctionCalling, CapabilityStreaming},
	"o1":           {CapabilityVision, CapabilityReasoning},
	"o3":           {CapabilityVision, CapabilityReasoning},
	"o4":           {CapabilityVision, CapabilityReasoning},

	// === Claude 系列（全部支持视觉）===
	"claude-3": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"claude-4": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},

	// === 智谱 GLM 系列 ===
	"glm-4v":       {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"glm-4.6v":     {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"glm-4-vision": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"glm-4":        {CapabilityFunctionCalling, CapabilityStreaming},
	"glm-3":        {CapabilityStreaming},
	"glm-5":        {CapabilityFunctionCalling, CapabilityStreaming},
	"chatglm":      {CapabilityStreaming},

	// === 阿里通义系列 ===
	"qwen-vl":    {CapabilityVision, CapabilityStreaming},
	"qwen2-vl":   {CapabilityVision, CapabilityStreaming},
	"qwen2.5-vl": {CapabilityVision, CapabilityStreaming},
	"qvq":        {CapabilityVision, CapabilityReasoning},
	"qwen-turbo": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"qwen-plus":  {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"qwen-max":   {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"qwen3":      {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"qwen3.5":    {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},

	// === Google 系列 ===
	"gemini":  {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"gemma-3": {CapabilityVision, CapabilityStreaming},

	// === 其他常见模型 ===
	"llama-2":  {CapabilityStreaming},
	"llama-3":  {CapabilityStreaming},
	"mistral":  {CapabilityFunctionCalling, CapabilityStreaming},
	"deepseek": {CapabilityFunctionCalling, CapabilityStreaming, CapabilityReasoning},
	"llava":    {CapabilityVision},
	"cogvlm":   {CapabilityVision},
	"internvl": {CapabilityVision},
	"yi-vl":    {CapabilityVision},
}

// RegisterModelCapabilities 注册单个模型的能力
// 应用层调用此函数注册或覆盖模型的能力列表
func RegisterModelCapabilities(modelName string, capabilities []ModelCapability) {
	capabilityRegistryMutex.Lock()
	defer capabilityRegistryMutex.Unlock()
	modelCapabilityRegistry[strings.ToLower(modelName)] = capabilities
}

// RegisterModelCapabilitiesFromConfig 批量注册模型能力
// 应用层启动时从配置文件读取并调用此函数批量注册
func RegisterModelCapabilitiesFromConfig(models []ModelInfo) {
	capabilityRegistryMutex.Lock()
	defer capabilityRegistryMutex.Unlock()
	for _, m := range models {
		modelCapabilityRegistry[strings.ToLower(m.Name)] = m.Capabilities
	}
}

// UnregisterModelCapabilities 取消注册模型能力
func UnregisterModelCapabilities(modelName string) {
	capabilityRegistryMutex.Lock()
	defer capabilityRegistryMutex.Unlock()
	delete(modelCapabilityRegistry, strings.ToLower(modelName))
}

// ClearModelCapabilitiesRegistry 清空模型能力注册表
// 用于重新加载配置时清空旧数据
func ClearModelCapabilitiesRegistry() {
	capabilityRegistryMutex.Lock()
	defer capabilityRegistryMutex.Unlock()
	modelCapabilityRegistry = make(map[string][]ModelCapability)
}

// GetModelCapabilities 获取模型的能力列表
// 检测顺序：1. 应用层注册表（覆盖） -> 2. 内置默认配置 -> 3. 保守默认能力
func GetModelCapabilities(modelName string) []ModelCapability {
	if modelName == "" {
		return nil
	}
	modelLower := strings.ToLower(modelName)

	// 1. 检查应用层注册表（可覆盖默认配置）
	capabilityRegistryMutex.RLock()
	if caps, ok := modelCapabilityRegistry[modelLower]; ok {
		capabilityRegistryMutex.RUnlock()
		return caps
	}
	capabilityRegistryMutex.RUnlock()

	// 2. 检查内置默认配置（模式匹配）
	// 使用“最长匹配优先”避免短模式误匹配长模型名（例如 glm-4 命中 glm-4-vision）
	var (
		matchedCaps  []ModelCapability
		matchedLen   int
		hasBestMatch bool
	)
	for pattern, caps := range defaultModelCapabilities {
		if strings.Contains(modelLower, pattern) {
			if !hasBestMatch || len(pattern) > matchedLen {
				matchedCaps = caps
				matchedLen = len(pattern)
				hasBestMatch = true
			}
		}
	}
	if hasBestMatch {
		return matchedCaps
	}

	// 3. 保守默认能力：假设支持所有常见能力（避免丢失功能）
	return []ModelCapability{CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming}
}

// HasCapability 检测模型是否具有指定能力
func HasCapability(modelName string, capability ModelCapability) bool {
	capabilities := GetModelCapabilities(modelName)
	for _, cap := range capabilities {
		if cap == capability {
			return true
		}
	}
	return false
}

// SupportsVision 检测模型是否支持视觉（图片）能力
func SupportsVision(modelName string) bool {
	return HasCapability(modelName, CapabilityVision)
}

// SupportsFunctionCalling 检测模型是否支持函数调用能力
func SupportsFunctionCalling(modelName string) bool {
	return HasCapability(modelName, CapabilityFunctionCalling)
}

// SupportsReasoning 检测模型是否支持推理能力
func SupportsReasoning(modelName string) bool {
	return HasCapability(modelName, CapabilityReasoning)
}
