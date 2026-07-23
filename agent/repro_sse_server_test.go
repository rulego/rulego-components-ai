package agent

// Using the local SSE server to run the real sashabaranov + eino-ext full-link reproduction of "Error in input stream".
// Unlike repro_midstream_test.go (fakeChatModel), here the server is real HTTP, sending OpenAI SSE
// Format (including data: {"error":...}), and the SDK provides real parsing.

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

// sseReproServer native OpenAI compatible with SSE server.
// breakOn request: first send chunks, then insert an SSE error event (simulating an internal error in Zhipu mid-stream);
// Other times: After sending chunks, normal [DONE] ends normally.
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
		// Insert SSE error event — simulates internal errors during Zhipu GLM stream generation
		errJSON, _ := json.Marshal(map[string]any{
			"error": map[string]any{
				"message": "Error in input stream (Please retry the request.)",
				"type":    "internal_error",
			},
		})
		writeSSE(string(errJSON))
		return // If [DONE] is not sent, after the connection closes, sashabaranov reads EOF and triggers unmarshalError
	}

	writeSSE("[DONE]")
}

// drainStreamErr consumes stream, returns (content concatenation, error); Normal termination err=io.EOF.
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

// TestReproSSE_OffMode_MidStreamError Real SDK path reproduction (off mode):
// After the local server sends 7 chunks on its first request, an SSE error event is inserted. Sashabaranov analyzed
// "error, Error in input stream (Please retry the request.)". off mode detection window = 3,
// error is passed → outside the window to the caller.
func TestReproSSE_OffMode_MidStreamError(t *testing.T) {
	srv := newSSEReproServer(t, 1, []string{"你", "好", "，", "这", "是", "回", "复"}) // 7 chunk > Window 3
	defer srv.server.Close()

	cfg := &config.LLMConfig{
		Url:             "http://" + srv.addr + "/v1",
		Key:             "test-key",
		Model:           "test-model",
		MaxRetries:      3,
		StreamRetryMode: config.StreamRetryOff,
		Params:          config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 3})
	if err != nil {
		t.Fatal(err)
	}

	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("hi")})
	var gotErr error
	if err != nil {
		gotErr = err // Returns an error during the creation phase/window
	} else {
		_, gotErr = drainStreamErr(sr) // Errors outside the window are transmitted during reading
	}

	if gotErr == nil || !strings.Contains(gotErr.Error(), "input stream") {
		t.Fatalf("I hope to convey 'error, Error in input stream...', got: %v", gotErr)
	}
	t.Logf("✅ True SDK path successfully reproduced (off mode): Server SSE error is parsed and transmitted as -> %v", gotErr)
}

// TestReproSSE_FullMode_Retries True SDK path reproduction (full mode):
// For the same server (first error issue), full mode fully buffers error capture→ retries → second successful attempt.
func TestReproSSE_FullMode_Retries(t *testing.T) {
	srv := newSSEReproServer(t, 1, []string{"你", "好", "回", "复"}) // 4 chunk
	defer srv.server.Close()

	cfg := &config.LLMConfig{
		Url:             "http://" + srv.addr + "/v1",
		Key:             "test-key",
		Model:           "test-model",
		MaxRetries:      3,
		StreamRetryMode: config.StreamRetryFull,
		Params:          config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 3})
	if err != nil {
		t.Fatal(err)
	}

	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("full The mode should be successfully retried and got: %v", err)
	}
	content, recvErr := drainStreamErr(sr)
	if recvErr != io.EOF {
		t.Fatalf("After replaying, it should be io.EOF and got: %v", recvErr)
	}
	if srv.calls < 2 {
		t.Fatalf("full mode should be called at least server twice (retry) got %d", srv.calls)
	}
	t.Logf("✅ True SDK path: full pattern retry successful (server call %d), content: %q", srv.calls, content)
}
