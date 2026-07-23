package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/session"
	imageutil "github.com/rulego/rulego-components-ai/utils/image"
	"github.com/rulego/rulego/api/types"
)

// imagePathMarkerRegexp matches the [image:path] tag added by processImages
var imagePathMarkerRegexp = regexp.MustCompile(`\[图片：[^\]]*\]\n?`)

// imageTokenEstimate: The estimated number of tokens per image
const imageTokenEstimate = 85

// estimateTokenCount estimates the number of tokens in the text
// Use simple estimation algorithms: distinguish between Chinese and English
func estimateTokenCount(text string) int {
	if len(text) == 0 {
		return 0
	}

	charCount := 0
	chineseCount := 0
	for _, r := range text {
		charCount++
		if r >= 0x4E00 && r <= 0x9FFF {
			chineseCount++
		}
	}

	// Chinese: about 1.5 characters/token (i.e., 2/3 token/character)
	// English/Other: about 4 characters/token (i.e., 1/4 token/character)
	nonChineseCount := charCount - chineseCount
	estimatedTokens := chineseCount*2/3 + nonChineseCount/4

	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	return estimatedTokens
}

// filterImageURLs: filters the list of images
// Retain: local file path, base64 format, http/https URL
func filterImageURLs(images []string) []string {
	if len(images) == 0 {
		return nil
	}
	var result []string
	for _, img := range images {
		if imageutil.IsLocalFilePath(img) || imageutil.IsBase64Image(img) || imageutil.IsExternalURL(img) {
			result = append(result, img)
		}
	}
	return result
}

// SessionAspect Session Management Aspect
// Responsible for loading and saving session history
type SessionAspect struct {
	order        int
	sessionMgr   session.SessionManager
	defaultScope session.SessionScope
	logger       types.Logger
}

// NewSessionAspect creates a session management aspect
func NewSessionAspect(sessionMgr session.SessionManager, defaultScope session.SessionScope, logger types.Logger) *SessionAspect {
	return &SessionAspect{
		order:        50,
		sessionMgr:   sessionMgr,
		defaultScope: defaultScope,
		logger:       logger,
	}
}

// Order returns the execution order
func (a *SessionAspect) Order() int {
	return a.order
}

// New: Create a new instance of the face
func (a *SessionAspect) New() aspect.Aspect {
	return &SessionAspect{
		order:        a.order,
		sessionMgr:   a.sessionMgr,
		defaultScope: a.defaultScope,
		logger:       a.logger,
	}
}

// PointCut checks whether this cut is applied
func (a *SessionAspect) PointCut(ctx context.Context, point *aspect.AgentPoint) bool {
	return a.sessionMgr != nil
}

// log internal log method
func (a *SessionAspect) log(format string, v ...interface{}) {
	if a.logger != nil {
		a.logger.Debugf(format, v...)
	}
}

// Before loading session history
func (a *SessionAspect) Before(ctx context.Context, point *aspect.AgentPoint, input *aspect.AgentInput) (*aspect.AgentInput, error) {
	// Determine session scope: prioritize the configured defaultScope; otherwise, use per_peer
	scope := a.defaultScope
	if scope == "" {
		scope = session.ScopePerPeer
	}

	// Construct the session request
	req := session.SessionRequest{
		AgentID: point.AgentId,
		Channel: session.GetChannelFromInput(point, input),
		Scope:   scope,
		ScopeID: session.GetScopeIDFromInput(point, input),
		UserID:  point.UserId,
	}
	a.log("[SessionAspect] Before: agentId=%s, channel=%s, scope=%s, scopeId=%s, userId=%s",
		req.AgentID, req.Channel, req.Scope, req.ScopeID, req.UserID)

	// Retrieve or create a session (whether or not historical loading is skipped, sessionKey is required to save messages)
	sess, err := a.sessionMgr.GetOrCreate(ctx, req)
	if err != nil {
		a.log("[SessionAspect] Before: GetOrCreate failed: %v", err)
		return input, nil
	}
	a.log("[SessionAspect] Before: session created/retrieved, key=%s", sess.Key)
	input.SessionKey = sess.Key

	// Injecting the session model into metadata (if the user switches the model via command)
	if sess.Metadata.Model != "" {
		input.Metadata[aspect.MetaSessionModel] = sess.Metadata.Model
		a.log("[SessionAspect] Before: injected session_model=%s into metadata", sess.Metadata.Model)
	}
	// Injecting session-level extended parameter coverage (such as thought strength) to metadata (JSON string)
	if len(sess.Metadata.ExtraFields) > 0 {
		if raw, err := json.Marshal(sess.Metadata.ExtraFields); err == nil {
			input.Metadata[aspect.MetaSessionExtraFields] = string(raw)
			a.log("[SessionAspect] Before: injected session_extra_fields into metadata")
		}
	}

	// Check if historical messages are loading
	if input.Metadata[aspect.MetaLoadHistory] != "true" {
		a.log("[SessionAspect] Before: %s not set, skipping history load", aspect.MetaLoadHistory)
		a.log("[SessionAspect] Before: session stats: messages=%d, totalTokens=%d", sess.Metadata.MessageCount, sess.Metadata.TotalTokenCount)
		a.saveUserMessageBeforeLLM(ctx, input, sess.Key)
		return input, nil
	}

	// Intelligent protection: Check if compression is needed
	if a.shouldAutoCompact(sess) {
		a.log("[SessionAspect] Before: auto-triggering compaction for session=%s (tokens=%d, messages=%d)",
			sess.Key, sess.Metadata.TotalTokenCount, sess.Metadata.MessageCount)
		if compacted, err := a.sessionMgr.CompactIfNeeded(ctx, sess.Key); compacted {
			a.log("[SessionAspect] Before: compaction completed for session=%s", sess.Key)
			if updatedSess, err := a.sessionMgr.Get(ctx, sess.Key); err == nil {
				sess = updatedSess
			}
		} else if err != nil {
			a.log("[SessionAspect] Before: compaction failed: %v", err)
		}
	}

	// Retrieve historical message limits and tool call filtering parameters (single value) from configuration
	historyLimit := 100
	keepToolCallsCount := 5
	if config := a.sessionMgr.GetConfig(); config != nil {
		if config.MaxMessages > 0 {
			historyLimit = config.MaxMessages
		}
		if config.PruningConfig != nil && config.PruningConfig.KeepToolCallsCount > 0 {
			keepToolCallsCount = config.PruningConfig.KeepToolCallsCount
		}
	}

	history, err := a.sessionMgr.GetHistory(ctx, sess.Key, historyLimit)
	if err != nil {
		a.saveUserMessageBeforeLLM(ctx, input, sess.Key)
		return input, nil
	}

	// Filter tool calls messages: only keep the most recent N
	history = filterRecentToolCalls(history, keepToolCallsCount)
	// Convert to schema.Message
	input.HistoryMessages = convertSessionMessagesToSchema(history)
	// Convert the local image file path in recent history messages to base64 to ensure the LLM API is readable
	converted, total := convertRecentHistoryImagesToBase64(input.HistoryMessages)
	if total > 0 {
		a.log("[SessionAspect] Before: converted %d/%d history images from local file to base64", converted, total)
	}
	a.log("[SessionAspect] Before: loaded %d history messages, session stats: messages=%d, totalTokens=%d",
		len(history), sess.Metadata.MessageCount, sess.Metadata.TotalTokenCount)

	// Save user messages to sessions before LLM calls
	a.saveUserMessageBeforeLLM(ctx, input, sess.Key)

	return input, nil
}

// shouldAutoCompact checks whether automatic compression is needed
// Security threshold policy: triggered when the number of messages > = 10 and the number of tokens > = 100,000
func (a *SessionAspect) shouldAutoCompact(sess *session.Session) bool {
	if sess.Metadata.MessageCount < 10 {
		return false
	}
	const safetyThreshold = 100000
	return sess.Metadata.TotalTokenCount >= safetyThreshold
}

// After saving session messages
func (a *SessionAspect) After(ctx context.Context, point *aspect.AgentPoint, output *aspect.AgentOutput) (*aspect.AgentOutput, error) {
	sessionKey := output.SessionKey
	if sessionKey == "" {
		sessionKey, _ = session.SessionKeyFromContext(ctx)
	}
	if sessionKey == "" {
		a.log("[SessionAspect] After: no sessionKey found, skipping save")
		return output, nil
	}
	a.log("[SessionAspect] After: saving messages for session=%s", sessionKey)

	// Check if it is a command response
	if output.Metadata["_isCommandResponse"] == true {
		a.log("[SessionAspect] After: command response detected, skipping history save")
		if _, err := a.sessionMgr.Get(ctx, sessionKey); err != nil {
			a.log("[SessionAspect] After: session not found, recreating session=%s", sessionKey)
			req := a.parseSessionKeyToRequest(sessionKey, point)
			if _, err := a.sessionMgr.GetOrCreate(ctx, req); err != nil {
				a.log("[SessionAspect] After: failed to recreate session: %v", err)
			}
		}
		return output, nil
	}

	// User messages are pre-stored in Before(), which only stores assistant replies and tool calls
	userTokenCount := a.estimateUserTokenCount(output.OriginalMessages)

	messageCount := 0
	totalEstimatedTokens := userTokenCount

	// Save tool calls and results
	tcCount, tcTokens := a.saveToolCallMessages(ctx, sessionKey, output.ToolCalls)
	messageCount += tcCount
	totalEstimatedTokens += tcTokens

	// Save the assistant for final reply
	if output.Content != "" {
		assistantTokenCount := estimateTokenCount(output.Content)
		assistantMsg := &session.SessionMessage{
			ID:         session.GenerateMessageID(),
			Role:       string(schema.Assistant),
			Content:    output.Content,
			TokenCount: assistantTokenCount,
			CreatedAt:  time.Now(),
		}
		if err := a.sessionMgr.AddMessage(ctx, sessionKey, assistantMsg); err != nil {
			a.log("[SessionAspect] After: AddMessage for assistant failed: %v", err)
		} else {
			messageCount++
			totalEstimatedTokens += assistantTokenCount
			a.log("[SessionAspect] After: assistant message saved, content length=%d, tokenCount=%d", len(output.Content), assistantTokenCount)
		}
	}

	// Update session statistics
	a.updateSessionStats(ctx, sessionKey, output, totalEstimatedTokens)

	return output, nil
}

// saveToolCallMessages saves the tool call and result messages, returns message count, and estimates the number of tokens
func (a *SessionAspect) saveToolCallMessages(ctx context.Context, sessionKey string, toolCalls []aspect.ToolCallResult) (messageCount, totalTokens int) {
	saveToolCalls := true
	if config := a.sessionMgr.GetConfig(); config != nil && config.PruningConfig != nil {
		saveToolCalls = config.PruningConfig.SaveToolCalls
	}
	validToolCalls := a.filterSavableToolCalls(toolCalls)

	if len(validToolCalls) > 0 && saveToolCalls {
		// 1. Save assistant messages (with tool call)
		toolCallsInfo := make([]session.ToolCallInfo, 0, len(validToolCalls))
		toolCallsTokenCount := 0
		for _, tc := range validToolCalls {
			processedArgs := session.ProcessToolCallArguments(tc.Arguments)
			toolCallsInfo = append(toolCallsInfo, session.ToolCallInfo{
				ID:        tc.CallId,
				Name:      tc.Name,
				Arguments: processedArgs,
			})
			toolCallsTokenCount += estimateTokenCount(tc.Name) + estimateTokenCount(processedArgs)
		}
		assistantToolCallMsg := &session.SessionMessage{
			ID:         session.GenerateMessageID(),
			Role:       string(schema.Assistant),
			Content:    "",
			ToolCalls:  toolCallsInfo,
			TokenCount: toolCallsTokenCount,
			CreatedAt:  time.Now(),
		}
		if err := a.sessionMgr.AddMessage(ctx, sessionKey, assistantToolCallMsg); err != nil {
			a.log("[SessionAspect] After: AddMessage for assistant tool call failed: %v", err)
		} else {
			messageCount++
			totalTokens += toolCallsTokenCount
			a.log("[SessionAspect] After: assistant tool call message saved, toolCalls=%d, tokenCount=%d", len(toolCallsInfo), toolCallsTokenCount)
		}

		// 2. Save the tool result message
		for _, tc := range validToolCalls {
			toolResult := tc.Result
			if tc.Error != nil {
				toolResult = fmt.Sprintf("Error: %v", tc.Error)
			}
			toolResult = session.ProcessToolResult(toolResult)
			toolResultTokenCount := estimateTokenCount(toolResult)

			toolMsg := &session.SessionMessage{
				ID:         session.GenerateMessageID(),
				Role:       string(schema.Tool),
				Content:    toolResult,
				ToolCallID: tc.CallId,
				TokenCount: toolResultTokenCount,
				CreatedAt:  time.Now(),
			}
			if err := a.sessionMgr.AddMessage(ctx, sessionKey, toolMsg); err != nil {
				a.log("[SessionAspect] After: AddMessage for tool result failed: %v", err)
			} else {
				messageCount++
				totalTokens += toolResultTokenCount
				a.log("[SessionAspect] After: tool result message saved, tool=%s, callId=%s, tokenCount=%d", tc.Name, tc.CallId, toolResultTokenCount)
			}
		}
	} else if len(toolCalls) > 0 && !saveToolCalls {
		a.log("[SessionAspect] After: skipping tool calls save (SaveToolCalls=false)")
	} else if len(toolCalls) > 0 {
		a.log("[SessionAspect] After: all tool calls skipped because they are invalid")
	}
	return messageCount, totalTokens
}

// estimateUserTokenCount estimates the number of tokens in a user's message from OriginalMessages
func (a *SessionAspect) estimateUserTokenCount(messages []*schema.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != schema.User {
			continue
		}
		tokenCount := estimateTokenCount(msg.Content)
		if extraImages := extractImagesFromExtra(msg.Extra); len(extraImages) > 0 {
			tokenCount += len(extraImages) * imageTokenEstimate
		} else if len(msg.UserInputMultiContent) > 0 {
			imgCount := 0
			for _, part := range msg.UserInputMultiContent {
				if part.Type == schema.ChatMessagePartTypeImageURL && part.Image != nil {
					imgCount++
				}
			}
			tokenCount += imgCount * imageTokenEstimate
		}
		return tokenCount
	}
	return 0
}

// updateSessionStats updates session token statistics
func (a *SessionAspect) updateSessionStats(ctx context.Context, sessionKey string, output *aspect.AgentOutput, totalEstimatedTokens int) {
	sess, err := a.sessionMgr.Get(ctx, sessionKey)
	if err != nil {
		a.log("[SessionAspect] After: Get session for stats update failed: %v", err)
		return
	}
	// Use the token statistics returned by the model as the current context occupancy
	if output.TokenUsage.TotalTokens > 0 && output.Content != "" {
		sess.Metadata.TotalTokenCount = output.TokenUsage.TotalTokens
	}
	if err := a.sessionMgr.Update(ctx, sess); err != nil {
		a.log("[SessionAspect] After: Update session failed: %v", err)
	} else {
		a.log("[SessionAspect] After: session stats updated, messages=%d, tokens=%d (model=%d, estimated=%d)",
			sess.Metadata.MessageCount, sess.Metadata.TotalTokenCount, output.TokenUsage.TotalTokens, totalEstimatedTokens)
	}
}

func (a *SessionAspect) filterSavableToolCalls(toolCalls []aspect.ToolCallResult) []aspect.ToolCallResult {
	if len(toolCalls) == 0 {
		return nil
	}
	filtered := make([]aspect.ToolCallResult, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if !session.IsExecutableToolCallArgs(tc.Name, tc.Arguments) {
			a.log("[SessionAspect] After: skipping invalid tool call, name=%q, callId=%q", tc.Name, tc.CallId)
			continue
		}
		filtered = append(filtered, tc)
	}
	return filtered
}

// parseSessionKeyToRequest parses session request information from the sessionKey
func (a *SessionAspect) parseSessionKeyToRequest(sessionKey string, point *aspect.AgentPoint) session.SessionRequest {
	agentId, channel, scope, scopeId, err := session.ParseSessionKey(sessionKey)
	if err != nil {
		a.log("[SessionAspect] parseSessionKeyToRequest: failed to parse sessionKey: %v, using point info", err)
		scope = a.defaultScope
		if scope == "" {
			scope = session.ScopePerPeer
		}
		return session.SessionRequest{
			AgentID: point.AgentId,
			Channel: session.GetChannelFromPoint(point),
			Scope:   scope,
			ScopeID: point.ThreadId,
			UserID:  point.UserId,
		}
	}
	return session.SessionRequest{
		AgentID: agentId,
		Channel: channel,
		Scope:   scope,
		ScopeID: scopeId,
		UserID:  point.UserId,
	}
}

// filterRecentToolCalls The filtering tool calls messages and only keeps the nearest N groups
// Policy: Keep all user messages and regular assistant messages, and only restrict messages related to tool calls
// A set of tool calls = 1 assistant (with ToolCalls) + N tool result messages
func filterRecentToolCalls(msgs []*session.SessionMessage, keepCount int) []*session.SessionMessage {
	if keepCount <= 0 || len(msgs) == 0 {
		return msgs
	}

	var toolCallGroupStarts []int
	for i, msg := range msgs {
		if msg.Role == string(schema.Assistant) && len(msg.ToolCalls) > 0 {
			toolCallGroupStarts = append(toolCallGroupStarts, i)
		}
	}

	if len(toolCallGroupStarts) <= keepCount {
		return msgs
	}

	keepGroupStarts := make(map[int]bool)
	startGroupIndex := len(toolCallGroupStarts) - keepCount
	for i := startGroupIndex; i < len(toolCallGroupStarts); i++ {
		keepGroupStarts[toolCallGroupStarts[i]] = true
	}

	result := make([]*session.SessionMessage, 0, len(msgs))
	inToolCallGroup := false
	currentGroupStart := -1

	for i, msg := range msgs {
		if msg.Role == string(schema.Assistant) && len(msg.ToolCalls) > 0 {
			currentGroupStart = i
			inToolCallGroup = true
			if keepGroupStarts[i] {
				result = append(result, msg)
			}
			continue
		}

		if msg.Role == string(schema.Tool) {
			if inToolCallGroup && keepGroupStarts[currentGroupStart] {
				result = append(result, msg)
			}
			continue
		}

		inToolCallGroup = false
		currentGroupStart = -1
		result = append(result, msg)
	}

	return result
}

// sanitizeRestorableToolCallHistory cleans up unpaired tool call messages in the recovery history.
func sanitizeRestorableToolCallHistory(msgs []*session.SessionMessage) []*session.SessionMessage {
	if len(msgs) == 0 {
		return nil
	}

	result := make([]*session.SessionMessage, 0, len(msgs))

	for i := 0; i < len(msgs); i++ {
		msg := msgs[i]

		if msg.Role == string(schema.Tool) {
			continue
		}

		if msg.Role != string(schema.Assistant) || len(msg.ToolCalls) == 0 {
			result = append(result, msg)
			continue
		}

		groupEnd := i + 1
		for groupEnd < len(msgs) && msgs[groupEnd].Role == string(schema.Tool) {
			groupEnd++
		}

		expectedToolCalls := make(map[string]session.ToolCallInfo, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if strings.TrimSpace(tc.ID) == "" {
				continue
			}
			expectedToolCalls[tc.ID] = tc
		}
		if len(expectedToolCalls) == 0 {
			i = groupEnd - 1
			continue
		}

		matchedToolMessages := make([]*session.SessionMessage, 0, len(expectedToolCalls))
		matchedToolCallIDs := make(map[string]struct{}, len(expectedToolCalls))
		for j := i + 1; j < groupEnd; j++ {
			toolMsg := msgs[j]
			if _, ok := expectedToolCalls[toolMsg.ToolCallID]; !ok {
				continue
			}
			if _, exists := matchedToolCallIDs[toolMsg.ToolCallID]; exists {
				continue
			}
			matchedToolCallIDs[toolMsg.ToolCallID] = struct{}{}
			matchedToolMessages = append(matchedToolMessages, toolMsg)
		}

		if len(matchedToolCallIDs) != len(expectedToolCalls) {
			i = groupEnd - 1
			continue
		}

		result = append(result, msg)
		result = append(result, matchedToolMessages...)
		i = groupEnd - 1
	}

	return result
}

// convertSessionMessagesToSchema Converts session messages to schema.Message
func convertSessionMessagesToSchema(msgs []*session.SessionMessage) []*schema.Message {
	sanitizedMessages := sanitizeRestorableToolCallHistory(msgs)
	result := make([]*schema.Message, 0, len(sanitizedMessages))
	for _, msg := range sanitizedMessages {
		switch msg.Role {
		case string(schema.Assistant):
			schemaMsg := &schema.Message{
				Role:    schema.Assistant,
				Content: msg.Content,
			}
			if len(msg.ToolCalls) > 0 {
				toolCalls := make([]schema.ToolCall, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					args := tc.Arguments
					if args == "" || args == "null" {
						args = "{}"
					} else {
						var check map[string]interface{}
						if err := json.Unmarshal([]byte(args), &check); err != nil {
							args = "{}"
						}
					}
					toolCalls[i] = schema.ToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: schema.FunctionCall{
							Name:      tc.Name,
							Arguments: args,
						},
					}
				}
				schemaMsg.ToolCalls = toolCalls
			}
			result = append(result, schemaMsg)

		case string(schema.Tool):
			result = append(result, &schema.Message{
				Role:       schema.Tool,
				Content:    msg.Content,
				ToolCallID: msg.ToolCallID,
			})

		default:
			if len(msg.Images) > 0 {
				var multiContent []schema.MessageInputPart
				for _, imgURL := range msg.Images {
					urlStr := imgURL
					multiContent = append(multiContent, schema.MessageInputPart{
						Type: schema.ChatMessagePartTypeImageURL,
						Image: &schema.MessageInputImage{
							MessagePartCommon: schema.MessagePartCommon{
								URL: &urlStr,
							},
							Detail: "auto",
						},
					})
				}
				// Add text parts only when the text content is not empty (empty text parts cause some model APIs to return empty responses)
				if msg.Content != "" {
					multiContent = append(multiContent, schema.MessageInputPart{
						Type: schema.ChatMessagePartTypeText,
						Text: msg.Content,
					})
				}
				result = append(result, &schema.Message{
					Role:                  schema.RoleType(msg.Role),
					UserInputMultiContent: multiContent,
				})
			} else {
				result = append(result, &schema.Message{
					Role:    schema.RoleType(msg.Role),
					Content: msg.Content,
				})
			}
		}
	}
	return result
}

// extractImagesFromExtra Extracts the original image reference (local path or URL) from the Extra field of the message
func extractImagesFromExtra(extra map[string]any) []string {
	if extra == nil {
		return nil
	}
	imagesRaw, ok := extra["images"]
	if !ok {
		return nil
	}
	imagesStr, ok := imagesRaw.(string)
	if !ok {
		return nil
	}
	var images []string
	if err := json.Unmarshal([]byte(imagesStr), &images); err != nil {
		return nil
	}
	return images
}

// saveUserMessageBeforeLLM saves the user message to the session before the LLM calls
func (a *SessionAspect) saveUserMessageBeforeLLM(ctx context.Context, input *aspect.AgentInput, sessionKey string) {
	if len(input.Messages) == 0 {
		return
	}
	for i := len(input.Messages) - 1; i >= 0; i-- {
		msg := input.Messages[i]
		if msg.Role != schema.User {
			continue
		}

		userContent := msg.Content
		var userImages []string

		// When Content is empty but UserInputMultiContent has a text part, extract the text content
		if userContent == "" && len(msg.UserInputMultiContent) > 0 {
			for _, part := range msg.UserInputMultiContent {
				if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
					userContent = part.Text
					break
				}
			}
		}

		// Image extraction: prioritize obtaining original references (local paths/URLs) from Extra
		if extraImages := extractImagesFromExtra(msg.Extra); len(extraImages) > 0 {
			userImages = extraImages
		} else if len(msg.UserInputMultiContent) > 0 {
			for _, part := range msg.UserInputMultiContent {
				if part.Type != schema.ChatMessagePartTypeImageURL || part.Image == nil {
					continue
				}
				if part.Image.URL != nil {
					userImages = append(userImages, *part.Image.URL)
				} else if part.Image.Base64Data != nil && part.Image.MIMEType != "" {
					userImages = append(userImages, fmt.Sprintf("data:%s;base64,%s", part.Image.MIMEType, *part.Image.Base64Data))
				}
			}
		}

		// Clean up redundant [image:path] tags added by processImages
		// The image path is saved in the images field, and there is no need to duplicate it in content
		if len(userImages) > 0 {
			userContent = stripImagePathMarkers(userContent)
		}

		savedImages := filterImageURLs(userImages)
		userTokenCount := estimateTokenCount(userContent) + len(savedImages)*imageTokenEstimate

		userMsg := &session.SessionMessage{
			ID:         session.GenerateMessageID(),
			Role:       string(schema.User),
			Content:    userContent,
			Images:     savedImages,
			TokenCount: userTokenCount,
			CreatedAt:  time.Now(),
		}
		if err := a.sessionMgr.AddMessage(ctx, sessionKey, userMsg); err != nil {
			a.log("[SessionAspect] Before: pre-save user message failed: %v", err)
			return
		}
		a.log("[SessionAspect] Before: pre-saved user message (images=%d, tokens=%d)", len(savedImages), userTokenCount)
		return
	}
}

// convertRecentHistoryImagesToBase64 converts the local image file path in recent history messages to base64
// Only search and convert within the most recent rounds of dialogue; images that are too old are downgraded to plain text
func convertRecentHistoryImagesToBase64(history []*schema.Message) (converted, total int) {
	if len(history) == 0 {
		return 0, 0
	}
	const maxImageHistoryDepth = 4
	startIdx := len(history) - maxImageHistoryDepth
	if startIdx < 0 {
		startIdx = 0
	}
	// Find the most recent message with UserInputMultiContent in reverse order and convert it
	for i := len(history) - 1; i >= startIdx; i-- {
		msg := history[i]
		if len(msg.UserInputMultiContent) == 0 {
			continue
		}
		for j := range msg.UserInputMultiContent {
			part := &msg.UserInputMultiContent[j]
			if part.Type != schema.ChatMessagePartTypeImageURL || part.Image == nil || part.Image.URL == nil {
				continue
			}
			urlStr := *part.Image.URL
			if !imageutil.IsLocalFilePath(urlStr) {
				continue
			}
			total++
			base64Img, err := imageutil.LoadLocalImage(urlStr)
			if err != nil {
				continue
			}
			mimeType, base64Data := imageutil.ParseBase64Image(base64Img)
			if mimeType == "" || base64Data == "" {
				continue
			}
			part.Image.URL = nil
			part.Image.Base64Data = &base64Data
			part.Image.MIMEType = mimeType
			converted++
		}
		break
	}
	// Cleanup: Downgrade all messages that still contain the local file path URL to plain text
	for i := range history {
		msg := history[i]
		if len(msg.UserInputMultiContent) == 0 {
			continue
		}
		hasLocalFilePath := false
		for _, part := range msg.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeImageURL && part.Image != nil && part.Image.URL != nil {
				if imageutil.IsLocalFilePath(*part.Image.URL) {
					hasLocalFilePath = true
					break
				}
			}
		}
		if !hasLocalFilePath {
			continue
		}
		var textParts []string
		for _, part := range msg.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
				textParts = append(textParts, part.Text)
			} else if part.Type == schema.ChatMessagePartTypeImageURL {
				textParts = append(textParts, "[图片]")
			}
		}
		msg.UserInputMultiContent = nil
		msg.Content = strings.Join(textParts, "\n")
	}
	return converted, total
}

// stripImagePathMarkers cleans up the [image:path] tag added by processImages
func stripImagePathMarkers(content string) string {
	content = imagePathMarkerRegexp.ReplaceAllString(content, "")
	return strings.TrimSpace(content)
}
