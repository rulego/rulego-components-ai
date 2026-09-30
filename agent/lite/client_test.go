package lite

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sse(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("data: " + l + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func chunk(content, reasoning string) string {
	delta := map[string]any{}
	if content != "" {
		delta["content"] = content
	}
	if reasoning != "" {
		delta["reasoning_content"] = reasoning
	}
	return frame(delta, "")
}

func toolChunk(index int, id, name, args string) string {
	tc := map[string]any{"index": index, "function": map[string]any{"arguments": args}}
	if id != "" {
		tc["id"] = id
	}
	if name != "" {
		tc["function"].(map[string]any)["name"] = name
	}
	return frame(map[string]any{"tool_calls": []any{tc}}, "tool_calls")
}

func finish(reason string) string {
	return frame(map[string]any{}, reason)
}

func frame(delta map[string]any, reason string) string {
	m := map[string]any{"delta": delta}
	if reason != "" {
		m["finish_reason"] = reason
	}
	b, _ := json.Marshal(map[string]any{"choices": []any{m}})
	return string(b)
}

func TestRequestExtraFieldsMarshal(t *testing.T) {
	// nil ExtraFields 输出与旧序列化完全一致
	b, err := json.Marshal(Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "extraFields") {
		t.Fatalf("nil ExtraFields 不应出现额外键: %s", b)
	}

	// ExtraFields 并入顶层,常规字段冲突时常规字段优先
	req := Request{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Stream:   true,
		ExtraFields: map[string]any{
			"thinking": map[string]any{"type": "enabled"},
			"model":    "hijack",
		},
	}
	b, err = json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "m" {
		t.Fatalf("model 应保持常规字段值: %v", body["model"])
	}
	th, ok := body["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" {
		t.Fatalf("thinking 应并入顶层: %v", body["thinking"])
	}
}

func TestRequestExtraFieldsSent(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	_, err := c.Complete(context.Background(), Request{
		Model: "m",
		ExtraFields: map[string]any{
			"reasoning_effort": "high",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, `"reasoning_effort":"high"`) {
		t.Fatalf("请求体应含顶层 reasoning_effort: %s", gotBody)
	}
}

func TestExpandDottedPaths(t *testing.T) {
	if expandDottedPaths(nil) != nil {
		t.Fatal("nil 输入应返回 nil")
	}
	if expandDottedPaths(map[string]any{}) != nil {
		t.Fatal("空 map 输入应返回 nil")
	}
	got := expandDottedPaths(map[string]any{
		"thinking.type":    true,
		"thinking.budget":  100,
		"reasoning_effort": "low",
		"nested":           map[string]any{"a": 1},
		"a.b.c":            "deep",
	})
	th, ok := got["thinking"].(map[string]any)
	if !ok || th["type"] != true || th["budget"] != 100 {
		t.Fatalf("thinking 展开错误: %v", got["thinking"])
	}
	if got["reasoning_effort"] != "low" {
		t.Fatalf("无点号键应原样保留: %v", got)
	}
	if n, ok := got["nested"].(map[string]any); !ok || n["a"] != 1 {
		t.Fatalf("嵌套 map 值应原样并入: %v", got["nested"])
	}
	a, ok := got["a"].(map[string]any)
	if !ok {
		t.Fatalf("a 应展开为 map: %v", got)
	}
	b, ok := a["b"].(map[string]any)
	if !ok || b["c"] != "deep" {
		t.Fatalf("多级点路径展开错误: %v", got["a"])
	}
}

func TestStreamParsesDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse(
			chunk("", "思考中"),
			chunk("你好", ""),
			toolChunk(0, "call-1", "list_devices", "{\"li"),
			toolChunk(0, "", "", "mit\":5}"),
			toolChunk(1, "call-2", "get_device", "{}"),
			finish("tool_calls"),
			finish("stop"), // usage 帧:OpenAI 风格空 choices + usage
		)))
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	var texts, reasonings []string
	finishes := 0
	_, _, err := c.Stream(context.Background(), Request{Model: "m"}, func(d StreamDelta) error {
		if d.Content != "" {
			texts = append(texts, d.Content)
		}
		if d.ReasoningContent != "" {
			reasonings = append(reasonings, d.ReasoningContent)
		}
		if d.FinishReason != "" {
			finishes++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(texts, "") != "你好" || strings.Join(reasonings, "") != "思考中" {
		t.Errorf("delta 内容异常: %v %v", texts, reasonings)
	}
	_ = finishes
}

func TestStreamAccumulatesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse(
			toolChunk(0, "call-1", "list_devices", "{\"li"),
			toolChunk(0, "", "", "mit\":5}"),
			toolChunk(1, "call-2", "get_device", "{}"),
			finish("tool_calls"),
		)))
	}))
	defer srv.Close()

	acc := newToolCallAccumulator()
	_, _, err := New(srv.URL, "k").Stream(context.Background(), Request{Model: "m"}, func(d StreamDelta) error {
		acc.add(d.ToolCalls)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := acc.result()
	if len(calls) != 2 {
		t.Fatalf("应合并出 2 个调用, got %d", len(calls))
	}
	if calls[0].Function.Name != "list_devices" || calls[0].Function.Arguments != `{"limit":5}` {
		t.Errorf("调用 0 异常: %+v", calls[0])
	}
	if calls[1].Function.Name != "get_device" || calls[1].ID != "call-2" {
		t.Errorf("调用 1 异常: %+v", calls[1])
	}
}

func TestCompleteAndError(t *testing.T) {
	ok := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		b, _ := json.Marshal(Response{Choices: []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		}{{Message: Message{Role: "assistant", Content: "pong"}, FinishReason: "stop"}}})
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	c := New(srv.URL, "bad")
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Errorf("非 200 应带服务端 message: %v", err)
	}

	ok = true
	resp, err := c.Complete(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != "pong" {
		t.Errorf("内容异常: %+v", resp.Choices[0])
	}
}

// ---- 重试(§6.2) ----

func TestCompleteRetriesOn500(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream down"}}`))
			return
		}
		b, _ := json.Marshal(Response{Choices: []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		}{{Message: Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}}})
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	c := New(srv.URL, "k")
	start := time.Now()
	resp, err := c.Complete(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("500 后重试应成功: %v", err)
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Errorf("重试后内容异常: %+v", resp.Choices[0])
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("应恰好请求 2 次(1 失败 + 1 重试), got %d", got)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Errorf("重试应遵守退避(≥500ms), got %v", elapsed)
	}
}

func TestCompleteNoRetryOn401(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "k")
	start := time.Now()
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err == nil {
		t.Fatal("401 应失败")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("401 是确定性错误不重试,应只请求 1 次, got %d", got)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Errorf("不重试不应退避等待, got %v", elapsed)
	}
}

func TestCompleteRetryExhausted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"down"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "k")
	c.SetMaxRetries(1)
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err == nil || !strings.Contains(err.Error(), "down") {
		t.Fatalf("重试耗尽应带最后一次错误: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("maxRetries=1 应请求 2 次, got %d", got)
	}
}

func TestStreamRetryBeforeFirstDelta(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			// 流建立失败:非 200,未输出任何 delta → 允许重试。
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"down"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse(chunk("你好", ""), finish("stop"))))
	}))
	defer srv.Close()

	c := New(srv.URL, "k")
	var got strings.Builder
	_, _, err := c.Stream(context.Background(), Request{Model: "m"}, func(d StreamDelta) error {
		got.WriteString(d.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("建立失败应重试成功: %v", err)
	}
	if got.String() != "你好" {
		t.Errorf("重试后内容异常: %q", got.String())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("应请求 2 次, got %d", got)
	}
}

func TestStreamNoRetryAfterFirstDelta(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) > 1 {
			t.Error("首个 delta 交付后中断不应重试")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + chunk("部分", "") + "\n\n"))
		w.(http.Flusher).Flush()
		// 挂住连接不发 [DONE],由客户端 ctx 超时制造"首个 delta 之后的中断"。
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	c := New(srv.URL, "k")
	var received strings.Builder
	_, _, err := c.Stream(ctx, Request{Model: "m"}, func(d StreamDelta) error {
		received.WriteString(d.Content)
		return nil
	})
	if err == nil {
		t.Fatal("首个 delta 后中断应报错而非重试")
	}
	if received.String() != "部分" {
		t.Errorf("已交付内容应保留: %q", received.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("不重试应只请求 1 次, got %d", got)
	}
}

// ---- 多模态(§6.1) ----

func TestMessageMarshalContentForms(t *testing.T) {
	// 纯文本消息保持字符串形态。
	b, err := json.Marshal(Message{Role: "user", Content: "看图"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"content":"看图"`) {
		t.Errorf("纯文本应序列化为字符串: %s", b)
	}

	// parts 消息序列化为数组,且忽略 Content 字段。
	m := Message{Role: "user", Content: "ignored", ContentParts: []ContentPart{
		TextPart("这是什么"), ImagePart("https://example.com/a.png"),
	}}
	b, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ignored") {
		t.Errorf("parts 形态不应输出 Content: %s", b)
	}
	if !strings.Contains(string(b), `"type":"image_url"`) || !strings.Contains(string(b), "a.png") {
		t.Errorf("缺少 image_url part: %s", b)
	}
}

func TestMessageUnmarshalContentForms(t *testing.T) {
	// 字符串形态。
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":"hi"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Content != "hi" || len(m.ContentParts) != 0 {
		t.Errorf("字符串形态解析异常: %+v", m)
	}

	// 数组形态(edge 旧实现在此解析失败)。
	var p Message
	if err := json.Unmarshal([]byte(`{"role":"user","content":[{"type":"text","text":"这是什么"},{"type":"image_url","image_url":{"url":"data:image/png;base64,xxx"}}]}`), &p); err != nil {
		t.Fatalf("数组形态应可解析: %v", err)
	}
	if p.Role != "user" || len(p.ContentParts) != 2 {
		t.Fatalf("数组形态 parts 异常: %+v", p)
	}
	if p.ContentParts[0].Text != "这是什么" || p.ContentParts[1].ImageURL == nil ||
		p.ContentParts[1].ImageURL.URL != "data:image/png;base64,xxx" {
		t.Errorf("parts 内容异常: %+v", p.ContentParts)
	}

	// 数组形态原样再序列化仍为数组。
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"type":"image_url"`) {
		t.Errorf("往返后应保持数组形态: %s", b)
	}
}

func TestRequestCarriesImageParts(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		b, _ := json.Marshal(Response{Choices: []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		}{{Message: Message{Role: "assistant", Content: "图里有猫"}, FinishReason: "stop"}}})
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	req := Request{
		Model: "vision",
		Messages: []Message{
			{Role: "user", ContentParts: []ContentPart{TextPart("这是什么"), ImagePart("https://e.com/cat.png")}},
		},
	}
	if _, err := New(srv.URL, "k").Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"type":"image_url"`) || !strings.Contains(string(body), "cat.png") {
		t.Errorf("请求体应含 image_url part: %s", body)
	}
}
