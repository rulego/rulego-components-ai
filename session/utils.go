package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GenerateMessageID generates the message ID
func GenerateMessageID() string {
	return fmt.Sprintf("msg_%s_%d", uuid.New().String()[:8], time.Now().UnixNano())
}

// GenerateToolCallID generates the tool call ID
func GenerateToolCallID() string {
	return fmt.Sprintf("call_%s_%d", uuid.New().String()[:8], time.Now().UnixNano())
}

// NewSessionMessage creates a new session message
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

// NewSessionMessageWithTokens creates session messages with a Token count
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

// NewToolCall creates a new tool call
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

// IsExecutableToolCallArgs checks whether the tool call parameters meet the most basic execution conditions.
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

// hasNonEmptyStringField checks whether the specified field in the object is a non-empty string.
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
