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

package aspect

// Metadata key constants for AgentInput.Metadata and AgentPoint.Metadata.
// These keys are used for session management, channel identification, and scope isolation.
const (
	// ============== Session Control ==============

	// MetaLoadHistory 是否加载历史消息 (true/false)
	// 用于 SessionAspect 决定是否加载会话历史
	MetaLoadHistory = "loadHistory"

	// ============== Channel Identification ==============

	// MetaChannel 通道标识 (如: api, dispatch, feishu, dingtalk)
	// 与 rulego-components-im 的 im.channel 对齐
	MetaChannel = "im.channel"

	// MetaPlatform 平台标识 (如: feishu, dingtalk, wecom)
	// 与 rulego-components-im 的 im.platform 对齐
	MetaPlatform = "im.platform"

	// ============== Scope Identification ==============

	// MetaScopeID 会话作用域 ID（会话隔离标识）
	// 最高优先级，显式指定会话隔离
	MetaScopeID = "scopeId"

	// MetaChatID 会话 ID
	// 常用的会话隔离标识
	MetaChatID = "chatId"

	// MetaIMChatID IM 平台会话 ID
	// 与 rulego-components-im 的 im.chatId 对齐
	MetaIMChatID = "im.chatId"

	// MetaThreadID 线程/话题 ID
	MetaThreadID = "threadId"

	// MetaIMThreadID IM 平台线程/话题 ID
	// 与 rulego-components-im 的 im.threadId 对齐
	MetaIMThreadID = "im.threadId"

	// ============== User Identification ==============

	// MetaUserID 用户 ID
	MetaUserID = "userId"

	// MetaIMUserID IM 平台用户 ID
	// 与 rulego-components-im 的 im.userId 对齐
	MetaIMUserID = "im.userId"

	// MetaIMSenderID IM 平台发送者 ID
	// 与 rulego-components-im 的 im.senderId 对齐
	MetaIMSenderID = "im.senderId"

	// ============== Model Selection ==============

	// MetaSessionModel 会话级模型
	// 用于从会话中读取用户通过 /model set 命令切换的模型
	MetaSessionModel = "session_model"
)
