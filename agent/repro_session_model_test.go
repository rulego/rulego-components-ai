package agent

// Reproduce/verify session-level model switching: baseModel=glm-5, context injection session_model=glm-5.2,
// Use Httptest Mock Provider to capture the real HTTP request and confirm the model field received by the provider.
// Without relying on the network/real key, it decisively locates the root cause of "switching to glm-5.2 but the request is still GLM-5."

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

// newMockProviderServer starts an OpenAI-compatible mock provider, recording the model field for each request.
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

// TestRepro_SessionModelSwitch_NoTools When verifying without binding tools: inject session_model = glm-5.2,
// The provider should receive the model glm-5.2.
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
	t.Logf("provider Receive model sequence: %v", *gotModels)
	if len(*gotModels) == 0 {
		t.Fatalf("mock No requests have been received")
	}
	for i, m := range *gotModels {
		if m != "glm-5.2" {
			t.Fatalf("❌ The %d th request model=%q, expecting glm-5.2", i+1, m)
		}
	}
	t.Logf("✅ No tool binding: provider Received model=%s", strings.Join(*gotModels, ","))
}

// TestRepro_SessionModelSwitch_WithTools When verifying binding tools (simulating eino react call paths):
// Inject session_model = glm-5.2, and the provider should still receive a model of glm-5.2.
func TestRepro_SessionModelSwitch_WithTools(t *testing.T) {
	srv, gotModels, mu := newMockProviderServer(t)
	llm := config.LLMConfig{Model: "glm-5", Url: srv.URL, Key: "test"}
	opts := ModelOptions{WrapRetry: true, MaxRetries: 1}
	base, err := CreateChatModel(llm, opts)
	if err != nil {
		t.Fatalf("CreateChatModel: %v", err)
	}
	wrapper := NewAgentAwareModelWrapper(base, llm, opts)

	// Simulating eino react agent binding tool (WithTools returns a derived wrapper)
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
	t.Logf("provider Receive model sequence: %v", *gotModels)
	if len(*gotModels) == 0 {
		t.Fatalf("mock No requests have been received")
	}
	for i, m := range *gotModels {
		if m != "glm-5.2" {
			t.Fatalf("❌ The %d th request model=%q, expecting glm-5.2", i+1, m)
		}
	}
	t.Logf("✅ Bind tool: provider Received model=%s", strings.Join(*gotModels, ","))
}

// TestRepro_NoSessionOverride Control group: No session_model is injected; the provider should receive baseModel = glm-5.
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
	t.Logf("provider Receive model sequence: %v", *gotModels)
	if len(*gotModels) == 0 {
		t.Fatalf("mock No requests have been received")
	}
	for i, m := range *gotModels {
		if m != "glm-5" {
			t.Fatalf("❌ The %d request in the control group is model=%q, with an expected glm-5", i+1, m)
		}
	}
	t.Logf("✅ No coverage: provider Received model=%s (default glm-5)", strings.Join(*gotModels, ","))
}
