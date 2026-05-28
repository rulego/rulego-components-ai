package session

import (
	"context"
	"sync"
	"time"
)

// MemoryStorage 内存存储实现
type MemoryStorage struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	messages map[string][]*SessionMessage
}

// NewMemoryStorage 创建新的内存存储
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		sessions: make(map[string]*Session),
		messages: make(map[string][]*SessionMessage),
	}
}

// Create 创建会话
func (m *MemoryStorage) Create(ctx context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[session.Key]; exists {
		return ErrSessionAlreadyExists
	}

	// 深拷贝会话
	sessionCopy := *session
	sessionCopy.Messages = make([]*SessionMessage, 0, len(session.Messages))

	m.sessions[session.Key] = &sessionCopy
	m.messages[session.Key] = make([]*SessionMessage, 0)

	return nil
}

// Get 获取会话
func (m *MemoryStorage) Get(ctx context.Context, key string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[key]
	if !exists {
		return nil, ErrSessionNotFound
	}

	// 深拷贝返回
	sessionCopy := *session
	sessionCopy.Messages = make([]*SessionMessage, 0, len(m.messages[key]))
	sessionCopy.Messages = append(sessionCopy.Messages, m.messages[key]...)

	return &sessionCopy, nil
}

// Update 更新会话
func (m *MemoryStorage) Update(ctx context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[session.Key]; !exists {
		return ErrSessionNotFound
	}

	// 深拷贝会话
	sessionCopy := *session
	sessionCopy.Messages = make([]*SessionMessage, 0, len(session.Messages))

	m.sessions[session.Key] = &sessionCopy
	m.messages[session.Key] = append(m.messages[session.Key][:0], session.Messages...)

	return nil
}

// Delete 删除会话
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

// AddMessage 添加消息到会话
func (m *MemoryStorage) AddMessage(ctx context.Context, sessionKey string, msg *SessionMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[sessionKey]; !exists {
		return ErrSessionNotFound
	}

	// 深拷贝消息
	msgCopy := *msg
	m.messages[sessionKey] = append(m.messages[sessionKey], &msgCopy)

	// 更新会话元数据
	session := m.sessions[sessionKey]
	session.Metadata.MessageCount++
	session.Metadata.TotalTokenCount += msg.TokenCount
	session.UpdatedAt = time.Now()
	session.LastActivityAt = time.Now()

	return nil
}

// GetHistory 获取会话历史消息
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

	// 返回最近的limit条消息
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}

	result := make([]*SessionMessage, limit)
	copy(result, messages[start:])

	return result, nil
}

// List 列出会话
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
		sessionCopy.Messages = append(sessionCopy.Messages, m.messages[session.Key]...)

		result = append(result, &sessionCopy)
	}

	// 应用limit和offset
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
