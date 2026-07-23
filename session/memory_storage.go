package session

import (
	"context"
	"sync"
	"time"
)

// defaultMaxMessages is the maximum number of messages a single session can retain in memory, and after exceeding it, the oldest message is trimmed
const defaultMaxMessages = 2000

// MemoryStorage implementation
type MemoryStorage struct {
	mu          sync.RWMutex
	sessions    map[string]*Session
	messages    map[string][]*SessionMessage
	maxMessages int
}

// NewMemoryStorage creates new memory storage, with a default retention of 2,000 messages per session
func NewMemoryStorage() *MemoryStorage {
	return NewMemoryStorageWithLimit(defaultMaxMessages)
}

// NewMemoryStorageWithLimit creates memory storage with message limits; if the limit is exceeded, the oldest message is clipped to prevent OOM
func NewMemoryStorageWithLimit(maxMessages int) *MemoryStorage {
	if maxMessages <= 0 {
		maxMessages = defaultMaxMessages
	}
	return &MemoryStorage{
		sessions:    make(map[string]*Session),
		messages:    make(map[string][]*SessionMessage),
		maxMessages: maxMessages,
	}
}

// cloneMessage deeply copies messages to isolate the caller from the internal slices of storage
func cloneMessage(msg *SessionMessage) *SessionMessage {
	if msg == nil {
		return nil
	}
	cp := *msg
	if msg.Images != nil {
		cp.Images = append([]string(nil), msg.Images...)
	}
	if msg.ToolCalls != nil {
		cp.ToolCalls = append([]ToolCallInfo(nil), msg.ToolCalls...)
	}
	return &cp
}

// Create creates a session
func (m *MemoryStorage) Create(ctx context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[session.Key]; exists {
		return ErrSessionAlreadyExists
	}

	// Deep copy sessions
	sessionCopy := *session
	sessionCopy.Messages = make([]*SessionMessage, 0, len(session.Messages))

	m.sessions[session.Key] = &sessionCopy
	m.messages[session.Key] = make([]*SessionMessage, 0)

	return nil
}

// Get the session
func (m *MemoryStorage) Get(ctx context.Context, key string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[key]
	if !exists {
		return nil, ErrSessionNotFound
	}

	// Deep copy returns
	sessionCopy := *session
	sessionCopy.Messages = make([]*SessionMessage, 0, len(m.messages[key]))
	for _, msg := range m.messages[key] {
		sessionCopy.Messages = append(sessionCopy.Messages, cloneMessage(msg))
	}

	return &sessionCopy, nil
}

// Update the session
func (m *MemoryStorage) Update(ctx context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[session.Key]; !exists {
		return ErrSessionNotFound
	}

	// Deep copy sessions
	sessionCopy := *session
	sessionCopy.Messages = make([]*SessionMessage, 0, len(session.Messages))

	m.sessions[session.Key] = &sessionCopy
	msgs := make([]*SessionMessage, 0, len(session.Messages))
	for _, msg := range session.Messages {
		msgs = append(msgs, cloneMessage(msg))
	}
	m.messages[session.Key] = msgs

	return nil
}

// Delete: Delete the session
func (m *MemoryStorage) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[key]; !exists {
		return ErrSessionNotFound
	}

	delete(m.sessions, key)
	delete(m.messages, key)

	return nil
}

// AddMessage Adds a message to a session
func (m *MemoryStorage) AddMessage(ctx context.Context, sessionKey string, msg *SessionMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[sessionKey]; !exists {
		return ErrSessionNotFound
	}

	m.messages[sessionKey] = append(m.messages[sessionKey], cloneMessage(msg))

	// Clip the oldest messages beyond the limit to prevent unlimited memory growth
	session := m.sessions[sessionKey]
	droppedTokens := 0
	if m.maxMessages > 0 && len(m.messages[sessionKey]) > m.maxMessages {
		excess := m.messages[sessionKey][:len(m.messages[sessionKey])-m.maxMessages]
		for _, d := range excess {
			droppedTokens += d.TokenCount
		}
		m.messages[sessionKey] = m.messages[sessionKey][len(m.messages[sessionKey])-m.maxMessages:]
	}
	session.Metadata.MessageCount++
	session.Metadata.TotalTokenCount += msg.TokenCount - droppedTokens
	session.UpdatedAt = time.Now()
	session.LastActivityAt = time.Now()

	return nil
}

// GetHistory to get session history messages
func (m *MemoryStorage) GetHistory(ctx context.Context, sessionKey string, limit int) ([]*SessionMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	messages, exists := m.messages[sessionKey]
	if !exists {
		return nil, ErrSessionNotFound
	}

	if limit <= 0 || limit > len(messages) {
		limit = len(messages)
	}

	// Returns the most recent limit bar message
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}

	result := make([]*SessionMessage, limit)
	for i, msg := range messages[start:] {
		result[i] = cloneMessage(msg)
	}

	return result, nil
}

// List Sessions
func (m *MemoryStorage) List(ctx context.Context, query *SessionQuery) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Session

	for _, session := range m.sessions {
		if query != nil {
			if query.AgentID != "" && session.AgentID != query.AgentID {
				continue
			}
			if query.Channel != "" && session.Channel != query.Channel {
				continue
			}
			if query.Scope != "" && session.Scope != query.Scope {
				continue
			}
			if query.ScopeID != "" && session.ScopeID != query.ScopeID {
				continue
			}
			if query.State != "" && session.State != query.State {
				continue
			}
		}

		sessionCopy := *session
		sessionCopy.Messages = make([]*SessionMessage, 0, len(m.messages[session.Key]))
		for _, msg := range m.messages[session.Key] {
			sessionCopy.Messages = append(sessionCopy.Messages, cloneMessage(msg))
		}

		result = append(result, &sessionCopy)
	}

	// Apply limit and offset
	if query != nil {
		if query.Offset > 0 {
			if query.Offset >= len(result) {
				return []*Session{}, nil
			}
			result = result[query.Offset:]
		}
		if query.Limit > 0 && query.Limit < len(result) {
			result = result[:query.Limit]
		}
	}

	return result, nil
}
