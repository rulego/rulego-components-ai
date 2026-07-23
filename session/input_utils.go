/*
 * Copyright 2024 The RuleGo Authors.
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
	"github.com/rulego/rulego-components-ai/aspect"
)

// GetChannelFromInput Retrieves the channel identifier from AgentPoint and AgentInput
// Priority: input.Metadata[im.channel] > input. Metadata[im.platform] > point.Metadata[im.channel] > point. Metadata[im.platform]
func GetChannelFromInput(point *aspect.AgentPoint, input *aspect.AgentInput) string {
	// Prioritize checking input.Metadata
	if ch := input.Metadata[aspect.MetaChannel]; ch != "" {
		return ch
	}
	if ch := input.Metadata[aspect.MetaPlatform]; ch != "" {
		return ch
	}
	// Then check the point.Metadata
	if ch := point.Metadata[aspect.MetaChannel]; ch != "" {
		return ch
	}
	if ch := point.Metadata[aspect.MetaPlatform]; ch != "" {
		return ch
	}
	return "default"
}

// GetChannelFromPoint obtains the channel identifier from AgentPoint
// Priority: point.Metadata[im.channel] > point. Metadata[im.platform]
func GetChannelFromPoint(point *aspect.AgentPoint) string {
	if ch := point.Metadata[aspect.MetaChannel]; ch != "" {
		return ch
	}
	if ch := point.Metadata[aspect.MetaPlatform]; ch != "" {
		return ch
	}
	return "default"
}

// GetScopeIDFromInput Gets the scope ID (session isolation identifier) from AgentPoint and AgentInput
// Priority: scopeId > chatId > threadId > userId
func GetScopeIDFromInput(point *aspect.AgentPoint, input *aspect.AgentInput) string {
	// 1. Explicitly specified scopeId
	if sid := input.Metadata[aspect.MetaScopeID]; sid != "" {
		return sid
	}
	// 2. Session ID (the most commonly used isolation identifier)
	if sid := input.Metadata[aspect.MetaChatID]; sid != "" {
		return sid
	}
	if sid := input.Metadata[aspect.MetaIMChatID]; sid != "" {
		return sid
	}
	// 3. Thread/topic ID
	if sid := input.Metadata[aspect.MetaThreadID]; sid != "" {
		return sid
	}
	if sid := input.Metadata[aspect.MetaIMThreadID]; sid != "" {
		return sid
	}
	if sid := point.ThreadId; sid != "" {
		return sid
	}
	// 4. User ID (Private Chat Scenario)
	if sid := input.Metadata[aspect.MetaUserID]; sid != "" {
		return sid
	}
	if sid := input.Metadata[aspect.MetaIMUserID]; sid != "" {
		return sid
	}
	if sid := input.Metadata[aspect.MetaIMSenderID]; sid != "" {
		return sid
	}
	if sid := point.UserId; sid != "" {
		return sid
	}
	return "default"
}
