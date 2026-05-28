package session

import (
	"fmt"
	"strings"
)

// GenerateSessionKey 生成会话键
// 格式: agent:{agentId}:channel:{channel}:scope:{scopeType}:{scopeId}
func GenerateSessionKey(agentId, channel string, scope SessionScope, scopeId string) string {
	if scopeId == "" {
		scopeId = "default"
	}
	return fmt.Sprintf("agent:%s:channel:%s:scope:%s:%s", agentId, channel, scope, scopeId)
}

// ParseSessionKey 解析会话键
// 格式: agent:{agentId}:channel:{channel}:scope:{scopeType}:{scopeId}
func ParseSessionKey(key string) (agentId, channel string, scope SessionScope, scopeId string, err error) {
	parts := strings.Split(key, ":")
	// 格式: agent:{agentId}:channel:{channel}:scope:{scopeType}:{scopeId}
	// 这会生成 7 个部分
	if len(parts) != 7 || parts[0] != "agent" || parts[2] != "channel" || parts[4] != "scope" {
		err = fmt.Errorf("invalid session key format: %s (expected 7 parts, got %d)", key, len(parts))
		return
	}
	agentId = parts[1]
	channel = parts[3]
	scope = SessionScope(parts[5])
	scopeId = parts[6]
	return
}

// SessionKeyFromRequest 从请求生成会话键
func SessionKeyFromRequest(req *SessionRequest) string {
	return GenerateSessionKey(req.AgentID, req.Channel, req.Scope, req.ScopeID)
}
