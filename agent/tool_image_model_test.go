package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/rulego/rulego-components-ai/utils/imgsink"
)

func TestToolImageModelTransformVision(t *testing.T) {
	sink := imgsink.NewSink()
	ref := sink.Add("data:image/jpeg;base64,AAA")
	ctx := imgsink.WithSink(context.Background(), sink)

	m := &ToolImageModel{vision: true}
	input := []*schema.Message{
		{Role: schema.User, Content: "拍一张"},
		{Role: schema.Assistant, Content: ""},
		{Role: schema.Tool, Content: "【已完成】已拍下。\n图片内容描述: 走廊\n" + imgsink.Marker(ref)},
	}
	out := m.transform(ctx, input)
	if len(out) != 4 {
		t.Fatalf("期望插入 1 条 user 消息共 4 条，得到 %d", len(out))
	}
	if out[2].Role != schema.Tool || strings.Contains(out[2].Content, "[图片引用:") {
		t.Fatalf("工具消息应剥离标记: %+v", out[2])
	}
	if out[3].Role != schema.User {
		t.Fatalf("第 4 条应为 user: %+v", out[3])
	}
	parts := out[3].MultiContent
	if len(parts) != 2 || parts[0].Type != schema.ChatMessagePartTypeText ||
		parts[1].Type != schema.ChatMessagePartTypeImageURL ||
		parts[1].ImageURL.URL != "data:image/jpeg;base64,AAA" {
		t.Fatalf("图片 parts 错误: %+v", parts)
	}
}

func TestToolImageModelTransformUnresolvableRef(t *testing.T) {
	sink := imgsink.NewSink()
	ctx := imgsink.WithSink(context.Background(), sink)

	m := &ToolImageModel{vision: true}
	input := []*schema.Message{
		{Role: schema.Tool, Content: "结果\n[图片引用:img-9]"},
	}
	out := m.transform(ctx, input)
	if len(out) != 1 || strings.Contains(out[0].Content, "[图片引用:") {
		t.Fatalf("取不到图应只剥标记不插图: %+v", out)
	}
}

func TestToolImageModelTransformNonVision(t *testing.T) {
	sink := imgsink.NewSink()
	ref := sink.Add("data:image/jpeg;base64,AAA")
	ctx := imgsink.WithSink(context.Background(), sink)

	m := &ToolImageModel{vision: false}
	input := []*schema.Message{
		{Role: schema.Tool, Content: "描述\n" + imgsink.Marker(ref)},
	}
	out := m.transform(ctx, input)
	if len(out) != 1 || strings.Contains(out[0].Content, "[图片引用:") {
		t.Fatalf("非视觉模型应仅剥离标记不插图: %+v", out)
	}
	if !strings.Contains(out[0].Content, "描述") {
		t.Fatal("原文本不应被破坏")
	}
}

func TestToolImageModelTransformUserMessageMarkerNotRestored(t *testing.T) {
	sink := imgsink.NewSink()
	ref := sink.Add("data:image/jpeg;base64,AAA")
	ctx := imgsink.WithSink(context.Background(), sink)

	m := &ToolImageModel{vision: true}
	// 标记按整行锚定；独立行的标记在用户消息里剥除但不还原成图片
	input := []*schema.Message{
		{Role: schema.User, Content: "上一轮那张图\n[图片引用:" + ref + "]"},
	}
	out := m.transform(ctx, input)
	if len(out) != 1 || strings.Contains(out[0].Content, "[图片引用:") {
		t.Fatalf("用户消息中的标记应只剥不还原: %+v", out)
	}
	if out[0].MultiContent != nil {
		t.Fatalf("用户消息后不应插入图片消息: %+v", out[0])
	}
	if !strings.Contains(out[0].Content, "上一轮那张图") {
		t.Fatal("原文本不应被破坏")
	}
}

func TestToolImageModelTransformNoMarker(t *testing.T) {
	m := &ToolImageModel{vision: true}
	input := []*schema.Message{{Role: schema.User, Content: "普通消息"}}
	if out := m.transform(context.Background(), input); len(out) != 1 || out[0] != input[0] {
		t.Fatalf("无标记应原样返回: %+v", out)
	}
}
