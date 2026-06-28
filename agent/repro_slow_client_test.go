package agent

// 复现“前端慢消费 → Error in input stream”。
//
// rulego 的 Stream 关系同步转发，前端慢会经 TellNext 反压到 Recv，把 LLM 服务端连接拖到
// 超时。本地 server 模拟 GLM/百炼/siliconflow 的行为：推送大 chunk，发现 Write 因 TCP 背压
// 变慢就发 “Error in input stream”。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
)

// slowFrontendServer 模拟 LLM 服务端在"客户端慢消费（TCP 背压）"时发 error 的行为。
// 推送大 chunk，每次 Write 后统计耗时；超过 slowThreshold（TCP 背压）则发 SSE error。
type slowFrontendServer struct {
	addr          string
	totalChunks   int
	chunkSize     int
	slowThreshold time.Duration // Write 耗时超过此值 = client 慢（TCP 背压）
	writeTimeout  time.Duration // 单次 Write 绝对超时（兜底）
	calls         int
	server        *http.Server
}

func newSlowFrontendServer(t *testing.T, total, chunkKB int, slow, wto time.Duration) *slowFrontendServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &slowFrontendServer{
		addr:          ln.Addr().String(),
		totalChunks:   total,
		chunkSize:     chunkKB * 1024,
		slowThreshold: slow,
		writeTimeout:  wto,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handle)
	s.server = &http.Server{Handler: mux}
	go func() { _ = s.server.Serve(ln) }()
	return s
}

func (s *slowFrontendServer) handle(w http.ResponseWriter, r *http.Request) {
	s.calls++
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	flusher, _ := w.(http.Flusher)

	writeSSE := func(payload string) {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	writeErr := func() {
		errJSON, _ := json.Marshal(map[string]any{
			"error": map[string]any{"message": "Error in input stream (Please retry the request.)", "type": "internal_error"},
		})
		writeSSE(string(errJSON))
	}

	big := strings.Repeat("a", s.chunkSize)
	for i := 0; i < s.totalChunks; i++ {
		_ = rc.SetWriteDeadline(time.Now().Add(s.writeTimeout))
		start := time.Now()
		chunkJSON, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion.chunk", "model": "test",
			"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": big}, "finish_reason": nil}},
		})
		writeSSE(string(chunkJSON))
		if elapsed := time.Since(start); elapsed > s.slowThreshold {
			// TCP 背压：client 没及时读，Write 阻塞超过阈值 → 模拟服务端超时发 error
			writeErr()
			return
		}
	}
	writeSSE("[DONE]")
}

// drainWithSlowdown 消费流，每个 chunk 后 sleep（模拟 rulego Stream 同步 TellNext + 慢前端）。
// slowPerChunk<=0 表示快消费（对照）。返回 (chunk数, 错误)。
func drainWithSlowdown(sr *schema.StreamReader[*schema.Message], slowPerChunk time.Duration) (int, error) {
	defer sr.Close()
	n := 0
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return n, nil // 正常结束
			}
			return n, err
		}
		if msg != nil && msg.Content != "" {
			n++
			if slowPerChunk > 0 {
				time.Sleep(slowPerChunk)
			}
		}
	}
}

// TestRepro_SlowFrontend_CausesInputStream 慢消费 → 服务端 TCP 背压 → 发 error。
// client 每个 chunk sleep 400ms（模拟慢前端），期望触发 "Error in input stream"。
func TestRepro_SlowFrontend_CausesInputStream(t *testing.T) {
	srv := newSlowFrontendServer(t, 20, 512, 200*time.Millisecond, 10*time.Second)
	defer srv.server.Close()

	cfg := &config.LLMConfig{
		Url: "http://" + srv.addr + "/v1", Key: "k", Model: "m",
		Params: config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
	m, err := CreateChatModel(*cfg, ModelOptions{}) // 裸模型，不包 retry，观察原始 error
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}

	n, gotErr := drainWithSlowdown(sr, 400*time.Millisecond)
	if gotErr == nil || !strings.Contains(gotErr.Error(), "input stream") {
		t.Fatalf("期望慢消费触发 'error, Error in input stream...'，收到 %d chunk，err=%v", n, gotErr)
	}
	t.Logf("✅ 复现成功：前端慢消费（sleep 400ms/chunk，收到 %d chunk 后）→ 服务端 TCP 背压 → %v", n, gotErr)
}

// TestRepro_FastFrontend_NoError 对照：快消费（不 sleep）→ 无背压 → 正常完成，不触发 error。
func TestRepro_FastFrontend_NoError(t *testing.T) {
	srv := newSlowFrontendServer(t, 20, 512, 200*time.Millisecond, 10*time.Second)
	defer srv.server.Close()

	cfg := &config.LLMConfig{
		Url: "http://" + srv.addr + "/v1", Key: "k", Model: "m",
		Params: config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
	m, err := CreateChatModel(*cfg, ModelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}

	n, gotErr := drainWithSlowdown(sr, 0) // 快消费，不 sleep
	if gotErr != nil {
		t.Fatalf("快消费不应触发 error，收到 %d chunk，err=%v", n, gotErr)
	}
	t.Logf("✅ 对照通过：快消费（不 sleep）收到全部 %d chunk，正常完成无错误", n)
}
