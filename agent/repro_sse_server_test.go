package agent

// 用本地 SSE server 走真实 sashabaranov + eino-ext 全链路复现 “Error in input stream”。
// 和 repro_midstream_test.go（fakeChatModel）不同，这里服务端是真实 HTTP，发 OpenAI SSE
// 格式（含 data: {"error":...}），由 SDK 真实解析。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
)

// sseReproServer 本地 OpenAI 兼容 SSE server。
// 第 breakOn 次请求：先发 chunks，再插入一个 SSE error 事件（模拟智谱 mid-stream 内部错误）；
// 其他次：发 chunks 后正常 [DONE] 结束。
type sseReproServer struct {
	addr    string
	breakOn int
	chunks  []string
	calls   int
	server  *http.Server
}

func newSSEReproServer(t *testing.T, breakOn int, chunks []string) *sseReproServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &sseReproServer{addr: ln.Addr().String(), breakOn: breakOn, chunks: chunks}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handle)
	s.server = &http.Server{Handler: mux}
	go func() { _ = s.server.Serve(ln) }()
	return s
}

func (s *sseReproServer) handle(w http.ResponseWriter, r *http.Request) {
	s.calls++
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	writeSSE := func(payload string) {
		fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}

	for _, c := range s.chunks {
		chunkJSON, _ := json.Marshal(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   "test-model",
			"choices": []map[string]any{
				{"index": 0, "delta": map[string]string{"role": "assistant", "content": c}, "finish_reason": nil},
			},
		})
		writeSSE(string(chunkJSON))
	}

	if s.calls == s.breakOn {
		// 插入 SSE error 事件 —— 模拟智谱 GLM 流式生成途中的内部错误
		errJSON, _ := json.Marshal(map[string]any{
			"error": map[string]any{
				"message": "Error in input stream (Please retry the request.)",
				"type":    "internal_error",
			},
		})
		writeSSE(string(errJSON))
		return // 不发 [DONE]，连接关闭后 sashabaranov 读到 EOF 触发 unmarshalError
	}

	writeSSE("[DONE]")
}

// drainStreamErr 消费流，返回 (内容拼接, 错误)；正常结束 err=io.EOF。
func drainStreamErr(sr *schema.StreamReader[*schema.Message]) (string, error) {
	defer sr.Close()
	var sb strings.Builder
	for {
		msg, err := sr.Recv()
		if err != nil {
			return sb.String(), err
		}
		if msg != nil {
			sb.WriteString(msg.Content)
		}
	}
}

// TestReproSSE_OffMode_MidStreamError 真实 SDK 路径复现（off 模式）：
// 本地 server 第 1 次请求发 7 个 chunk 后插入 SSE error 事件。sashabaranov 解析出
// "error, Error in input stream (Please retry the request.)"。off 模式探测窗口=3，
// error 在窗口外 → 透传给调用方。
func TestReproSSE_OffMode_MidStreamError(t *testing.T) {
	srv := newSSEReproServer(t, 1, []string{"你", "好", "，", "这", "是", "回", "复"}) // 7 chunk > 窗口 3
	defer srv.server.Close()

	cfg := &config.LLMConfig{
		Url:         "http://" + srv.addr + "/v1",
		Key:         "test-key",
		Model:       "test-model",
		MaxRetries:  3,
		StreamRetryMode: config.StreamRetryOff,
		Params:      config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 3})
	if err != nil {
		t.Fatal(err)
	}

	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("hi")})
	var gotErr error
	if err != nil {
		gotErr = err // 建立阶段/窗口内即返回错误
	} else {
		_, gotErr = drainStreamErr(sr) // 窗口外错误在读取时透传
	}

	if gotErr == nil || !strings.Contains(gotErr.Error(), "input stream") {
		t.Fatalf("期望透传 'error, Error in input stream...'，got: %v", gotErr)
	}
	t.Logf("✅ 真实 SDK 路径复现成功（off 模式）：服务端 SSE error 被解析并透传 -> %v", gotErr)
}

// TestReproSSE_FullMode_Retries 真实 SDK 路径复现（full 模式）：
// 同样的 server（第 1 次发 error），full 模式完整缓冲捕获 error → 重试 → 第 2 次成功。
func TestReproSSE_FullMode_Retries(t *testing.T) {
	srv := newSSEReproServer(t, 1, []string{"你", "好", "回", "复"}) // 4 chunk
	defer srv.server.Close()

	cfg := &config.LLMConfig{
		Url:         "http://" + srv.addr + "/v1",
		Key:         "test-key",
		Model:       "test-model",
		MaxRetries:  3,
		StreamRetryMode: config.StreamRetryFull,
		Params:      config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 3})
	if err != nil {
		t.Fatal(err)
	}

	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("full 模式应重试成功，got: %v", err)
	}
	content, recvErr := drainStreamErr(sr)
	if recvErr != io.EOF {
		t.Fatalf("重放后应 io.EOF，got: %v", recvErr)
	}
	if srv.calls < 2 {
		t.Fatalf("full 模式应至少调用 server 2 次（重试），got %d", srv.calls)
	}
	t.Logf("✅ 真实 SDK 路径：full 模式重试成功（server 调用 %d 次），内容: %q", srv.calls, content)
}
