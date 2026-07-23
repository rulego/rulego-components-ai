package agent

// Reproduces "Front-end slow consumption → Error in input stream."
//
// Stream-related synchronous forwarding in rulego, the frontend slowly, via TellNext, pushes it to Recv, dragging the LLM server connection to
// Overtime. Local server simulates GLM/Bailian/siliconflow behavior: pushes a large chunk, discovers write is backloading due to TCP
// If it slows down, it sends "Error in input stream".

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

// slowFrontendServer simulates the behavior of the LLM server when it issues an error during "client-side slow consumption (TCP backpressure)".
// Push large chunks, and count the time spent after each write; If the slowThreshold (TCP backpressure) is exceeded, an SSE error is issued.
type slowFrontendServer struct {
	addr          string
	totalChunks   int
	chunkSize     int
	slowThreshold time.Duration // Write time exceeding this value = client slow (TCP backpressure)
	writeTimeout  time.Duration // Absolute timeout for single writes (as a backup)
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
			// TCP backpressure: client does not read in time, write blocks exceed thresholds → simulates server timeout and error
			writeErr()
			return
		}
	}
	writeSSE("[DONE]")
}

// drainWithSlowdown consumes streams, sleeping after each chunk (simulates a rulego stream synchronizing TellNext + slow frontend).
// slowPerChunk<=0 indicates fast consumption (control). Returns (chunk count, invalid).
func drainWithSlowdown(sr *schema.StreamReader[*schema.Message], slowPerChunk time.Duration) (int, error) {
	defer sr.Close()
	n := 0
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return n, nil // Ends normally
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

// TestRepro_SlowFrontend_CausesInputStream Slow consumption → server-side TCP backpressure → send errors.
// Each client chunk sleep 400ms (simulating a slow frontend), expecting to trigger "Error in input stream".
func TestRepro_SlowFrontend_CausesInputStream(t *testing.T) {
	srv := newSlowFrontendServer(t, 20, 512, 200*time.Millisecond, 10*time.Second)
	defer srv.server.Close()

	cfg := &config.LLMConfig{
		Url: "http://" + srv.addr + "/v1", Key: "k", Model: "m",
		Params: config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
	m, err := CreateChatModel(*cfg, ModelOptions{}) // Bare models, no retry, observe the original error
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}

	n, gotErr := drainWithSlowdown(sr, 400*time.Millisecond)
	if gotErr == nil || !strings.Contains(gotErr.Error(), "input stream") {
		t.Fatalf("Expect slow consumption to trigger 'error, Error in input stream...', receive %d chunk, err = %v", n, gotErr)
	}
	t.Logf("✅ Successful reproduction: Front-end slow consumption (sleep 400ms/chunk, after receiving %d chunk) → server TCP backpressure → %v", n, gotErr)
}

// TestRepro_FastFrontend_NoError Comparison: Fast consumption (no sleep) → no backpressure→ Completed normally, no error triggered.
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

	n, gotErr := drainWithSlowdown(sr, 0) // Spend quickly, don't sleep
	if gotErr != nil {
		t.Fatalf("Fast spending should not trigger error, receive %d chunk, err = %v", n, gotErr)
	}
	t.Logf("✅ Verification passed: Kuai Consumption (not sleep) received all %d chunk, completed normally without errors", n)
}
