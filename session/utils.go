package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GenerateMessageID 生成消息ID
func GenerateMessageID() string {
	return fmt.Sprintf("msg_%s_%d", uuid.New().String()[:8], time.Now().UnixNano())
}

// GenerateToolCallID 生成工具调用ID
func GenerateToolCallID() string {
	return fmt.Sprintf("call_%s_%d", uuid.New().String()[:8], time.Now().UnixNano())
}

// NewSessionMessage 创建新的会话消息
func NewSessionMessage(role, content string) *SessionMessage {
	return &SessionMessage{
		ID:          GenerateMessageID(),
		Role:        role,
		Content:     content,
		TokenCount:  0,
		IsCompacted: false,
		CreatedAt:   time.Now(),
	}
}

// NewSessionMessageWithTokens 创建带Token计数的会话消息
func NewSessionMessageWithTokens(role, content string, tokenCount int) *SessionMessage {
	return &SessionMessage{
		ID:          GenerateMessageID(),
		Role:        role,
		Content:     content,
		TokenCount:  tokenCount,
		IsCompacted: false,
		CreatedAt:   time.Now(),
	}
}

// NewToolCall 创建新的工具调用
func NewToolCall(name, arguments string) *ToolCall {
	return &ToolCall{
		ID:          GenerateToolCallID(),
		Name:        name,
		Arguments:   arguments,
		Status:      ToolCallStatusPending,
		CreatedAt:   time.Now(),
		CompletedAt: nil,
	}
}

// IsExecutableToolCallArgs 判断工具调用参数是否满足最基本的执行条件。
func IsExecutableToolCallArgs(toolName, arguments string) bool {
	if strings.TrimSpace(toolName) == "" {
		return false
	}

	trimmedArgs := strings.TrimSpace(arguments)
	if trimmedArgs == "" || trimmedArgs == "null" {
		return false
	}

	var payload any
	if err := json.Unmarshal([]byte(trimmedArgs), &payload); err != nil {
		return false
	}

	switch strings.TrimSpace(toolName) {
	case "bash":
		params, ok := payload.(map[string]any)
		if !ok {
			return false
		}
		return hasNonEmptyStringField(params, "command")
	case "skill":
		params, ok := payload.(map[string]any)
		if !ok {
			return false
		}
		return hasNonEmptyStringField(params, "skill")
	default:
		return true
	}
}

// hasNonEmptyStringField 判断对象中指定字段是否为非空字符串。
func hasNonEmptyStringField(params map[string]any, field string) bool {
	value, ok := params[field]
	if !ok {
		return false
	}

	strValue, ok := value.(string)
	if !ok {
		return false
	}

	return strings.TrimSpace(strValue) != ""
}
