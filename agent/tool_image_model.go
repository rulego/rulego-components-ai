package agent

import (
	"context"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego-components-ai/utils/imgsink"
)

var toolImageMarkerRe = regexp.MustCompile(`(?m)^\[图片引用:(img-\d+)\]$`)

// resolveVisionModel 判定主模型是否走工具图片透传：显式配置优先，缺省按能力注册表。
func resolveVisionModel(cfg ChatAgentConfig) bool {
	if cfg.VisionModel != nil {
		return *cfg.VisionModel
	}
	return config.SupportsVision(cfg.LLMConfig.Model)
}

// ToolImageModel 模型装饰器：把工具结果里的 [图片引用:img-N] 标记还原为
// 紧跟其后的 user 图片消息（OpenAI/GLM 口径 image_url 只进 user 角色）。
// vision=false 时仅剥离标记，模型只看工具结果里的文字描述。
type ToolImageModel struct {
	model.ToolCallingChatModel
	vision bool
}

func NewToolImageModel(inner model.ToolCallingChatModel, vision bool) *ToolImageModel {
	return &ToolImageModel{ToolCallingChatModel: inner, vision: vision}
}

func (m *ToolImageModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.ToolCallingChatModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &ToolImageModel{ToolCallingChatModel: inner, vision: m.vision}, nil
}

func (m *ToolImageModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.ToolCallingChatModel.Generate(ctx, m.transform(ctx, input), opts...)
}

func (m *ToolImageModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.ToolCallingChatModel.Stream(ctx, m.transform(ctx, input), opts...)
}

// transform 在请求模型前处理消息：剥离全部标记行；能还原时（vision 且 ctx 有仓）
// 在含标记的工具消息后插入 user 图片消息。标记对应的图片取不到时只剥标记不插图；
// 非工具角色消息里出现的标记只剥不还原。
func (m *ToolImageModel) transform(ctx context.Context, input []*schema.Message) []*schema.Message {
	sink := imgsink.FromContext(ctx)
	restore := m.vision && sink != nil

	marked := false
	for _, msg := range input {
		if strings.Contains(msg.Content, "[图片引用:") {
			marked = true
			break
		}
	}
	if !marked {
		return input
	}

	out := make([]*schema.Message, 0, len(input)+1)
	for _, msg := range input {
		refs := toolImageMarkerRe.FindAllStringSubmatch(msg.Content, -1)
		if len(refs) == 0 {
			out = append(out, msg)
			continue
		}
		clone := *msg
		clone.Content = stripToolImageMarkers(msg.Content)
		out = append(out, &clone)

		if !restore || msg.Role != schema.Tool {
			continue
		}
		parts := []schema.ChatMessagePart{{
			Type: schema.ChatMessagePartTypeText,
			Text: "上一条工具结果附带了图片，请直接查看图片并结合图片内容回答",
		}}
		for _, ref := range refs {
			urls, ok := sink.Resolve(ref[1])
			if !ok {
				continue
			}
			for _, url := range urls {
				parts = append(parts, schema.ChatMessagePart{
					Type:     schema.ChatMessagePartTypeImageURL,
					ImageURL: &schema.ChatMessageImageURL{URL: url},
				})
			}
		}
		if len(parts) > 1 {
			out = append(out, &schema.Message{Role: schema.User, MultiContent: parts})
		}
	}
	return out
}

func stripToolImageMarkers(content string) string {
	if !strings.Contains(content, "[图片引用:") {
		return content
	}
	return toolImageMarkerRe.ReplaceAllString(content, "")
}
