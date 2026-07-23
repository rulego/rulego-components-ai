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

package aspect

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// =============================================================================
// Channel Information Context
// =============================================================================

// ChannelType channel type
type ChannelType string

const (
	ChannelTypeFeishu   ChannelType = "feishu"
	ChannelTypeDingTalk ChannelType = "dingtalk"
	ChannelTypeWeCom    ChannelType = "wecom"
	ChannelTypeSlack    ChannelType = "slack"
	ChannelTypeDiscord  ChannelType = "discord"
	ChannelTypeTelegram ChannelType = "telegram"
	ChannelTypeAPI      ChannelType = "api"
	ChannelTypeCLI      ChannelType = "cli"
	ChannelTypeWeb      ChannelType = "web"
	ChannelTypeUnknown  ChannelType = "unknown"
)

// ChannelContext channel context
// Used to record channel information for the current session, supporting proactive responses to users for long-term or scheduled tasks
type ChannelContext struct {
	// Basic channel information
	Type      ChannelType `json:"type"`      // Channel types: feishu, dingtalk, wecom, etc.
	Platform  string      `json:"platform"`  // Platform name (same as Type, used for compatibility)
	Timestamp int64       `json:"timestamp"` // Create timestamps

	// Session icon
	ChatID    string `json:"chatId,omitempty"`    // Session/chat ID
	ChatType  string `json:"chatType,omitempty"`  // Session type: P2P, group
	ThreadID  string `json:"threadId,omitempty"`  // Thread/topic ID
	MessageID string `json:"messageId,omitempty"` // Message ID (for reply)

	// User information
	UserID    string `json:"userId,omitempty"`    // User ID
	OpenID    string `json:"openId,omitempty"`    // User OpenID
	UnionID   string `json:"unionId,omitempty"`   // User UnionID
	UserName  string `json:"userName,omitempty"`  // User nickname
	UserEmail string `json:"userEmail,omitempty"` // User email

	// Robot information
	BotID string `json:"botId,omitempty"` // Robot ID

	// Tenant Information (Multi-tenant Scenario)
	TenantKey string `json:"tenantKey,omitempty"` // Tenant: Key (Feishu)
	CorpID    string `json:"corpId,omitempty"`    // Company ID (WeChat Work)

	// Reply relevantly
	RootID   string `json:"rootId,omitempty"`   // Root message ID (topic/thread)
	ParentID string `json:"parentId,omitempty"` // Parent message ID

	// Extended Information (Platform-Specific Data)
	Extensions map[string]string `json:"extensions,omitempty"`
}

// ToJSON converts the channel context into a JSON string
func (c *ChannelContext) ToJSON() string {
	data, _ := json.Marshal(c)
	return string(data)
}

// ToMetadata converts channel context into metadata maps
func (c *ChannelContext) ToMetadata() map[string]string {
	metadata := make(map[string]string)

	if c.Type != "" {
		metadata["im.platform"] = string(c.Type)
	}
	if c.ChatID != "" {
		metadata["im.chatId"] = c.ChatID
	}
	if c.ChatType != "" {
		metadata["im.chatType"] = c.ChatType
	}
	if c.MessageID != "" {
		metadata["im.msgId"] = c.MessageID
	}
	if c.UserID != "" {
		metadata["im.userId"] = c.UserID
	}
	if c.OpenID != "" {
		metadata["im.openId"] = c.OpenID
	}
	if c.UnionID != "" {
		metadata["im.unionId"] = c.UnionID
	}
	if c.UserName != "" {
		metadata["im.senderName"] = c.UserName
	}
	if c.BotID != "" {
		metadata["im.botId"] = c.BotID
	}
	if c.TenantKey != "" {
		metadata["im.tenantKey"] = c.TenantKey
	}
	if c.CorpID != "" {
		metadata["im.corpId"] = c.CorpID
	}
	if c.RootID != "" {
		metadata["im.rootId"] = c.RootID
	}
	if c.ParentID != "" {
		metadata["im.parentId"] = c.ParentID
	}

	// Merge and expand information
	for k, v := range c.Extensions {
		metadata[k] = v
	}

	return metadata
}

// Is IsGroup a group chat?
func (c *ChannelContext) IsGroup() bool {
	return c.ChatType == "group"
}

// IsP2P: Is there a private message?
func (c *ChannelContext) IsP2P() bool {
	return c.ChatType == "p2p"
}

// IsFeishu is a flying book channel
func (c *ChannelContext) IsFeishu() bool {
	return c.Type == ChannelTypeFeishu
}

// IsDingTalk is a DingTalk channel
func (c *ChannelContext) IsDingTalk() bool {
	return c.Type == ChannelTypeDingTalk
}

// IsWeCom is a WeCom enterprise channel
func (c *ChannelContext) IsWeCom() bool {
	return c.Type == ChannelTypeWeCom
}

// Can CanReply be replyed?
func (c *ChannelContext) CanReply() bool {
	return c.ChatID != ""
}

// Can CanSend send messages?
func (c *ChannelContext) CanSend() bool {
	return c.ChatID != ""
}

// =============================================================================
// Channel Context Tool
// =============================================================================

type channelContextKey struct{}

// ChannelContextKey is used to store the key of the ChannelContext within the Context
var ChannelContextKey = channelContextKey{}

// GetChannelContext retrieves the channel context from the Context
func GetChannelContext(ctx context.Context) (*ChannelContext, bool) {
	chCtx, ok := ctx.Value(ChannelContextKey).(*ChannelContext)
	return chCtx, ok
}

// WithChannelContext adds channel context to the Context
func WithChannelContext(ctx context.Context, chCtx *ChannelContext) context.Context {
	return context.WithValue(ctx, ChannelContextKey, chCtx)
}

// MustGetChannelContext obtains the channel context; if it does not exist, returns an empty object
func MustGetChannelContext(ctx context.Context) *ChannelContext {
	if chCtx, ok := GetChannelContext(ctx); ok {
		return chCtx
	}
	return &ChannelContext{}
}

// =============================================================================
// Channel Context Manager (for storing and retrieving historical channel information)
// =============================================================================

// ChannelContextManager interface
// Used to store session channel information, supports proactive replies to users for long-term or scheduled tasks
type ChannelContextManager interface {
	// Save saves the channel context
	Save(ctx context.Context, sessionKey string, chCtx *ChannelContext) error

	// Get channel context
	Get(ctx context.Context, sessionKey string) (*ChannelContext, error)

	// Delete to remove the channel context
	Delete(ctx context.Context, sessionKey string) error
}

// MemoryChannelContextManager
type MemoryChannelContextManager struct {
	mu       sync.RWMutex
	contexts map[string]*ChannelContext
}

// NewMemoryChannelContextManager creates a memory channel context manager
func NewMemoryChannelContextManager() *MemoryChannelContextManager {
	return &MemoryChannelContextManager{
		contexts: make(map[string]*ChannelContext),
	}
}

// Save saves the channel context
func (m *MemoryChannelContextManager) Save(ctx context.Context, sessionKey string, chCtx *ChannelContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Update timestamp
	chCtx.Timestamp = time.Now().UnixMilli()
	m.contexts[sessionKey] = chCtx
	return nil
}

// Get channel context
func (m *MemoryChannelContextManager) Get(ctx context.Context, sessionKey string) (*ChannelContext, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if chCtx, ok := m.contexts[sessionKey]; ok {
		return chCtx, nil
	}
	return nil, nil
}

// Delete to remove the channel context
func (m *MemoryChannelContextManager) Delete(ctx context.Context, sessionKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.contexts, sessionKey)
	return nil
}

// =============================================================================
// Global channel context manager
// =============================================================================

var (
	globalChannelContextManager   ChannelContextManager
	globalChannelContextManagerMu sync.Mutex
)

// GetGlobalChannelContextManager to obtain the global channel context manager
func GetGlobalChannelContextManager() ChannelContextManager {
	globalChannelContextManagerMu.Lock()
	defer globalChannelContextManagerMu.Unlock()
	if globalChannelContextManager == nil {
		globalChannelContextManager = NewMemoryChannelContextManager()
	}
	return globalChannelContextManager
}

// SetGlobalChannelContextManager sets the global channel context manager
func SetGlobalChannelContextManager(manager ChannelContextManager) {
	globalChannelContextManagerMu.Lock()
	defer globalChannelContextManagerMu.Unlock()
	globalChannelContextManager = manager
}

// =============================================================================
// Channel Context Manager Context tool
// =============================================================================

type channelContextManagerKey struct{}

// ChannelContextManagerKey is used to store the manager's key in the context
var ChannelContextManagerKey = channelContextManagerKey{}

// GetChannelContextManager obtains the channel context manager from the Context
func GetChannelContextManager(ctx context.Context) (ChannelContextManager, bool) {
	manager, ok := ctx.Value(ChannelContextManagerKey).(ChannelContextManager)
	if ok {
		return manager, true
	}
	// Revert to the global manager
	global := GetGlobalChannelContextManager()
	if global != nil {
		return global, true
	}
	return nil, false
}

// WithChannelContextManager adds the channel context manager to the Context
func WithChannelContextManager(ctx context.Context, manager ChannelContextManager) context.Context {
	return context.WithValue(ctx, ChannelContextManagerKey, manager)
}

// SaveChannelContext saves channel context (using the manager in Context)
func SaveChannelContext(ctx context.Context, sessionKey string, chCtx *ChannelContext) error {
	if manager, ok := GetChannelContextManager(ctx); ok {
		return manager.Save(ctx, sessionKey, chCtx)
	}
	return nil
}

// LoadChannelContext Load the channel context (using the manager in Context)
func LoadChannelContext(ctx context.Context, sessionKey string) (*ChannelContext, error) {
	if manager, ok := GetChannelContextManager(ctx); ok {
		return manager.Get(ctx, sessionKey)
	}
	return nil, nil
}
