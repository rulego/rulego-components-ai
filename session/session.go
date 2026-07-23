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

package session

import (
	"sync"
	"time"
)

// Session
type Session struct {
	Key              string
	AgentID          string
	Channel          string
	Scope            SessionScope
	ScopeID          string
	Messages         []*SessionMessage
	CompactedSummary string
	Metadata         SessionMetadata
	State            SessionState
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastActivityAt   time.Time

	mu sync.Mutex `json:"-"`
}

// SessionMessage
type SessionMessage struct {
	ID          string
	Role        string
	Content     string
	Images      []string // Image URL list, storing only URLs, not base64
	TokenCount  int
	IsCompacted bool
	CreatedAt   time.Time

	// Tool call-related
	ToolCalls  []ToolCallInfo // Assistant message tool call
	ToolCallID string         // tool message associated with the call ID
}

// AddMessage Adds a message to a session
func (s *Session) AddMessage(msg *SessionMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, msg)
	s.Metadata.MessageCount++
	s.Metadata.TotalTokenCount += msg.TokenCount
	s.UpdatedAt = time.Now()
	s.LastActivityAt = time.Now()
}

// GetMessageCount retrieves the number of messages
func (s *Session) GetMessageCount() int {
	return len(s.Messages)
}

// GetTotalTokenCount retrieves the total number of tokens
func (s *Session) GetTotalTokenCount() int {
	return s.Metadata.TotalTokenCount
}

// SetState sets the session state
func (s *Session) SetState(state SessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = state
	s.UpdatedAt = time.Now()
}

// UpdateActivity: Update the event time
func (s *Session) UpdateActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActivityAt = time.Now()
	s.UpdatedAt = time.Now()
}
