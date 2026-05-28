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

// GetChannelFromInput 从 AgentPoint 和 AgentInput 获取渠道标识
// 优先级：input.Metadata[im.channel] > input.Metadata[im.platform] > point.Metadata[im.channel] > point.Metadata[im.platform]
func GetChannelFromInput(point *aspect.AgentPoint, input *aspect.AgentInput) string {
	// 优先检查 input.Metadata
	if ch := input.Metadata[aspect.MetaChannel]; ch != "" {
		return ch
	}
	if ch := input.Metadata[aspect.MetaPlatform]; ch != "" {
		return ch
	}
	// 然后检查 point.Metadata
	if ch := point.Metadata[aspect.MetaChannel]; ch != "" {
		return ch
	}
	if ch := point.Metadata[aspect.MetaPlatform]; ch != "" {
		return ch
	}
	return "default"
}

// GetChannelFromPoint 从 AgentPoint 获取渠道标识
// 优先级：point.Metadata[im.channel] > point.Metadata[im.platform]
func GetChannelFromPoint(point *aspect.AgentPoint) string {
	if ch := point.Metadata[aspect.MetaChannel]; ch != "" {
		return ch
	}
	if ch := point.Metadata[aspect.MetaPlatform]; ch != "" {
		return ch
	}
	return "default"
}

// GetScopeIDFromInput 从 AgentPoint 和 AgentInput 获取作用域ID（会话隔离标识）
// 优先级：scopeId > chatId > threadId > userId
func GetScopeIDFromInput(point *aspect.AgentPoint, input *aspect.AgentInput) string {
	// 1. 显式指定的 scopeId
	if sid := input.Metadata[aspect.MetaScopeID]; sid != "" {
		return sid
	}
	// 2. 会话 ID（最常用的隔离标识）
	if sid := input.Metadata[aspect.MetaChatID]; sid != "" {
		return sid
	}
	if sid := input.Metadata[aspect.MetaIMChatID]; sid != "" {
		return sid
	}
	// 3. 线程/话题 ID
	if sid := input.Metadata[aspect.MetaThreadID]; sid != "" {
		return sid
	}
	if sid := input.Metadata[aspect.MetaIMThreadID]; sid != "" {
		return sid
	}
	if sid := point.ThreadId; sid != "" {
		return sid
	}
	// 4. 用户 ID（私聊场景)
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
