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

// SessionScope 会话作用域
type SessionScope string

const (
	ScopeMain                  SessionScope = "main"
	ScopePerPeer               SessionScope = "per_peer"
	ScopePerChannelPeer        SessionScope = "per_channel_peer"
	ScopePerAccountChannelPeer SessionScope = "per_account_channel_peer"
	ScopeThread                SessionScope = "thread"
	ScopeTask                  SessionScope = "task"
)

// SessionState 会话状态
type SessionState string

const (
	StateActive    SessionState = "active"
	StateIdle      SessionState = "idle"
	StateCompacted SessionState = "compacted"
	StateArchived  SessionState = "archived"
)

// ToolCallStatus 工具调用状态
type ToolCallStatus string

const (
	ToolCallStatusPending ToolCallStatus = "pending"
	ToolCallStatusRunning ToolCallStatus = "running"
	ToolCallStatusSuccess ToolCallStatus = "success"
	ToolCallStatusError   ToolCallStatus = "error"
)

// ToolCallInfo 工具调用信息
// 用于记录 assistant 消息中的工具调用
type ToolCallInfo struct {
	ID        string `json:"id"`        // 工具调用 ID
	Name      string `json:"name"`      // 工具名称
	Arguments string `json:"arguments"` // JSON 参数
}

// ToolCall 工具调用完整记录
// 包含执行结果和状态
type ToolCall struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Arguments   string         `json:"arguments"`
	Result      string         `json:"result"`
	Status      ToolCallStatus `json:"status"`
	CreatedAt   time.Time      `json:"createdAt"`
	CompletedAt *time.Time     `json:"completedAt,omitempty"`
}

// SessionMetadata 会话元数据
type SessionMetadata struct {
	Title           string `json:"title"`
	Model           string `json:"model,omitempty"` // 当前使用的模型
	TotalTokenCount int    `json:"totalTokenCount"`
	MessageCount    int    `json:"messageCount"`
}
