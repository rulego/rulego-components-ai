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
// 通道信息上下文 (Channel Context)
// =============================================================================

// ChannelType 通道类型
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

// ChannelContext 通道上下文信息
// 用于记录当前会话的通道信息，支持长时间任务或定时任务主动回复用户
type ChannelContext struct {
	// 基础通道信息
	Type      ChannelType `json:"type"`       // 通道类型: feishu, dingtalk, wecom, etc.
	Platform  string      `json:"platform"`   // 平台名称（与 Type 相同，用于兼容）
	Timestamp int64       `json:"timestamp"`  // 创建时间戳

	// 会话标识
	ChatID    string `json:"chatId,omitempty"`    // 会话/聊天 ID
	ChatType  string `json:"chatType,omitempty"`  // 会话类型: p2p, group
	ThreadID  string `json:"threadId,omitempty"`  // 线程/话题 ID
	MessageID string `json:"messageId,omitempty"` // 消息 ID（用于回复）

	// 用户信息
	UserID    string `json:"userId,omitempty"`    // 用户 ID
	OpenID    string `json:"openId,omitempty"`    // 用户 OpenID
	UnionID   string `json:"unionId,omitempty"`   // 用户 UnionID
	UserName  string `json:"userName,omitempty"`  // 用户昵称
	UserEmail string `json:"userEmail,omitempty"` // 用户邮箱

	// 机器人信息
	BotID string `json:"botId,omitempty"` // 机器人 ID

	// 租户信息（多租户场景）
	TenantKey string `json:"tenantKey,omitempty"` // 租户 Key（飞书）
	CorpID    string `json:"corpId,omitempty"`    // 企业 ID（企业微信）

	// 回复相关
	RootID   string `json:"rootId,omitempty"`   // 根消息 ID（话题/线程）
	ParentID string `json:"parentId,omitempty"` // 父消息 ID

	// 扩展信息（平台特定数据）
	Extensions map[string]string `json:"extensions,omitempty"`
}

// ToJSON 将通道上下文转换为 JSON 字符串
func (c *ChannelContext) ToJSON() string {
	data, _ := json.Marshal(c)
	return string(data)
}

// ToMetadata 将通道上下文转换为元数据 map
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

	// 合并扩展信息
	for k, v := range c.Extensions {
		metadata[k] = v
	}

	return metadata
}

// IsGroup 是否群聊
func (c *ChannelContext) IsGroup() bool {
	return c.ChatType == "group"
}

// IsP2P 是否私聊
func (c *ChannelContext) IsP2P() bool {
	return c.ChatType == "p2p"
}

// IsFeishu 是否飞书通道
func (c *ChannelContext) IsFeishu() bool {
	return c.Type == ChannelTypeFeishu
}

// IsDingTalk 是否钉钉通道
func (c *ChannelContext) IsDingTalk() bool {
	return c.Type == ChannelTypeDingTalk
}

// IsWeCom 是否企业微信通道
func (c *ChannelContext) IsWeCom() bool {
	return c.Type == ChannelTypeWeCom
}

// CanReply 是否可以回复
func (c *ChannelContext) CanReply() bool {
	return c.ChatID != ""
}

// CanSend 是否可以发送消息
func (c *ChannelContext) CanSend() bool {
	return c.ChatID != ""
}

// =============================================================================
// 通道上下文 Context 工具
// =============================================================================

type channelContextKey struct{}

// ChannelContextKey 用于在 Context 中存储 ChannelContext 的 key
var ChannelContextKey = channelContextKey{}

// GetChannelContext 从 Context 获取通道上下文
func GetChannelContext(ctx context.Context) (*ChannelContext, bool) {
	chCtx, ok := ctx.Value(ChannelContextKey).(*ChannelContext)
	return chCtx, ok
}

// WithChannelContext 添加通道上下文到 Context
func WithChannelContext(ctx context.Context, chCtx *ChannelContext) context.Context {
	return context.WithValue(ctx, ChannelContextKey, chCtx)
}

// MustGetChannelContext 获取通道上下文，如果不存在返回空对象
func MustGetChannelContext(ctx context.Context) *ChannelContext {
	if chCtx, ok := GetChannelContext(ctx); ok {
		return chCtx
	}
	return &ChannelContext{}
}

// =============================================================================
// 通道上下文管理器（用于存储和检索历史通道信息）
// =============================================================================

// ChannelContextManager 通道上下文管理器接口
// 用于存储会话的通道信息，支持长时间任务或定时任务主动回复用户
type ChannelContextManager interface {
	// Save 保存通道上下文
	Save(ctx context.Context, sessionKey string, chCtx *ChannelContext) error

	// Get 获取通道上下文
	Get(ctx context.Context, sessionKey string) (*ChannelContext, error)

	// Delete 删除通道上下文
	Delete(ctx context.Context, sessionKey string) error
}

// MemoryChannelContextManager 内存通道上下文管理器
type MemoryChannelContextManager struct {
	mu      sync.RWMutex
	contexts map[string]*ChannelContext
}

// NewMemoryChannelContextManager 创建内存通道上下文管理器
func NewMemoryChannelContextManager() *MemoryChannelContextManager {
	return &MemoryChannelContextManager{
		contexts: make(map[string]*ChannelContext),
	}
}

// Save 保存通道上下文
func (m *MemoryChannelContextManager) Save(ctx context.Context, sessionKey string, chCtx *ChannelContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 更新时间戳
	chCtx.Timestamp = time.Now().UnixMilli()
	m.contexts[sessionKey] = chCtx
	return nil
}

// Get 获取通道上下文
func (m *MemoryChannelContextManager) Get(ctx context.Context, sessionKey string) (*ChannelContext, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if chCtx, ok := m.contexts[sessionKey]; ok {
		return chCtx, nil
	}
	return nil, nil
}

// Delete 删除通道上下文
func (m *MemoryChannelContextManager) Delete(ctx context.Context, sessionKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.contexts, sessionKey)
	return nil
}

// =============================================================================
// 全局通道上下文管理器
// =============================================================================

var (
	globalChannelContextManager     ChannelContextManager
	globalChannelContextManagerOnce sync.Once
)

// GetGlobalChannelContextManager 获取全局通道上下文管理器
func GetGlobalChannelContextManager() ChannelContextManager {
	globalChannelContextManagerOnce.Do(func() {
		if globalChannelContextManager == nil {
			globalChannelContextManager = NewMemoryChannelContextManager()
		}
	})
	return globalChannelContextManager
}

// SetGlobalChannelContextManager 设置全局通道上下文管理器
func SetGlobalChannelContextManager(manager ChannelContextManager) {
	globalChannelContextManager = manager
}

// =============================================================================
// 通道上下文管理器 Context 工具
// =============================================================================

type channelContextManagerKey struct{}

// ChannelContextManagerKey 用于在 Context 中存储管理器的 key
var ChannelContextManagerKey = channelContextManagerKey{}

// GetChannelContextManager 从 Context 获取通道上下文管理器
func GetChannelContextManager(ctx context.Context) (ChannelContextManager, bool) {
	manager, ok := ctx.Value(ChannelContextManagerKey).(ChannelContextManager)
	if ok {
		return manager, true
	}
	// 回退到全局管理器
	if globalChannelContextManager != nil {
		return globalChannelContextManager, true
	}
	return nil, false
}

// WithChannelContextManager 添加通道上下文管理器到 Context
func WithChannelContextManager(ctx context.Context, manager ChannelContextManager) context.Context {
	return context.WithValue(ctx, ChannelContextManagerKey, manager)
}

// SaveChannelContext 保存通道上下文（使用 Context 中的管理器）
func SaveChannelContext(ctx context.Context, sessionKey string, chCtx *ChannelContext) error {
	if manager, ok := GetChannelContextManager(ctx); ok {
		return manager.Save(ctx, sessionKey, chCtx)
	}
	return nil
}

// LoadChannelContext 加载通道上下文（使用 Context 中的管理器）
func LoadChannelContext(ctx context.Context, sessionKey string) (*ChannelContext, error) {
	if manager, ok := GetChannelContextManager(ctx); ok {
		return manager.Get(ctx, sessionKey)
	}
	return nil, nil
}
