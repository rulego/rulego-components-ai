package session

import (
	"context"
	"fmt"
	"time"
)

// SessionManager 会话管理器接口
type SessionManager interface {
	// GetOrCreate 获取或创建会话
	GetOrCreate(ctx context.Context, req SessionRequest) (*Session, error)

	// Get 获取会话（不包含历史消息)
	Get(ctx context.Context, key string) (*Session, error)

	// AddMessage 添加消息到会话
	AddMessage(ctx context.Context, sessionKey string, msg *SessionMessage) error

	// GetHistory 获取会话历史消息
	GetHistory(ctx context.Context, sessionKey string, limit int) ([]*SessionMessage, error)

	// Update 更新会话
	Update(ctx context.Context, session *Session) error

	// Delete 删除会话
	Delete(ctx context.Context, key string) error

	// List 列出会话
	List(ctx context.Context, query *SessionQuery) ([]*Session, error)

	// CompactIfNeeded 按需压缩会话
	// 如果会话达到压缩阈值，执行压缩操作
	// 返回值: 是否执行了压缩, 错误
	CompactIfNeeded(ctx context.Context, sessionKey string) (bool, error)

	// GetConfig 获取会话配置
	GetConfig() *SessionConfig
}

// SessionRequest 会话请求
type SessionRequest struct {
	AgentID  string
	Channel  string
	Scope    SessionScope
	ScopeID  string
	UserID   string
}

// SessionConfig 会话配置
type SessionConfig struct {
	// MaxMessages 最大消息数
	MaxMessages int

	// MaxTokenCount 最大Token数
	MaxTokenCount int

	// TTL 会话生存时间
	TTL time.Duration

	// IdleTimeout 空闲超时
	IdleTimeout time.Duration

	// PruningConfig 修剪配置
	PruningConfig *PruningConfig

	// CompactionConfig 压缩配置
	CompactionConfig *CompactionConfig
}

// PruningConfig 修剪配置
type PruningConfig struct {
	Enabled         bool
	Mode            PruneMode
	KeepRecentCount int
	MaxToolResultSize int

	// SaveToolCalls 是否保存工具调用记录到会话历史
	// true: 保存工具调用和结果（会被加载到历史消息中）
	// false: 不保存工具调用记录（节省存储和 token）
	SaveToolCalls bool

	// KeepToolCallsCount 加载历史时保留的最近工具调用数量
	// 仅当 SaveToolCalls 为 true 时有效
	// 0 表示不限制（加载所有）
	// N 表示只保留最近 N 组工具调用
	KeepToolCallsCount int
}

// PruneMode 修剪模式
type PruneMode string

const (
	PruneModeSoft     PruneMode = "soft"
	PruneModeHard     PruneMode = "hard"
	PruneModeCacheTTL PruneMode = "cache_ttl"
)

// CompactionConfig 压缩配置
type CompactionConfig struct {
	Enabled            bool
	MaxTokenCount      int
	TriggerThreshold   float64
	KeepRecentCount    int
	MinMessagesToCompact int
}

// DefaultSessionConfig 默认会话配置
func DefaultSessionConfig() *SessionConfig {
	return &SessionConfig{
		MaxMessages:  100,
		MaxTokenCount: 128000,
		TTL:          24 * time.Hour * 30, // 30天
		IdleTimeout:  1 * time.Hour,
		PruningConfig: &PruningConfig{
			Enabled:            false,
			Mode:               PruneModeSoft,
			KeepRecentCount:    10,
			MaxToolResultSize:  2000,
			SaveToolCalls:      true,  // 默认保存工具调用
			KeepToolCallsCount: 5,     // 默认保留最近 5 组
		},
		CompactionConfig: &CompactionConfig{
			Enabled:            false,
			MaxTokenCount:      100000,
			TriggerThreshold:   0.8,
			KeepRecentCount:    10,
			MinMessagesToCompact: 20,
		},
	}
}

// Manager 会话管理器实现
type Manager struct {
	storage SessionStorage
	config  SessionConfig
}

// NewManager 创建新的会话管理器
func NewManager(storage SessionStorage, config *SessionConfig) *Manager {
	if config == nil {
		config = DefaultSessionConfig()
	}
	return &Manager{
		storage: storage,
		config:  *config,
	}
}

// GetOrCreate 获取或创建会话
func (m *Manager) GetOrCreate(ctx context.Context, req SessionRequest) (*Session, error) {
	key := SessionKeyFromRequest(&req)

	// 尝试获取现有会话
	session, err := m.storage.Get(ctx, key)
	if err == nil {
		session.UpdateActivity()
		return session, nil
	}

	// 会话不存在，创建新会话
	if err == ErrSessionNotFound {
		return m.createSession(ctx, req)
	}

	return nil, err
}

// Get 获取会话
func (m *Manager) Get(ctx context.Context, key string) (*Session, error) {
	return m.storage.Get(ctx, key)
}

// AddMessage 添加消息到会话
func (m *Manager) AddMessage(ctx context.Context, sessionKey string, msg *SessionMessage) error {
	return m.storage.AddMessage(ctx, sessionKey, msg)
}

// GetHistory 获取会话历史消息
func (m *Manager) GetHistory(ctx context.Context, sessionKey string, limit int) ([]*SessionMessage, error) {
	return m.storage.GetHistory(ctx, sessionKey, limit)
}

// Update 更新会话
func (m *Manager) Update(ctx context.Context, session *Session) error {
	return m.storage.Update(ctx, session)
}

// Delete 删除会话
func (m *Manager) Delete(ctx context.Context, key string) error {
	return m.storage.Delete(ctx, key)
}

// List 列出会话
func (m *Manager) List(ctx context.Context, query *SessionQuery) ([]*Session, error) {
	return m.storage.List(ctx, query)
}

// CompactIfNeeded 按需压缩会话（默认实现不支持压缩）
func (m *Manager) CompactIfNeeded(ctx context.Context, sessionKey string) (bool, error) {
	// 默认 Manager 不支持压缩功能
	// 实际的压缩由 SessionManagerAdapter 实现
	return false, nil
}

// GetConfig 获取会话配置
func (m *Manager) GetConfig() *SessionConfig {
	return &m.config
}

// createSession 创建新会话
func (m *Manager) createSession(ctx context.Context, req SessionRequest) (*Session, error) {
	now := time.Now()

	session := &Session{
		Key:        SessionKeyFromRequest(&req),
		AgentID:    req.AgentID,
		Channel:    req.Channel,
		Scope:      req.Scope,
		ScopeID:    req.ScopeID,
		Messages:   make([]*SessionMessage, 0),
		Metadata: SessionMetadata{
			Title:        fmt.Sprintf("Session %s", req.ScopeID),
			MessageCount: 0,
		},
		State:          StateActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastActivityAt: now,
	}

	if err := m.storage.Create(ctx, session); err != nil {
		return nil, err
	}

	return session, nil
}

// ShouldCompact 检查是否需要压缩
func (s *Session) ShouldCompact(config *CompactionConfig) bool {
	if !config.Enabled {
		return false
	}

	if s.Metadata.MessageCount < config.MinMessagesToCompact {
		return false
	}

	return s.Metadata.TotalTokenCount >= int(float64(config.MaxTokenCount)*config.TriggerThreshold)
}

// ShouldPrune 检查是否需要修剪
func (s *Session) ShouldPrune(config *PruningConfig) bool {
	if !config.Enabled {
		return false
	}

	return len(s.Messages) > config.KeepRecentCount
}
