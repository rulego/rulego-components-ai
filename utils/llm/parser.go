package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/utils/el"
)

// ChatMessageTemplate Contextual message/user message template
type ChatMessageTemplate struct {
	Role            string
	ContentTemplate el.Template
}

// ParseMultiTurnChatRequest parses multi-turn conversation requests
func ParseMultiTurnChatRequest(msgData string) (*config.MultiTurnChatRequest, []ChatMessageTemplate, error) {
	var chatRequest config.MultiTurnChatRequest
	if err := json.Unmarshal([]byte(msgData), &chatRequest); err != nil {
		return nil, nil, err
	}

	if len(chatRequest.Messages) == 0 {
		return nil, nil, fmt.Errorf("messages字段不能为空")
	}

	var messagesFromData []ChatMessageTemplate
	for _, msg := range chatRequest.Messages {
		if strings.TrimSpace(msg.Role) == "" {
			msg.Role = config.DefaultRole
		}
		tmpl, err := el.NewTemplate(msg.Content)
		if err != nil {
			return nil, nil, err
		}
		messagesFromData = append(messagesFromData, ChatMessageTemplate{
			Role:            msg.Role,
			ContentTemplate: tmpl,
		})
	}

	return &chatRequest, messagesFromData, nil
}
