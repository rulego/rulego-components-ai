package agent

import (
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	imageutil "github.com/rulego/rulego-components-ai/utils/image"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego/utils/el"
)

// isBase64Image checks whether the image is in base64 format
func isBase64Image(s string) bool {
	return imageutil.IsBase64Image(s)
}

// isLocalFilePath checks whether it is a local file path
func isLocalFilePath(s string) bool {
	return imageutil.IsLocalFilePath(s)
}

// parseBase64Image parses the base64 image, returning mimeType and base64Data
func parseBase64Image(s string) (mimeType, base64Data string) {
	return imageutil.ParseBase64Image(s)
}

// loadLocalImage Load the local image and convert it to base64 format (with compression)
func loadLocalImage(path string) (string, error) {
	return imageutil.LoadLocalImage(path)
}

// ChatMessageTemplate Contextual message/user message template
type ChatMessageTemplate struct {
	Role            string
	ContentTemplate el.Template
}

// ConvertRuleMsgToAgentInput converts RuleMsg to Eino AgentInput, supporting system prompt templates and preset message templates
// presetMessages: A preset list of message templates used for scheduled tasks and similar scenarios, used when there are no messages in the RuleMsg
// modelName: Model name, used to automatically detect whether visual capabilities are supported. If visual abilities are not supported, images will be ignored
// agentID: The agent ID, used to isolate and store images and other resources. If it is empty, it will attempt to extract from the message metadata
// logger: A logger used to output debugging information
func ConvertRuleMsgToAgentInput(ctx types.RuleContext, msg types.RuleMsg, systemPromptTemplate el.Template, hasVar bool, systemPromptRaw string, presetMessages []ChatMessageTemplate, modelName string, agentID string, logger types.Logger) (*adk.AgentInput, error) {
	input := &adk.AgentInput{
		Messages: make([]*schema.Message, 0),
	}

	// Testing whether the model supports visual capabilities
	supportsVision := config.SupportsVision(modelName)

	data := msg.GetData()
	env := base.NodeUtils.GetEvnAndMetadata(ctx, msg)

	if logger != nil {
		logger.Debugf("[ConvertRuleMsgToAgentInput] modelName=%s, supportsVision=%v, dataType=%s", modelName, supportsVision, msg.DataType)
	}

	if msg.DataType == types.JSON {
		parseChatMessages(data, supportsVision, agentID, input, logger)
	}

	// If the message is not parsed, check if there is a preset message template
	if len(input.Messages) == 0 {
		appendPresetOrRawMessages(input, data, presetMessages, env)
	}

	// Added System Prompt to support dynamic template variable parsing
	if systemPromptRaw != "" {
		appendSystemPrompt(input, systemPromptRaw, systemPromptTemplate, hasVar, env)
	}

	return input, nil
}

// parseChatMessages parses conversation messages and adds them to AgentInput
func parseChatMessages(data string, supportsVision bool, agentID string, input *adk.AgentInput, logger types.Logger) {
	var chatRequest config.MultiTurnChatRequest
	if err := json.Unmarshal([]byte(data), &chatRequest); err != nil {
		if logger != nil {
			logger.Warnf("[ConvertRuleMsgToAgentInput] failed to unmarshal chat request: %v", err)
		}
		return
	}

	// Pre-allocated slicing capacity
	if len(chatRequest.Messages) > 0 {
		input.Messages = make([]*schema.Message, 0, len(chatRequest.Messages)+1) // +1 reserved for possible system prompts
	}

	for _, m := range chatRequest.Messages {
		processSingleMessage(m, supportsVision, agentID, input, logger)
	}
}

// processSingleMessage processes a single message and adds it to AgentInput
func processSingleMessage(m config.ChatMessage, supportsVision bool, agentID string, input *adk.AgentInput, logger types.Logger) {
	allImages := m.GetAllImages()

	if logger != nil {
		logger.Debugf("[ConvertRuleMsgToAgentInput] message role=%s, images count=%d", m.Role, len(allImages))
		for i, img := range allImages {
			// Truncate base64 data and display only the first 50 characters
			imgPreview := img
			if len(img) > 50 {
				imgPreview = img[:50] + "..."
			}
			logger.Debugf("[ConvertRuleMsgToAgentInput] image[%d]: %s", i, imgPreview)
		}
	}

	content := m.GetContentAsString()

	// Plain text message optimization: Add directly
	if len(allImages) == 0 {
		input.Messages = append(input.Messages, buildSchemaMessage(m, content, nil, nil))
		return
	}

	// Handling messages containing images
	finalContent, extra := processImages(allImages, content, agentID, logger)

	if !supportsVision {
		input.Messages = append(input.Messages, buildSchemaMessage(m, finalContent, extra, nil))
	} else {
		// The model supports visual capabilities using multimodal formats
		if logger != nil {
			logger.Debugf("[ConvertRuleMsgToAgentInput] using multimodal format (vision supported)")
		}

		multiContent := buildVisionMultiContent(allImages, finalContent, logger)

		input.Messages = append(input.Messages, buildSchemaMessage(m, "", extra, multiContent))
	}
}

// buildSchemaMessage Build schema.Message, and keep the tool call history field.
func buildSchemaMessage(m config.ChatMessage, content string, extra map[string]any, multiContent []schema.MessageInputPart) *schema.Message {
	msg := &schema.Message{
		Role:    schema.RoleType(m.Role),
		Content: content,
		Extra:   extra,
	}

	if len(multiContent) > 0 {
		msg.UserInputMultiContent = multiContent
		msg.Content = ""
	}

	if len(m.ToolCalls) > 0 {
		msg.ToolCalls = make([]schema.ToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: schema.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: normalizeToolCallArguments(tc.Function.Arguments),
				},
			})
		}
	}

	if m.ToolCallID != "" {
		msg.ToolCallID = m.ToolCallID
	}

	return msg
}

// normalizeToolCallArguments ensures that the tool call argument is always a valid JSON string.
func normalizeToolCallArguments(arguments string) string {
	args := strings.TrimSpace(arguments)
	if args == "" || args == "null" {
		return "{}"
	}

	var check map[string]any
	if err := json.Unmarshal([]byte(args), &check); err != nil {
		return "{}"
	}
	return args
}

// processImages processes images and generates text content containing image references and extended fields
func processImages(allImages []string, content string, agentID string, logger types.Logger) (string, map[string]any) {
	var imageInfo strings.Builder
	imageInfo.Grow(len(allImages) * 64) // Enough space is pre-allocated

	// Collect image references and store them in the Extra field for tool/facet access
	imageRefs := make([]string, 0, len(allImages))

	for _, img := range allImages {
		if imageInfo.Len() > 0 {
			imageInfo.WriteString("\n")
		}
		// Determine the image type and format it
		if isBase64Image(img) {
			// Save base64 images to local files so that models can be passed to image analysis tools via file paths
			filePath, err := imageutil.SaveBase64WithContext(img, agentID)
			if err == nil && filePath != "" {
				imageInfo.WriteString("[图片：")
				imageInfo.WriteString(filePath)
				imageInfo.WriteString("]")
				imageRefs = append(imageRefs, filePath)
				if logger != nil {
					logger.Debugf("[ConvertRuleMsgToAgentInput] saved base64 image to temp file: %s", filePath)
				}
			} else {
				imageInfo.WriteString("[图片：Base64格式]")
				if logger != nil {
					logger.Warnf("[ConvertRuleMsgToAgentInput] failed to save base64 image to temp file: %v", err)
				}
			}
		} else if isLocalFilePath(img) {
			imageInfo.WriteString("[图片：")
			imageInfo.WriteString(img)
			imageInfo.WriteString("]")
			imageRefs = append(imageRefs, img)
		} else {
			// URL format
			imageInfo.WriteString("[图片链接：")
			imageInfo.WriteString(img)
			imageInfo.WriteString("]")
			imageRefs = append(imageRefs, img)
		}
	}

	// Combine the final content
	finalContent := content
	if imageInfo.Len() > 0 {
		if content != "" {
			finalContent = content + "\n" + imageInfo.String()
		} else {
			finalContent = imageInfo.String()
		}
	}

	// Save image references in Extra so tools and aspects can access them through metadata.
	var extra map[string]any
	if len(imageRefs) > 0 {
		extra = make(map[string]any, 1)
		if jsonRefs, err := json.Marshal(imageRefs); err == nil {
			extra["images"] = string(jsonRefs)
		}
	}

	return finalContent, extra
}

// buildVisionMultiContent builds multimodal content that supports visual models
func buildVisionMultiContent(allImages []string, finalContent string, logger types.Logger) []schema.MessageInputPart {
	multiContent := make([]schema.MessageInputPart, 0, len(allImages)+1)

	// Add images
	for _, img := range allImages {
		if isBase64Image(img) {
			// Base64 format images
			mimeType, base64Data := parseBase64Image(img)
			if mimeType != "" && base64Data != "" {
				if logger != nil {
					logger.Debugf("[ConvertRuleMsgToAgentInput] adding base64 image: mimeType=%s, dataLen=%d", mimeType, len(base64Data))
				}
				multiContent = append(multiContent, createBase64ImagePart(mimeType, base64Data))
			} else {
				if logger != nil {
					logger.Warnf("[ConvertRuleMsgToAgentInput] failed to parse base64 image: mimeType=%s, dataLen=%d", mimeType, len(base64Data))
				}
			}
		} else if isLocalFilePath(img) {
			// Local file path: Read and convert to base64
			base64Img, err := loadLocalImage(img)
			if err != nil {
				// Loading failed, skip this image
				if logger != nil {
					logger.Warnf("[ConvertRuleMsgToAgentInput] failed to load local image: %s, error: %v", img, err)
				}
				continue
			}
			// Parse the generated base64 data
			mimeType, base64Data := parseBase64Image(base64Img)
			if mimeType != "" && base64Data != "" {
				if logger != nil {
					logger.Debugf("[ConvertRuleMsgToAgentInput] adding local image: path=%s, mimeType=%s", img, mimeType)
				}
				multiContent = append(multiContent, createBase64ImagePart(mimeType, base64Data))
			}
		} else {
			// URL format image: Directly pass the URL, which the large model reads on its own
			if logger != nil {
				logger.Debugf("[ConvertRuleMsgToAgentInput] adding URL image: %s", img)
			}
			imgURL := img // Create local variables to obtain secure pointers
			multiContent = append(multiContent, schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{
						URL: &imgURL,
					},
					Detail: "auto",
				},
			})
		}
	}

	// Add text content (using finalContent containing image paths)
	if finalContent != "" {
		multiContent = append(multiContent, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeText,
			Text: finalContent,
		})
	}

	return multiContent
}

// createBase64ImagePart Creates the MessageInputPart for the base64 image
func createBase64ImagePart(mimeType, base64Data string) schema.MessageInputPart {
	// Copy variables to prevent pointer sharing issues
	mData := base64Data
	return schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{
				Base64Data: &mData,
				MIMEType:   mimeType,
			},
			Detail: "auto",
		},
	}
}

// appendPresetOrRawMessages adds a preset message or a raw data message
func appendPresetOrRawMessages(input *adk.AgentInput, data string, presetMessages []ChatMessageTemplate, env map[string]interface{}) {
	if len(presetMessages) > 0 {
		// Uses preset message templates, supports template variable parsing
		for _, tmpl := range presetMessages {
			content := tmpl.ContentTemplate.ExecuteAsString(env)
			input.Messages = append(input.Messages, &schema.Message{
				Role:    schema.RoleType(tmpl.Role),
				Content: content,
			})
		}
	} else {
		// No preset messages; raw data is used as the user message
		input.Messages = append(input.Messages, &schema.Message{
			Role:    schema.User,
			Content: data,
		})
	}
}

// appendSystemPrompt Adds a system prompt
func appendSystemPrompt(input *adk.AgentInput, systemPromptRaw string, systemPromptTemplate el.Template, hasVar bool, env map[string]interface{}) {
	systemPrompt := systemPromptRaw
	if hasVar && systemPromptTemplate != nil {
		systemPrompt = systemPromptTemplate.ExecuteAsString(env)
	}

	// Check if a system message already exists
	hasSystemMessage := false
	for _, m := range input.Messages {
		if m.Role == schema.System {
			hasSystemMessage = true
			break
		}
	}

	// If there is no system message, add it at the beginning of the message list
	if !hasSystemMessage {
		input.Messages = append([]*schema.Message{schema.SystemMessage(systemPrompt)}, input.Messages...)
	}
}
