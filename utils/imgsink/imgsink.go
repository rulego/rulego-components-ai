// Package imgsink 单次智能体运行内的工具图片传递仓：
// 工具适配器把图片 data URL 存入 ctx 的 Sink、结果文本留 [图片引用:img-N] 标记，
// 模型装饰器在发请求前把标记还原成 user 消息的 image_url parts。
package imgsink

import (
	"context"
	"fmt"
	"sync"
)

// Sink 一次运行的图片仓，按引用号存取 data URL。
type Sink struct {
	mu     sync.Mutex
	next   int
	images map[string][]string
}

func NewSink() *Sink {
	return &Sink{images: map[string][]string{}}
}

type ctxKey struct{}

// WithSink 把仓挂进 ctx；同一运行内工具侧与模型侧共享。
func WithSink(ctx context.Context, sink *Sink) context.Context {
	return context.WithValue(ctx, ctxKey{}, sink)
}

func FromContext(ctx context.Context) *Sink {
	sink, _ := ctx.Value(ctxKey{}).(*Sink)
	return sink
}

// Add 存入一组 data URL，返回引用号（img-1、img-2…）。
func (s *Sink) Add(dataURLs ...string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	ref := fmt.Sprintf("img-%d", s.next)
	s.images[ref] = append(s.images[ref], dataURLs...)
	return ref
}

// Resolve 取引用号对应的 data URL；引用在整轮运行内可重复解析（消息历史每轮重发）。
func (s *Sink) Resolve(ref string) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	urls, ok := s.images[ref]
	return urls, ok
}

// Marker 生成结果文本中的引用标记行。
func Marker(ref string) string { return "[图片引用:" + ref + "]" }
