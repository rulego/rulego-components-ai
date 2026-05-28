package session

import "context"

// SessionStorage 会话存储接口
type SessionStorage interface {
	// Create 创建会话
	Create(ctx context.Context, session *Session) error

	// Get 获取会话
	Get(ctx context.Context, key string) (*Session, error)

	// Update 更新会话
	Update(ctx context.Context, session *Session) error

	// Delete 删除会话
	Delete(ctx context.Context, key string) error

	// AddMessage 添加消息到会话
	AddMessage(ctx context.Context, sessionKey string, msg *SessionMessage) error

	// GetHistory 获取会话历史消息
	GetHistory(ctx context.Context, sessionKey string, limit int) ([]*SessionMessage, error)

	// List 列出会话（可选接口实现）
	List(ctx context.Context, query *SessionQuery) ([]*Session, error)
}

// SessionQuery 会话查询条件
type SessionQuery struct {
	AgentID string
	Channel string
	Scope   SessionScope
	ScopeID string
	State   SessionState
	Limit   int
	Offset  int
}

// StorageConfig 存储配置
type StorageConfig struct {
	Type string
	Prefix string

	// Redis 配置
	RedisAddr     string
	RedisDB       int
	RedisPassword string

	// SQLite 配置
	SQLitePath string
}

// DefaultStorageConfig 默认存储配置
func DefaultStorageConfig() *StorageConfig {
	return &StorageConfig{
		Type:   "memory",
		Prefix: "session:",
	}
}
