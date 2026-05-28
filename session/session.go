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

import "time"

// Session 会话
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
}

// SessionMessage 会话消息
type SessionMessage struct {
	ID          string
	Role        string
	Content     string
	Images      []string // 图片 URL 列表，只存储 URL，不存储 base64
	TokenCount  int
	IsCompacted bool
	CreatedAt   time.Time

	// 工具调用相关
	ToolCalls  []ToolCallInfo // assistant 消息的工具调用
	ToolCallID string         // tool 消息关联的调用 ID
}

// AddMessage 添加消息到会话
func (s *Session) AddMessage(msg *SessionMessage) {
	s.Messages = append(s.Messages, msg)
	s.Metadata.MessageCount++
	s.Metadata.TotalTokenCount += msg.TokenCount
	s.UpdatedAt = time.Now()
	s.LastActivityAt = time.Now()
}

// GetMessageCount 获取消息数量
func (s *Session) GetMessageCount() int {
	return len(s.Messages)
}

// GetTotalTokenCount 获取总Token数
func (s *Session) GetTotalTokenCount() int {
	return s.Metadata.TotalTokenCount
}

// SetState 设置会话状态
func (s *Session) SetState(state SessionState) {
	s.State = state
	s.UpdatedAt = time.Now()
}

// UpdateActivity 更新活动时间
func (s *Session) UpdateActivity() {
	s.LastActivityAt = time.Now()
	s.UpdatedAt = time.Now()
}
