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

// imagePathMarkerRegexp 匹配 processImages 添加的 [图片：path] 标记
var imagePathMarkerRegexp = regexp.MustCompile(`\[图片：[^\]]*\]\n?`)

// imageTokenEstimate 每张图片估算的 token 数
const imageTokenEstimate = 85

// estimateTokenCount 估算文本的 token 数量
// 使用简单的估算算法：区分中文和英文
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

	// 中文：约 1.5 字符/token (即 2/3 token/字符)
	// 英文/其他：约 4 字符/token (即 1/4 token/字符)
	nonChineseCount := charCount - chineseCount
	estimatedTokens := chineseCount*2/3 + nonChineseCount/4

	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	return estimatedTokens
}

// filterImageURLs 过滤图片列表
// 保留：本地文件路径、base64 格式、http/https URL
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

// SessionAspect 会话管理切面
// 负责加载和保存会话历史
type SessionAspect struct {
	order        int
	sessionMgr   session.SessionManager
	defaultScope session.SessionScope
	logger       types.Logger
}

// NewSessionAspect 创建会话管理切面
func NewSessionAspect(sessionMgr session.SessionManager, defaultScope session.SessionScope, logger types.Logger) *SessionAspect {
	return &SessionAspect{
		order:        50,
		sessionMgr:   sessionMgr,
		defaultScope: defaultScope,
		logger:       logger,
	}
}

// Order 返回执行顺序
func (a *SessionAspect) Order() int {
	return a.order
}

// New 创建切面的新实例
func (a *SessionAspect) New() aspect.Aspect {
	return &SessionAspect{
		order:        a.order,
		sessionMgr:   a.sessionMgr,
		defaultScope: a.defaultScope,
		logger:       a.logger,
	}
}

// PointCut 检查是否应用此切面
func (a *SessionAspect) PointCut(ctx context.Context, point *aspect.AgentPoint) bool {
	return a.sessionMgr != nil
}

// log 内部日志方法
func (a *SessionAspect) log(format string, v ...interface{}) {
	if a.logger != nil {
		a.logger.Debugf(format, v...)
	}
}

// Before 加载会话历史
func (a *SessionAspect) Before(ctx context.Context, point *aspect.AgentPoint, input *aspect.AgentInput) (*aspect.AgentInput, error) {
	// 确定会话作用域：优先使用配置的 defaultScope，否则使用 per_peer
	scope := a.defaultScope
	if scope == "" {
		scope = session.ScopePerPeer
	}

	// 构建会话请求
	req := session.SessionRequest{
		AgentID: point.AgentId,
		Channel: session.GetChannelFromInput(point, input),
		Scope:   scope,
		ScopeID: session.GetScopeIDFromInput(point, input),
		UserID:  point.UserId,
	}
	a.log("[SessionAspect] Before: agentId=%s, channel=%s, scope=%s, scopeId=%s, userId=%s",
		req.AgentID, req.Channel, req.Scope, req.ScopeID, req.UserID)

	// 获取或创建会话（无论是否跳过历史加载，都需要 sessionKey 用于保存消息）
	sess, err := a.sessionMgr.GetOrCreate(ctx, req)
	if err != nil {
		a.log("[SessionAspect] Before: GetOrCreate failed: %v", err)
		return input, nil
	}
	a.log("[SessionAspect] Before: session created/retrieved, key=%s", sess.Key)
	input.SessionKey = sess.Key

	// 注入会话模型到 metadata（如果用户通过命令切换了模型）
	if sess.Metadata.Model != "" {
		input.Metadata[aspect.MetaSessionModel] = sess.Metadata.Model
		a.log("[SessionAspect] Before: injected session_model=%s into metadata", sess.Metadata.Model)
	}
	// 注入会话级扩展参数覆盖（思考强度等）到 metadata（JSON 字符串）
	if len(sess.Metadata.ExtraFields) > 0 {
		if raw, err := json.Marshal(sess.Metadata.ExtraFields); err == nil {
			input.Metadata[aspect.MetaSessionExtraFields] = string(raw)
			a.log("[SessionAspect] Before: injected session_extra_fields into metadata")
		}
	}

	// 检查是否加载历史消息
	if input.Metadata[aspect.MetaLoadHistory] != "true" {
		a.log("[SessionAspect] Before: %s not set, skipping history load", aspect.MetaLoadHistory)
		a.log("[SessionAspect] Before: session stats: messages=%d, totalTokens=%d", sess.Metadata.MessageCount, sess.Metadata.TotalTokenCount)
		a.saveUserMessageBeforeLLM(ctx, input, sess.Key)
		return input, nil
	}

	// 智能保护: 检查是否需要压缩
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

	// 从配置获取历史消息限制和工具调用过滤参数（一次取值）
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

	// 过滤工具调用消息：只保留最近的 N 条
	history = filterRecentToolCalls(history, keepToolCallsCount)
	// 转换为 schema.Message
	input.HistoryMessages = convertSessionMessagesToSchema(history)
	// 将最近历史消息中的本地图片文件路径转为 base64，确保 LLM API 可读取
	converted, total := convertRecentHistoryImagesToBase64(input.HistoryMessages)
	if total > 0 {
		a.log("[SessionAspect] Before: converted %d/%d history images from local file to base64", converted, total)
	}
	a.log("[SessionAspect] Before: loaded %d history messages, session stats: messages=%d, totalTokens=%d",
		len(history), sess.Metadata.MessageCount, sess.Metadata.TotalTokenCount)

	// 在 LLM 调用之前保存用户消息到 session
	a.saveUserMessageBeforeLLM(ctx, input, sess.Key)

	return input, nil
}

// shouldAutoCompact 检查是否应该自动压缩
// 安全阈值策略：当消息数 >= 10 且 token 数 >= 100000 时触发
func (a *SessionAspect) shouldAutoCompact(sess *session.Session) bool {
	if sess.Metadata.MessageCount < 10 {
		return false
	}
	const safetyThreshold = 100000
	return sess.Metadata.TotalTokenCount >= safetyThreshold
}

// After 保存会话消息
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

	// 检查是否是命令响应
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

	// 用户消息已在 Before() 中预存，After() 只保存助手回复和工具调用
	userTokenCount := a.estimateUserTokenCount(output.OriginalMessages)

	messageCount := 0
	totalEstimatedTokens := userTokenCount

	// 保存工具调用和结果
	tcCount, tcTokens := a.saveToolCallMessages(ctx, sessionKey, output.ToolCalls)
	messageCount += tcCount
	totalEstimatedTokens += tcTokens

	// 保存助手最终回复
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

	// 更新会话统计
	a.updateSessionStats(ctx, sessionKey, output, totalEstimatedTokens)

	return output, nil
}

// saveToolCallMessages 保存工具调用和结果消息，返回消息数量和估算 token 数
func (a *SessionAspect) saveToolCallMessages(ctx context.Context, sessionKey string, toolCalls []aspect.ToolCallResult) (messageCount, totalTokens int) {
	saveToolCalls := true
	if config := a.sessionMgr.GetConfig(); config != nil && config.PruningConfig != nil {
		saveToolCalls = config.PruningConfig.SaveToolCalls
	}
	validToolCalls := a.filterSavableToolCalls(toolCalls)

	if len(validToolCalls) > 0 && saveToolCalls {
		// 1. 保存助手消息（带工具调用）
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

		// 2. 保存工具结果消息
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

// estimateUserTokenCount 从 OriginalMessages 估算用户消息的 token 数
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

// updateSessionStats 更新会话 token 统计
func (a *SessionAspect) updateSessionStats(ctx context.Context, sessionKey string, output *aspect.AgentOutput, totalEstimatedTokens int) {
	sess, err := a.sessionMgr.Get(ctx, sessionKey)
	if err != nil {
		a.log("[SessionAspect] After: Get session for stats update failed: %v", err)
		return
	}
	// 使用模型返回的 token 统计作为当前上下文占用
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

// parseSessionKeyToRequest 从 sessionKey 解析会话请求信息
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

// filterRecentToolCalls 过滤工具调用消息，只保留最近的 N 组
// 策略：保留所有用户消息和普通助手消息，只对工具调用相关消息进行限制
// 一组工具调用 = 1 条 assistant(带 ToolCalls) + N 条 tool 结果消息
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

// sanitizeRestorableToolCallHistory 清理恢复历史中的不成对工具调用消息。
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

// convertSessionMessagesToSchema 转换会话消息为 schema.Message
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
				// 仅在文本内容非空时添加文本 part（空的 text part 会导致部分模型 API 返回空响应）
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

// extractImagesFromExtra 从消息的 Extra 字段提取原始图片引用（本地路径或 URL）
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

// saveUserMessageBeforeLLM 在 LLM 调用之前保存用户消息到 session
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

		// 当 Content 为空但 UserInputMultiContent 有文本 part 时，提取文本内容
		if userContent == "" && len(msg.UserInputMultiContent) > 0 {
			for _, part := range msg.UserInputMultiContent {
				if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
					userContent = part.Text
					break
				}
			}
		}

		// 提取图片：优先从 Extra 获取原始引用（本地路径/URL）
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

		// 清理 processImages 添加的冗余 [图片：path] 标记
		// 图片路径已保存在 images 字段，content 中不需要重复
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

// convertRecentHistoryImagesToBase64 将最近历史消息中的本地图片文件路径转为 base64
// 只在最近几轮对话内查找并转换，太老的图片降级为纯文本
func convertRecentHistoryImagesToBase64(history []*schema.Message) (converted, total int) {
	if len(history) == 0 {
		return 0, 0
	}
	const maxImageHistoryDepth = 4
	startIdx := len(history) - maxImageHistoryDepth
	if startIdx < 0 {
		startIdx = 0
	}
	// 倒序查找最近一条有 UserInputMultiContent 的消息并转换
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
	// 清理：将所有仍包含本地文件路径 URL 的消息降级为纯文本
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

// stripImagePathMarkers 清理 processImages 添加的 [图片：path] 标记
func stripImagePathMarkers(content string) string {
	content = imagePathMarkerRegexp.ReplaceAllString(content, "")
	return strings.TrimSpace(content)
}
