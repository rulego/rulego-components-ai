package session

import (
	"context"
	"fmt"
	"time"
)

// SessionManager session manager interface
type SessionManager interface {
	// GetOrCreate to get or create a session
	GetOrCreate(ctx context.Context, req SessionRequest) (*Session, error)

	// Get the session (excluding historical messages)
	Get(ctx context.Context, key string) (*Session, error)

	// AddMessage Adds a message to a session
	AddMessage(ctx context.Context, sessionKey string, msg *SessionMessage) error

	// GetHistory to get session history messages
	GetHistory(ctx context.Context, sessionKey string, limit int) ([]*SessionMessage, error)

	// Update the session
	Update(ctx context.Context, session *Session) error

	// Delete: Delete the session
	Delete(ctx context.Context, key string) error

	// List Sessions
	List(ctx context.Context, query *SessionQuery) ([]*Session, error)

	// CompactIfNeeded compresses sessions on demand
	// If the session reaches the compression threshold, perform the compression operation
	// Return value: Whether compression was executed, error
	CompactIfNeeded(ctx context.Context, sessionKey string) (bool, error)

	// GetConfig obtains the session configuration
	GetConfig() *SessionConfig
}

// SessionRequest Session request
type SessionRequest struct {
	AgentID string
	Channel string
	Scope   SessionScope
	ScopeID string
	UserID  string
}

// SessionConfig session configuration
type SessionConfig struct {
	// MaxMessages maximum message count
	MaxMessages int

	// MaxTokenCount is the maximum number of tokens
	MaxTokenCount int

	// TTL session survival time
	TTL time.Duration

	// IdleTimeout: idle timeout
	IdleTimeout time.Duration

	// PruningConfig trimming configuration
	PruningConfig *PruningConfig

	// CompactionConfig Compressed configuration
	CompactionConfig *CompactionConfig
}

// PruningConfig trimming configuration
type PruningConfig struct {
	Enabled           bool
	Mode              PruneMode
	KeepRecentCount   int
	MaxToolResultSize int

	// SaveToolCalls Whether to save the record of the tool call to the session history
	// true: Saves tool calls and results (will be loaded into the history messages)
	// false: Does not save tool call records (saves storage and tokens)
	SaveToolCalls bool

	// KeepToolCallsCount The number of recent tool calls retained when loading history
	// Valid only when SaveToolCalls is true
	// 0 means unrestricted (load all)
	// N means only the nearest N group of tool calls are retained
	KeepToolCallsCount int
}

// PruneMode trimming mode
type PruneMode string

const (
	PruneModeSoft     PruneMode = "soft"
	PruneModeHard     PruneMode = "hard"
	PruneModeCacheTTL PruneMode = "cache_ttl"
)

// CompactionConfig Compressed configuration
type CompactionConfig struct {
	Enabled              bool
	MaxTokenCount        int
	TriggerThreshold     float64
	KeepRecentCount      int
	MinMessagesToCompact int
}

// DefaultSessionConfig Default session configuration
func DefaultSessionConfig() *SessionConfig {
	return &SessionConfig{
		MaxMessages:   100,
		MaxTokenCount: 128000,
		TTL:           24 * time.Hour * 30, // 30 days
		IdleTimeout:   1 * time.Hour,
		PruningConfig: &PruningConfig{
			Enabled:            false,
			Mode:               PruneModeSoft,
			KeepRecentCount:    10,
			MaxToolResultSize:  2000,
			SaveToolCalls:      true, // By default, save tool calls
			KeepToolCallsCount: 5,    // By default, the 5 most recent groups are retained
		},
		CompactionConfig: &CompactionConfig{
			Enabled:              false,
			MaxTokenCount:        100000,
			TriggerThreshold:     0.8,
			KeepRecentCount:      10,
			MinMessagesToCompact: 20,
		},
	}
}

// Manager Session Manager implementation
type Manager struct {
	storage SessionStorage
	config  SessionConfig
}

// NewManager creates a new session manager
func NewManager(storage SessionStorage, config *SessionConfig) *Manager {
	if config == nil {
		config = DefaultSessionConfig()
	}
	return &Manager{
		storage: storage,
		config:  *config,
	}
}

// GetOrCreate to get or create a session
func (m *Manager) GetOrCreate(ctx context.Context, req SessionRequest) (*Session, error) {
	key := SessionKeyFromRequest(&req)

	// Try to get an existing session
	session, err := m.storage.Get(ctx, key)
	if err == nil {
		session.UpdateActivity()
		return session, nil
	}

	// If the session doesn't exist, create a new one
	if err == ErrSessionNotFound {
		return m.createSession(ctx, req)
	}

	return nil, err
}

// Get the session
func (m *Manager) Get(ctx context.Context, key string) (*Session, error) {
	return m.storage.Get(ctx, key)
}

// AddMessage Adds a message to a session
func (m *Manager) AddMessage(ctx context.Context, sessionKey string, msg *SessionMessage) error {
	return m.storage.AddMessage(ctx, sessionKey, msg)
}

// GetHistory to get session history messages
func (m *Manager) GetHistory(ctx context.Context, sessionKey string, limit int) ([]*SessionMessage, error) {
	return m.storage.GetHistory(ctx, sessionKey, limit)
}

// Update the session
func (m *Manager) Update(ctx context.Context, session *Session) error {
	return m.storage.Update(ctx, session)
}

// Delete: Delete the session
func (m *Manager) Delete(ctx context.Context, key string) error {
	return m.storage.Delete(ctx, key)
}

// List Sessions
func (m *Manager) List(ctx context.Context, query *SessionQuery) ([]*Session, error) {
	return m.storage.List(ctx, query)
}

// CompactIfNeeded Compresses sessions on demand (compression is not supported by default)
func (m *Manager) CompactIfNeeded(ctx context.Context, sessionKey string) (bool, error) {
	// By default, Manager does not support compression
	// The actual compression is performed by the SessionManagerAdapter
	return false, nil
}

// GetConfig obtains the session configuration
func (m *Manager) GetConfig() *SessionConfig {
	return &m.config
}

// createSession creates a new session
func (m *Manager) createSession(ctx context.Context, req SessionRequest) (*Session, error) {
	now := time.Now()

	session := &Session{
		Key:      SessionKeyFromRequest(&req),
		AgentID:  req.AgentID,
		Channel:  req.Channel,
		Scope:    req.Scope,
		ScopeID:  req.ScopeID,
		Messages: make([]*SessionMessage, 0),
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

// ShouldCompact checks whether compression is needed
func (s *Session) ShouldCompact(config *CompactionConfig) bool {
	if !config.Enabled {
		return false
	}

	if s.Metadata.MessageCount < config.MinMessagesToCompact {
		return false
	}

	return s.Metadata.TotalTokenCount >= int(float64(config.MaxTokenCount)*config.TriggerThreshold)
}

// ShouldPrune checks whether pruning is needed
func (s *Session) ShouldPrune(config *PruningConfig) bool {
	if !config.Enabled {
		return false
	}

	return len(s.Messages) > config.KeepRecentCount
}
