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

	// Does MetaLoadHistory load history messages (true/false)
	// Used for SessionAspect to decide whether to load the session history
	MetaLoadHistory = "loadHistory"

	// ============== Channel Identification ==============

	// MetaChannel channel identifiers (e.g., api, dispatch, feishu, dingtalk)
	// Align with the im.channel of rulego-components-im
	MetaChannel = "im.channel"

	// MetaPlatform platform identity (e.g., feishu, dingtalk, wecom)
	// Align with im.platform for rulego-components-im
	MetaPlatform = "im.platform"

	// ============== Scope Identification ==============

	// MetaScopeID Session Scope ID (Session Isolation Identifier)
	// Highest priority, explicitly designating session isolation
	MetaScopeID = "scopeId"

	// MetaChatID Session ID
	// Common session isolation marks
	MetaChatID = "chatId"

	// MetaIMChatID IM platform session ID
	// Align with the im.chatId of rulego-components-im
	MetaIMChatID = "im.chatId"

	// MetaThreadID thread/topic ID
	MetaThreadID = "threadId"

	// MetaIMThreadID IM platform thread/topic ID
	// Align with the im.threadId of rulego-components-im
	MetaIMThreadID = "im.threadId"

	// ============== User Identification ==============

	// MetaUserID User ID
	MetaUserID = "userId"

	// MetaIMUserID: The user ID of the IM platform
	// Align with the im.userId of rulego-components-im
	MetaIMUserID = "im.userId"

	// MetaIMSenderID IM platform sender ID
	// Align with the im.senderId of rulego-components-im
	MetaIMSenderID = "im.senderId"

	// ============== Model Selection ==============

	// MetaSessionModel session-level model
	// Used to read models switched by users via the /model set command from sessions
	MetaSessionModel = "session_model"

	// MetaSessionExtraFields session-level extended parameter coverage (JSON string)
	// Session-level temporary covers for transmitting model-specific parameters such as thinking intensity (e.g., thinking.type, reasoning_effort)
	MetaSessionExtraFields = "session_extra_fields"
)
