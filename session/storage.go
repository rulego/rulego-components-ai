package session

import "context"

// SessionStorage session storage interface
type SessionStorage interface {
	// Create creates a session
	Create(ctx context.Context, session *Session) error

	// Get the session
	Get(ctx context.Context, key string) (*Session, error)

	// Update the session
	Update(ctx context.Context, session *Session) error

	// Delete: Delete the session
	Delete(ctx context.Context, key string) error

	// AddMessage Adds a message to a session
	AddMessage(ctx context.Context, sessionKey string, msg *SessionMessage) error

	// GetHistory to get session history messages
	GetHistory(ctx context.Context, sessionKey string, limit int) ([]*SessionMessage, error)

	// List of sessions (optional interface implementation)
	List(ctx context.Context, query *SessionQuery) ([]*Session, error)
}

// SessionQuery Session query conditions
type SessionQuery struct {
	AgentID string
	Channel string
	Scope   SessionScope
	ScopeID string
	State   SessionState
	Limit   int
	Offset  int
}

// StorageConfig storage configuration
type StorageConfig struct {
	Type   string
	Prefix string

	// Redis configuration
	RedisAddr     string
	RedisDB       int
	RedisPassword string

	// SQLite configuration
	SQLitePath string
}

// DefaultStorageConfig The default storage configuration
func DefaultStorageConfig() *StorageConfig {
	return &StorageConfig{
		Type:   "memory",
		Prefix: "session:",
	}
}
