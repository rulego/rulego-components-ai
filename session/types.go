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

// SessionScope session scope
type SessionScope string

const (
	ScopeMain                  SessionScope = "main"
	ScopePerPeer               SessionScope = "per_peer"
	ScopePerChannelPeer        SessionScope = "per_channel_peer"
	ScopePerAccountChannelPeer SessionScope = "per_account_channel_peer"
	ScopeThread                SessionScope = "thread"
	ScopeTask                  SessionScope = "task"
)

// SessionState Session status
type SessionState string

const (
	StateActive    SessionState = "active"
	StateIdle      SessionState = "idle"
	StateCompacted SessionState = "compacted"
	StateArchived  SessionState = "archived"
)

// ToolCallStatus: The state of the tool call
type ToolCallStatus string

const (
	ToolCallStatusPending ToolCallStatus = "pending"
	ToolCallStatusRunning ToolCallStatus = "running"
	ToolCallStatusSuccess ToolCallStatus = "success"
	ToolCallStatusError   ToolCallStatus = "error"
)

// ToolCallInfo tool call information
// Used to record tool calls in Assistant messages
type ToolCallInfo struct {
	ID        string `json:"id"`        // Tool call ID
	Name      string `json:"name"`      // Tool name
	Arguments string `json:"arguments"` // JSON parameters
}

// ToolCall tool call complete record
// Includes execution results and status
type ToolCall struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Arguments   string         `json:"arguments"`
	Result      string         `json:"result"`
	Status      ToolCallStatus `json:"status"`
	CreatedAt   time.Time      `json:"createdAt"`
	CompletedAt *time.Time     `json:"completedAt,omitempty"`
}

// SessionMetadata session metadata
type SessionMetadata struct {
	Title           string         `json:"title"`
	Model           string         `json:"model,omitempty"`       // The model currently in use
	ExtraFields     map[string]any `json:"extraFields,omitempty"` // Session-level extended parameter coverage (such as thinking intensity, e.g., thinking.type/reasoning_effort)
	TotalTokenCount int            `json:"totalTokenCount"`
	MessageCount    int            `json:"messageCount"`
}
