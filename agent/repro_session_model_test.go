package agent

// 复现/验证会话级模型切换：baseModel=glm-5，context 注入 session_model=glm-5.2，
// 用 httptest mock provider 捕获真实 HTTP 请求，确认 provider 收到的 model 字段。
// 不依赖网络/真实 key，决定性定位"切到 glm-5.2 但请求仍是 glm-5"的根因。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
)

func writeMockSSE(w http.ResponseWriter, model string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	chunk := func(delta map[string]any, finish any) {
		j, _ := json.Marshal(map[string]any{
			"id": "1", "object": "chat.completion.chunk", "model": model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		fmt.Fprintf(w, "data: %s\n\n", j)
	}
	chunk(map[string]any{"role": "assistant", "content": "hi"}, nil)
	chunk(map[string]any{}, "stop")
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// newMockProviderServer 启动一个 openai 兼容的 mock provider，记录每次请求的 model 字段。
func newMockProviderServer(t *testing.T) (*httptest.Server, *[]string, *sync.Mutex) {
	var mu sync.Mutex
	var gotModels []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		gotModels = append(gotModels, req.Model)
		mu.Unlock()
		writeMockSSE(w, req.Model)
	}))
	t.Cleanup(srv.Close)
	return srv, &gotModels, &mu
}

// TestRepro_SessionModelSwitch_NoTools 验证不绑工具时：注入 session_model=glm-5.2，
// provider 收到的 model 应为 glm-5.2。
func TestRepro_SessionModelSwitch_NoTools(t *testing.T) {
	srv, gotModels, mu := newMockProviderServer(t)
	llm := config.LLMConfig{Model: "glm-5", Url: srv.URL, Key: "test"}
	opts := ModelOptions{WrapRetry: true, MaxRetries: 1}
	base, err := CreateChatModel(llm, opts)
	if err != nil {
		t.Fatalf("CreateChatModel: %v", err)
	}
	wrapper := NewAgentAwareModelWrapper(base, llm, opts)

	ctx := ContextWithSessionModel(context.Background(), "glm-5.2")
	sr, err := wrapper.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	drainStream(sr)

	mu.Lock()
	defer mu.Unlock()
	t.Logf("provider 收到 model 序列: %v", *gotModels)
	if len(*gotModels) == 0 {
		t.Fatalf("mock 未收到任何请求")
	}
	for i, m := range *gotModels {
		if m != "glm-5.2" {
			t.Fatalf("❌ 第 %d 次请求 model=%q，期望 glm-5.2", i+1, m)
		}
	}
	t.Logf("✅ 不绑工具：provider 收到 model=%s", strings.Join(*gotModels, ","))
}

// TestRepro_SessionModelSwitch_WithTools 验证绑工具时（模拟 eino react 调用路径）：
// 注入 session_model=glm-5.2，provider 收到的 model 仍应为 glm-5.2。
func TestRepro_SessionModelSwitch_WithTools(t *testing.T) {
	srv, gotModels, mu := newMockProviderServer(t)
	llm := config.LLMConfig{Model: "glm-5", Url: srv.URL, Key: "test"}
	opts := ModelOptions{WrapRetry: true, MaxRetries: 1}
	base, err := CreateChatModel(llm, opts)
	if err != nil {
		t.Fatalf("CreateChatModel: %v", err)
	}
	wrapper := NewAgentAwareModelWrapper(base, llm, opts)

	// 模拟 eino react agent 绑定工具（WithTools 返回派生 wrapper）
	toolInfo := &schema.ToolInfo{
		Name: "search",
		Desc: "search the web",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {Type: "string", Desc: "query", Required: true},
		}),
	}
	withToolsModel, err := wrapper.WithTools([]*schema.ToolInfo{toolInfo})
	if err != nil {
		t.Fatalf("WithTools err: %v", err)
	}

	ctx := ContextWithSessionModel(context.Background(), "glm-5.2")
	sr, err := withToolsModel.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	drainStream(sr)

	mu.Lock()
	defer mu.Unlock()
	t.Logf("provider 收到 model 序列: %v", *gotModels)
	if len(*gotModels) == 0 {
		t.Fatalf("mock 未收到任何请求")
	}
	for i, m := range *gotModels {
		if m != "glm-5.2" {
			t.Fatalf("❌ 第 %d 次请求 model=%q，期望 glm-5.2", i+1, m)
		}
	}
	t.Logf("✅ 绑工具：provider 收到 model=%s", strings.Join(*gotModels, ","))
}

// TestRepro_NoSessionOverride 对照组：不注入 session_model，provider 收到的应是 baseModel=glm-5。
func TestRepro_NoSessionOverride(t *testing.T) {
	srv, gotModels, mu := newMockProviderServer(t)
	llm := config.LLMConfig{Model: "glm-5", Url: srv.URL, Key: "test"}
	opts := ModelOptions{WrapRetry: true, MaxRetries: 1}
	base, err := CreateChatModel(llm, opts)
	if err != nil {
		t.Fatalf("CreateChatModel: %v", err)
	}
	wrapper := NewAgentAwareModelWrapper(base, llm, opts)

	sr, err := wrapper.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	drainStream(sr)

	mu.Lock()
	defer mu.Unlock()
	t.Logf("provider 收到 model 序列: %v", *gotModels)
	if len(*gotModels) == 0 {
		t.Fatalf("mock 未收到任何请求")
	}
	for i, m := range *gotModels {
		if m != "glm-5" {
			t.Fatalf("❌ 对照组第 %d 次请求 model=%q，期望 glm-5", i+1, m)
		}
	}
	t.Logf("✅ 无覆盖：provider 收到 model=%s（默认 glm-5）", strings.Join(*gotModels, ","))
}
