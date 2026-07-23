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

// LLMConfig component configuration
type LLMConfig struct {
	Url             string             `json:"url"`             // Request address
	Key             string             `json:"key"`             // API Key
	Model           string             `json:"model"`           // Model name
	SystemPrompt    string             `json:"systemPrompt"`    // System prompts are used to predefine the basic behavioral framework and response style of the model. You can use the ${} placeholder variable to support ${include("/path/to/file")} to include files (using absolute paths)
	Messages        []ChatMessage      `json:"messages"`        // Context/user message list
	Images          []string           `json:"images"`          // Allows the model to input images and answer user questions based on its understanding of the image content
	Tools           []Tool             `json:"tools"`           // Tool list
	Params          ModelParams        `json:"params"`          //Large model parameters
	MaxRetries      int                `json:"maxRetries"`      // For the same model, the number of retries is zero, meaning the default value is 3. Automatic retry of interrupts for 429/5xx/network errors/timeout/stream establishment
	StreamRetryMode string             `json:"streamRetryMode"` // Streaming Mid-Stream Retry Mode: "off" (default, only retries within the detection window, reserving real-time) / "full" (full buffer replay, sacrificing real-time to replace mid-stream interruptions for retry)
	Failover        []FailoverEndpoint `json:"failover"`        // Failover backup endpoints, by priority; After the main endpoint retries exhausts the limit, it switches sequentially. Empty = Close failover
	// Fuse (effective only when Failover is enabled): The master endpoint retries exhaust and the fuse blows; during cooldown, skip the main and use it as a backup.
	// During a main persistent fault, detection cooldown doubles each time (detection failure doubles, capped at 10 minutes), and if detection succeeds, reset to base cooling.
	CircuitCooldownSec int `json:"circuitCooldownSec"` // Fuse base cooldown seconds: 0 = default 60. During a main persistent fault, the detection cooling is doubled and capped at 10 minutes each time
}

// FailoverEndpoint failover backup endpoint
type FailoverEndpoint struct {
	Url    string       `json:"url"`              // Backup request address
	Key    string       `json:"key"`              // Backup API Key
	Model  string       `json:"model"`            // Alternate model name; empty ones retain the primary model name
	Params *ModelParams `json:"params,omitempty"` // Optional: Override main endpoint parameters; nil = inherits from the master endpoint Params
}

// Streaming mid-stream retry mode (LLMConfig.StreamRetryMode values)
const (
	// StreamRetryOff default: only retries within the probe window, keeping the real-time experience.
	StreamRetryOff = "off"
	// StreamRetryFull full mid-stream retry (buffered replay, sacrificing real-time).
	StreamRetryFull = "full"
)

// ModelParams Large Model Parameters
type ModelParams struct {
	Temperature      float32        `json:"temperature"`      //Sampling temperature controls the randomness of output output. Temperature values within the range of [0.0, 2.0]; the higher the value, the more random and creative the output; The lower the value, the more stable the output.
	TopP             float32        `json:"topP"`             // The sampling method ranges from [0.0, 1.0]. top_p Value determination: The model selects tokens from the top p% candidate terms with the highest probability; When top_p is 0, this parameter is invalid.
	PresencePenalty  float32        `json:"presencePenalty"`  //There is a penalty imposed on the logarithmic probability of existing markers in the text. Value range: [0.0, 1.0]
	FrequencyPenalty float32        `json:"frequencyPenalty"` //Frequency penalty: Punishes the logarithmic probability of markings appearing in the text. Value range: [0.0, 1.0]
	MaxTokens        int            `json:"maxTokens"`        // Maximum output length
	Stop             []string       `json:"stop"`             // Marker when the model stops output
	ResponseFormat   string         `json:"responseFormat"`   // Format of output results. Supported: text, json_object, json_schema. The default is text.
	JsonSchema       string         `json:"jsonSchema"`       // JSON Schema
	KeepThink        bool           `json:"keepThink"`        //Whether to keep the thought process and only apply to the text response format
	ExtraFields      map[string]any `json:"extraFields"`      // Extension fields used to pass model-specific parameters. For example: thinking_type, thinking_budget_tokens, reasoning_effort, etc
}

// ChatMessage Context Messages / User Messages
// Supports OpenAI standard format: content can be strings or arrays
type ChatMessage struct {
	Role       string         `json:"role"`                   // Message role: user/assistant/system/tool
	Content    interface{}    `json:"content"`                // News content. It can be a string or a []ContentPart array (OpenAI multimodal format)
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`   // Tool call history for assistant messages
	ToolCallID string         `json:"tool_call_id,omitempty"` // tool call ID associated with the tool message
}

// ChatToolCall OpenAI compatible tool call structure.
type ChatToolCall struct {
	ID       string           `json:"id"`       // Tool call ID
	Type     string           `json:"type"`     // Tool call type, usually function
	Function ChatFunctionCall `json:"function"` // Tool function information
}

// ChatFunctionCall OpenAI-compatible function call structure.
type ChatFunctionCall struct {
	Name      string `json:"name"`      // Tool name
	Arguments string `json:"arguments"` // JSON string parameters
}

// ContentPart OpenAI's standard message content section
type ContentPart struct {
	Type     string    `json:"type"`      // Type: text, image_url
	Text     string    `json:"text"`      // Text content (when type=text)
	ImageURL *ImageURL `json:"image_url"` // Image URL (type=image_url time)
}

// ImageURL image URL structure
type ImageURL struct {
	URL    string `json:"url"`    // Image URL
	Detail string `json:"detail"` // Image detail levels: auto, low, high
}

// GetContentAsString retrieves the message content as a string
func (m *ChatMessage) GetContentAsString() string {
	if m.Content == nil {
		return ""
	}
	switch v := m.Content.(type) {
	case string:
		return v
	case []interface{}:
		// OpenAI array format, extracting the text section
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

// GetContentParts retrieves message content as an array of ContentParts (OpenAI format)
func (m *ChatMessage) GetContentParts() []ContentPart {
	if m.Content == nil {
		return nil
	}

	switch v := m.Content.(type) {
	case string:
		// String format, returns a single text part
		if v != "" {
			return []ContentPart{{Type: "text", Text: v}}
		}
		return nil
	case []interface{}:
		// OpenAI array format
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

// IsMultimodal checks whether messages contain multimodal content (image)
func (m *ChatMessage) IsMultimodal() bool {
	parts := m.GetContentParts()
	for _, part := range parts {
		if part.Type == "image_url" && part.ImageURL != nil && part.ImageURL.URL != "" {
			return true
		}
	}
	return false
}

// GetAllImages retrieves all image URLs in the message
func (m *ChatMessage) GetAllImages() []string {
	var images []string

	// Extracted from an array of content in OpenAI format
	parts := m.GetContentParts()
	for _, part := range parts {
		if part.Type == "image_url" && part.ImageURL != nil && part.ImageURL.URL != "" {
			images = append(images, part.ImageURL.URL)
		}
	}

	return images
}

// MultiTurnChatRequest Multi-turn Conversation Request Struct
type MultiTurnChatRequest struct {
	Model    string        `json:"model"`    // Model name, optional
	Messages []ChatMessage `json:"messages"` // Talk to the message list
	Stream   bool          `json:"stream"`   // Enable streaming response, optional
	Params   *ModelParams  `json:"params"`   // Large model parameters, optional
	Tools    []Tool        `json:"tools"`    // Tool list, optional
}

// Tool configuration
type Tool struct {
	Type        string              `json:"type"`        // Tool types: rulechain, builtin, agent, mcp
	Name        string              `json:"name"`        // Tool name
	Description string              `json:"description"` // Tool description
	TargetId    string              `json:"targetId"`    // Target rulechain ID (used when type=rulechain/agent)
	Parameters  string              `json:"parameters"`  // Tool parameter JSON Schema
	Config      types.Configuration `json:"config"`      // Tool initialization configuration supports ${global.xxx} variable replacement
	Timeout     int64               `json:"timeout"`     // Timeout time (milliseconds), default 120,000 (120 seconds)
}

const (
	// ToolTypeRuleChain tool type
	ToolTypeRuleChain = "rulechain"
	// ToolTypeBuiltin includes built-in tool types
	ToolTypeBuiltin = "builtin"
	// ToolTypeAgent sub-agent type (semantic alias for rulechain, used to call sub-agents)
	ToolTypeAgent = "agent"
	// ToolTypeMCP is the MCP tool type, discovering and loading the tool from the MCP Server.
	// Supports both self (in-process) and remote (http/stdio) modes.
	// Self mode obtains MCPToolProvider via RuleConfig UDF to achieve zero network calls.
	// Remote mode automatically discovers all tools through the MCP protocol's tools/list.
	ToolTypeMCP = "mcp"

	// DefaultRole
	DefaultRole = "user"
	// DefaultResponseFormat: The default response format
	DefaultResponseFormat = "text"
	// ResponseFormatJSONObject JSON object response format
	ResponseFormatJSONObject = "json_object"
	// ResponseFormatJSONSchema JSON Schema response format
	ResponseFormatJSONSchema = "json_schema"
	// DefaultMaxStep Default maximum iteration count - defined in defaults.go
	// KeyStream flag key
	KeyStream = "stream"
	// KeyModel model name key
	KeyModel = "model"
	// KeyPromptTokens Enter the token key
	KeyPromptTokens = "prompt_tokens"
	// KeyCompletionTokens outputs the token key
	KeyCompletionTokens = "completion_tokens"
	// KeyTotalTokens Total token key
	KeyTotalTokens = "total_tokens"
	// KeyCachedTokens caches token keys
	KeyCachedTokens = "cached_tokens"
	// KeyToolCalls: The tool calls key
	KeyToolCalls = "tool_calls"
	// KeyFinishReason key
	KeyFinishReason = "finish_reason"
	// KeyChunk data block key
	KeyChunk = "chunk"
	// KeyStreamCompleted key
	KeyStreamCompleted = "stream_completed"
	// KeyStreamStart key
	KeyStreamStart = "stream_start"
	// KeyReasoningContent: Thinking process content key
	KeyReasoningContent = "reasoning_content"
	// KeyToolCall is a single tool call key
	KeyToolCall = "tool_call"
	// KeyFullContent Full Content key, used to mark the final message of the stream request (containing the full merged content)
	KeyFullContent = "full_content"
	// MsgTypeToolCall tool calls message types
	MsgTypeToolCall = "TOOL_CALL"
	// TypeFunction type
	TypeFunction = "function"
	// FinishReasonToolCalls: The reason for the end of the tool call
	FinishReasonToolCalls = "tool_calls"
	// KeyRuleConfig Rule configuration key
	KeyRuleConfig = "rule_config"

	// ValueTrue truth string
	ValueTrue = "true"
	// ValueFalse is a false value string
	ValueFalse = "false"

	// ShareRuleContextKey is used to pass RuleContext within a Context
	ShareRuleContextKey = "share_rule_context"
)

// Default model parameters - defined in defaults.go

// ModelCapability: Model capability type
type ModelCapability string

const (
	// CapabilityVision Visual Capability (supports image input)
	CapabilityVision ModelCapability = "vision"
	// CapabilityFunctionCalling function call capability
	CapabilityFunctionCalling ModelCapability = "function_calling"
	// CapabilityStreaming: streaming output capability
	CapabilityStreaming ModelCapability = "streaming"
	// CapabilityReasoning (such as deep thinking in the o1 series)
	CapabilityReasoning ModelCapability = "reasoning"
	// CapabilityEmbedding: Vector embedding capability
	CapabilityEmbedding ModelCapability = "embedding"
)

// ModelInfo Model information
type ModelInfo struct {
	Name         string            // Model name
	Capabilities []ModelCapability // Model Competency List
}

// modelCapabilityRegistry (application layer can override built-in configurations)
// key: model name (lowercase), value: capability list
var modelCapabilityRegistry = make(map[string][]ModelCapability)
var capabilityRegistryMutex sync.RWMutex

// defaultModelCapabilities provides the built-in default model capability configuration
// The application layer can be overridden by RegisterModelCapabilities
var defaultModelCapabilities = map[string][]ModelCapability{
	// === OpenAI Series ===
	"gpt-4o":       {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-4-turbo":  {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-4-vision": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-4":        {CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-4-32k":    {CapabilityFunctionCalling, CapabilityStreaming},
	"gpt-3.5":      {CapabilityFunctionCalling, CapabilityStreaming},
	"o1":           {CapabilityVision, CapabilityReasoning},
	"o3":           {CapabilityVision, CapabilityReasoning},
	"o4":           {CapabilityVision, CapabilityReasoning},

	// === Claude Series (all visual supports) ===
	"claude-3": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"claude-4": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},

	// === Zhipu GLM Series ===
	"glm-4v":       {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"glm-4.6v":     {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"glm-4-vision": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"glm-4":        {CapabilityFunctionCalling, CapabilityStreaming},
	"glm-3":        {CapabilityStreaming},
	"glm-5":        {CapabilityFunctionCalling, CapabilityStreaming},
	"chatglm":      {CapabilityStreaming},

	// === Alibaba Tongyi Series ===
	"qwen-vl":    {CapabilityVision, CapabilityStreaming},
	"qwen2-vl":   {CapabilityVision, CapabilityStreaming},
	"qwen2.5-vl": {CapabilityVision, CapabilityStreaming},
	"qvq":        {CapabilityVision, CapabilityReasoning},
	"qwen-turbo": {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"qwen-plus":  {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"qwen-max":   {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"qwen3":      {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"qwen3.5":    {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},

	// === Google Series ===
	"gemini":  {CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming},
	"gemma-3": {CapabilityVision, CapabilityStreaming},

	// === Other Common Models ===
	"llama-2":  {CapabilityStreaming},
	"llama-3":  {CapabilityStreaming},
	"mistral":  {CapabilityFunctionCalling, CapabilityStreaming},
	"deepseek": {CapabilityFunctionCalling, CapabilityStreaming, CapabilityReasoning},
	"llava":    {CapabilityVision},
	"cogvlm":   {CapabilityVision},
	"internvl": {CapabilityVision},
	"yi-vl":    {CapabilityVision},
}

// RegisterModelCapabilities: The ability to register individual models
// The application layer calls this function to register or overwrite the model's ability list
func RegisterModelCapabilities(modelName string, capabilities []ModelCapability) {
	capabilityRegistryMutex.Lock()
	defer capabilityRegistryMutex.Unlock()
	modelCapabilityRegistry[strings.ToLower(modelName)] = capabilities
}

// RegisterModelCapabilitiesFromConfig Batch registration model capabilities
// When the application layer starts, it reads from the configuration file and calls this function for batch registration
func RegisterModelCapabilitiesFromConfig(models []ModelInfo) {
	capabilityRegistryMutex.Lock()
	defer capabilityRegistryMutex.Unlock()
	for _, m := range models {
		modelCapabilityRegistry[strings.ToLower(m.Name)] = m.Capabilities
	}
}

// UnregisterModelCapabilities
func UnregisterModelCapabilities(modelName string) {
	capabilityRegistryMutex.Lock()
	defer capabilityRegistryMutex.Unlock()
	delete(modelCapabilityRegistry, strings.ToLower(modelName))
}

// ClearModelCapabilitiesRegistry Clears the model capabilities registry
// Used to clear old data when reloading configurations
func ClearModelCapabilitiesRegistry() {
	capabilityRegistryMutex.Lock()
	defer capabilityRegistryMutex.Unlock()
	modelCapabilityRegistry = make(map[string][]ModelCapability)
}

// GetModelCapabilities: A list of the capabilities of a model to obtain
// Testing sequence: 1. Application Layer Registry (Override) -> 2. Built-in default configuration -> 3. Conservative default ability
func GetModelCapabilities(modelName string) []ModelCapability {
	if modelName == "" {
		return nil
	}
	modelLower := strings.ToLower(modelName)

	// 1. Check the application layer registry (can override default configurations)
	capabilityRegistryMutex.RLock()
	if caps, ok := modelCapabilityRegistry[modelLower]; ok {
		capabilityRegistryMutex.RUnlock()
		return caps
	}
	capabilityRegistryMutex.RUnlock()

	// 2. Check the built-in default configuration (mode matching)
	// Use "Longest Match First" to avoid mismatched long model names in short modes (e.g., glm-4 hitting glm-4-vision)
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

	// 3. Conservative default capability: Assuming support for all common abilities (avoiding missing features)
	return []ModelCapability{CapabilityVision, CapabilityFunctionCalling, CapabilityStreaming}
}

// HasCapability detects whether the model has specified capabilities
func HasCapability(modelName string, capability ModelCapability) bool {
	capabilities := GetModelCapabilities(modelName)
	for _, cap := range capabilities {
		if cap == capability {
			return true
		}
	}
	return false
}

// SupportsVision checks whether the model supports visual (image) capabilities
func SupportsVision(modelName string) bool {
	return HasCapability(modelName, CapabilityVision)
}

// SupportsFunctionCalling checks whether the model supports function call capability
func SupportsFunctionCalling(modelName string) bool {
	return HasCapability(modelName, CapabilityFunctionCalling)
}

// SupportsReasoning checks whether the model supports reasoning capabilities
func SupportsReasoning(modelName string) bool {
	return HasCapability(modelName, CapabilityReasoning)
}
