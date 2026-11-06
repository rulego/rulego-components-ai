package imgsink

import (
	"context"

	"testing"
)

func TestSinkAddResolveMarker(t *testing.T) {
	s := NewSink()
	ref := s.Add("data:image/jpeg;base64,AAA", "data:image/png;base64,BBB")
	if ref != "img-1" {
		t.Fatalf("期望 img-1，得到 %s", ref)
	}
	urls, ok := s.Resolve("img-1")
	if !ok || len(urls) != 2 || urls[0] != "data:image/jpeg;base64,AAA" {
		t.Fatalf("解析错误: %v %v", ok, urls)
	}
	// 重复解析（消息历史每轮重发）
	if _, ok := s.Resolve("img-1"); !ok {
		t.Fatal("引用应可重复解析")
	}
	if Marker("img-1") != "[图片引用:img-1]" {
		t.Fatalf("标记格式错误: %s", Marker("img-1"))
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if FromContext(ctx) != nil {
		t.Fatal("默认应无仓")
	}
	s := NewSink()
	ctx = WithSink(ctx, s)
	if FromContext(ctx) != s {
		t.Fatal("ctx 仓不一致")
	}
}
