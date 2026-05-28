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

// isBase64Image 检查是否为 base64 格式图片
func isBase64Image(s string) bool {
	return imageutil.IsBase64Image(s)
}

// isLocalFilePath 检查是否为本地文件路径
func isLocalFilePath(s string) bool {
	return imageutil.IsLocalFilePath(s)
}

// parseBase64Image 解析 base64 图片，返回 mimeType 和 base64Data
func parseBase64Image(s string) (mimeType, base64Data string) {
	return imageutil.ParseBase64Image(s)
}

// loadLocalImage 加载本地图片并转为 base64 格式（带压缩）
func loadLocalImage(path string) (string, error) {
	return imageutil.LoadLocalImage(path)
}

// ChatMessageTemplate 上下文消息/用户消息模板
type ChatMessageTemplate struct {
	Role            string
	ContentTemplate el.Template
}

// ConvertRuleMsgToAgentInput 将 RuleMsg 转换为 Eino AgentInput，支持系统提示词模板和预设消息模板
// presetMessages: 预设消息模板列表，用于定时任务等场景，当 RuleMsg 中没有消息时使用
// modelName: 模型名称，用于自动检测是否支持视觉能力。如果不支持视觉能力，图片会被忽略
// agentID: 智能体 ID，用于隔离存储图片等资源。如果为空，将尝试从消息元数据中提取
// logger: 日志器，用于输出调试信息
func ConvertRuleMsgToAgentInput(ctx types.RuleContext, msg types.RuleMsg, systemPromptTemplate el.Template, hasVar bool, systemPromptRaw string, presetMessages []ChatMessageTemplate, modelName string, agentID string, logger types.Logger) (*adk.AgentInput, error) {
	input := &adk.AgentInput{
		Messages: make([]*schema.Message, 0),
	}

	// 检测模型是否支持视觉能力
	supportsVision := config.SupportsVision(modelName)

	data := msg.GetData()
	env := base.NodeUtils.GetEvnAndMetadata(ctx, msg)

	if logger != nil {
		logger.Debugf("[ConvertRuleMsgToAgentInput] modelName=%s, supportsVision=%v, dataType=%s", modelName, supportsVision, msg.DataType)
	}

	if msg.DataType == types.JSON {
		parseChatMessages(data, supportsVision, agentID, input, logger)
	}

	// 如果没有解析到消息，检查是否有预设消息模板
	if len(input.Messages) == 0 {
		appendPresetOrRawMessages(input, data, presetMessages, env)
	}

	// 添加系统提示词（System Prompt），支持动态模板变量解析
	if systemPromptRaw != "" {
		appendSystemPrompt(input, systemPromptRaw, systemPromptTemplate, hasVar, env)
	}

	return input, nil
}

// parseChatMessages 解析对话消息并添加到 AgentInput
func parseChatMessages(data string, supportsVision bool, agentID string, input *adk.AgentInput, logger types.Logger) {
	var chatRequest config.MultiTurnChatRequest
	if err := json.Unmarshal([]byte(data), &chatRequest); err != nil {
		if logger != nil {
			logger.Warnf("[ConvertRuleMsgToAgentInput] failed to unmarshal chat request: %v", err)
		}
		return
	}

	// 预分配切片容量
	if len(chatRequest.Messages) > 0 {
		input.Messages = make([]*schema.Message, 0, len(chatRequest.Messages)+1) // +1 为可能的 system prompt 预留
	}

	for _, m := range chatRequest.Messages {
		processSingleMessage(m, supportsVision, agentID, input, logger)
	}
}

// processSingleMessage 处理单条消息并添加到 AgentInput
func processSingleMessage(m config.ChatMessage, supportsVision bool, agentID string, input *adk.AgentInput, logger types.Logger) {
	allImages := m.GetAllImages()

	if logger != nil {
		logger.Debugf("[ConvertRuleMsgToAgentInput] message role=%s, images count=%d", m.Role, len(allImages))
		for i, img := range allImages {
			// 截断 base64 数据，只显示前 50 个字符
			imgPreview := img
			if len(img) > 50 {
				imgPreview = img[:50] + "..."
			}
			logger.Debugf("[ConvertRuleMsgToAgentInput] image[%d]: %s", i, imgPreview)
		}
	}

	content := m.GetContentAsString()

	// 纯文本消息优化：直接添加
	if len(allImages) == 0 {
		input.Messages = append(input.Messages, &schema.Message{
			Role:    schema.RoleType(m.Role),
			Content: content,
		})
		return
	}

	// 处理包含图片的消息
	finalContent, extra := processImages(allImages, content, agentID, logger)

	if !supportsVision {
		input.Messages = append(input.Messages, &schema.Message{
			Role:    schema.RoleType(m.Role),
			Content: finalContent,
			Extra:   extra,
		})
	} else {
		// 模型支持视觉能力，使用多模态格式
		if logger != nil {
			logger.Debugf("[ConvertRuleMsgToAgentInput] using multimodal format (vision supported)")
		}

		multiContent := buildVisionMultiContent(allImages, finalContent, logger)

		input.Messages = append(input.Messages, &schema.Message{
			Role:                  schema.RoleType(m.Role),
			UserInputMultiContent: multiContent,
			Extra:                 extra,
		})
	}
}

// processImages 处理图片，生成包含图片引用的文本内容和扩展字段
func processImages(allImages []string, content string, agentID string, logger types.Logger) (string, map[string]any) {
	var imageInfo strings.Builder
	imageInfo.Grow(len(allImages) * 64) // 预分配足够的空间

	// 收集图片引用，存入 Extra 字段供工具/切面访问
	imageRefs := make([]string, 0, len(allImages))

	for _, img := range allImages {
		if imageInfo.Len() > 0 {
			imageInfo.WriteString("\n")
		}
		// 判断图片类型并格式化
		if isBase64Image(img) {
			// 将 base64 图片保存到本地文件，以便模型可以通过文件路径传递给图像分析工具
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
			// URL 格式
			imageInfo.WriteString("[图片链接：")
			imageInfo.WriteString(img)
			imageInfo.WriteString("]")
			imageRefs = append(imageRefs, img)
		}
	}

	// 组合最终内容
	finalContent := content
	if imageInfo.Len() > 0 {
		if content != "" {
			finalContent = content + "\n" + imageInfo.String()
		} else {
			finalContent = imageInfo.String()
		}
	}

	// 将图片引用存入 Extra 字段，供工具/切面通过 metadata 访问
	var extra map[string]any
	if len(imageRefs) > 0 {
		extra = make(map[string]any, 1)
		if jsonRefs, err := json.Marshal(imageRefs); err == nil {
			extra["images"] = string(jsonRefs)
		}
	}

	return finalContent, extra
}

// buildVisionMultiContent 构建支持视觉模型的多模态内容
func buildVisionMultiContent(allImages []string, finalContent string, logger types.Logger) []schema.MessageInputPart {
	multiContent := make([]schema.MessageInputPart, 0, len(allImages)+1)

	// 添加图片
	for _, img := range allImages {
		if isBase64Image(img) {
			// Base64 格式图片
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
			// 本地文件路径：读取并转为 base64
			base64Img, err := loadLocalImage(img)
			if err != nil {
				// 加载失败，跳过此图片
				if logger != nil {
					logger.Warnf("[ConvertRuleMsgToAgentInput] failed to load local image: %s, error: %v", img, err)
				}
				continue
			}
			// 解析生成的 base64 数据
			mimeType, base64Data := parseBase64Image(base64Img)
			if mimeType != "" && base64Data != "" {
				if logger != nil {
					logger.Debugf("[ConvertRuleMsgToAgentInput] adding local image: path=%s, mimeType=%s", img, mimeType)
				}
				multiContent = append(multiContent, createBase64ImagePart(mimeType, base64Data))
			}
		} else {
			// URL 格式图片：直接传递 URL，由大模型自行读取
			if logger != nil {
				logger.Debugf("[ConvertRuleMsgToAgentInput] adding URL image: %s", img)
			}
			imgURL := img // 创建局部变量以获取安全的指针
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

	// 添加文本内容（使用包含图片路径的 finalContent）
	if finalContent != "" {
		multiContent = append(multiContent, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeText,
			Text: finalContent,
		})
	}

	return multiContent
}

// createBase64ImagePart 创建 base64 图片的 MessageInputPart
func createBase64ImagePart(mimeType, base64Data string) schema.MessageInputPart {
	// 拷贝变量，防止指针共享问题
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

// appendPresetOrRawMessages 添加预设消息或原始数据消息
func appendPresetOrRawMessages(input *adk.AgentInput, data string, presetMessages []ChatMessageTemplate, env map[string]interface{}) {
	if len(presetMessages) > 0 {
		// 使用预设消息模板，支持模板变量解析
		for _, tmpl := range presetMessages {
			content := tmpl.ContentTemplate.ExecuteAsString(env)
			input.Messages = append(input.Messages, &schema.Message{
				Role:    schema.RoleType(tmpl.Role),
				Content: content,
			})
		}
	} else {
		// 没有预设消息，使用原始数据作为用户消息
		input.Messages = append(input.Messages, &schema.Message{
			Role:    schema.User,
			Content: data,
		})
	}
}

// appendSystemPrompt 添加系统提示词
func appendSystemPrompt(input *adk.AgentInput, systemPromptRaw string, systemPromptTemplate el.Template, hasVar bool, env map[string]interface{}) {
	systemPrompt := systemPromptRaw
	if hasVar && systemPromptTemplate != nil {
		systemPrompt = systemPromptTemplate.ExecuteAsString(env)
	}

	// 检查是否已存在系统消息
	hasSystemMessage := false
	for _, m := range input.Messages {
		if m.Role == schema.System {
			hasSystemMessage = true
			break
		}
	}

	// 如果没有系统消息，则在消息列表开头添加
	if !hasSystemMessage {
		input.Messages = append([]*schema.Message{schema.SystemMessage(systemPrompt)}, input.Messages...)
	}
}
