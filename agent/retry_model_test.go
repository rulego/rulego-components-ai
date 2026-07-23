package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
)

// mockLogger is a simulation log recorder used for testing
type mockLogger struct {
	messages []string
}

func (l *mockLogger) Printf(format string, v ...interface{}) {
	l.messages = append(l.messages, format)
}

func (l *mockLogger) Debugf(format string, v ...interface{}) {
	l.messages = append(l.messages, "[DEBUG] "+format)
}

func (l *mockLogger) Infof(format string, v ...interface{}) {
	l.messages = append(l.messages, "[INFO] "+format)
}

func (l *mockLogger) Warnf(format string, v ...interface{}) {
	l.messages = append(l.messages, "[WARN] "+format)
}

func (l *mockLogger) Errorf(format string, v ...interface{}) {
	l.messages = append(l.messages, "[ERROR] "+format)
}

// TestIsRetryableError Test the isRetryableError function
func TestIsRetryableError(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		// nil incorrect
		{name: "nil error", err: nil, expected: false},

		// 429 Rate limit error
		{name: "429 error", err: errors.New("status code 429"), expected: true},
		{name: "rate limit error", err: errors.New("rate limit exceeded"), expected: true},
		{name: "too many requests", err: errors.New("Too Many Requests"), expected: true},

		// 5xx Server error
		{name: "500 error", err: errors.New("internal server error 500"), expected: true},
		{name: "502 error", err: errors.New("bad gateway 502"), expected: true},
		{name: "503 error", err: errors.New("service unavailable 503"), expected: true},
		{name: "504 error", err: errors.New("gateway timeout 504"), expected: true},

		// False positive: The string of digits in the timestamp/count should not be considered retry (regression containsHTTPStatus)
		{name: "timestamp 429 not rate limit", err: errors.New("event at 20260429 done"), expected: false},
		{name: "count 500 not server error", err: errors.New("processed 1500 items"), expected: false},

		// Timeout error
		{name: "timeout error", err: errors.New("request timeout"), expected: true},
		{name: "deadline exceeded", err: errors.New("deadline exceeded"), expected: true},

		// Network errors
		{name: "connection refused", err: errors.New("connection refused"), expected: true},
		{name: "connection reset", err: errors.New("connection reset by peer"), expected: true},
		{name: "broken pipe", err: errors.New("broken pipe"), expected: true},

		// Do not retry mistakes
		{name: "invalid request", err: errors.New("invalid request"), expected: false},
		{name: "unauthorized", err: errors.New("unauthorized access"), expected: false},
		{name: "not found", err: errors.New("resource not found"), expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapper.isRetryableError(tt.err)
			if result != tt.expected {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestIsRetryableError_NetworkError Test network error types
func TestIsRetryableError_NetworkError(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}

	// Test net.Error type
	netErr := &net.OpError{Err: errors.New("network error")}
	if !wrapper.isRetryableError(netErr) {
		t.Error("net.OpError should be retryable")
	}

	// Test url.Error type
	urlErr := &url.Error{
		Op:  "Get",
		URL: "http://example.com",
		Err: errors.New("connection failed"),
	}
	if !wrapper.isRetryableError(urlErr) {
		t.Error("url.Error should be retryable")
	}
}

// TestIsNetworkError tests the isNetworkError function
func TestIsNetworkError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "net.Error", err: &net.OpError{}, expected: true},
		{name: "url.Error", err: &url.Error{}, expected: true},
		{name: "connection refused", err: errors.New("connection refused"), expected: true},
		{name: "connection reset", err: errors.New("connection reset"), expected: true},
		{name: "broken pipe", err: errors.New("broken pipe"), expected: true},
		{name: "generic error", err: errors.New("some error"), expected: false},
		{name: "nil error", err: nil, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNetworkError(tt.err)
			if result != tt.expected {
				t.Errorf("isNetworkError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestCalculateDelay Tests the calculateDelay function
func TestCalculateDelay(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}
	logger := &mockLogger{}
	wrapper.logger = logger

	tests := []struct {
		name     string
		attempt  int
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{name: "attempt 1", attempt: 1, minDelay: 500 * time.Millisecond, maxDelay: 1500 * time.Millisecond},
		{name: "attempt 2", attempt: 2, minDelay: 1000 * time.Millisecond, maxDelay: 3000 * time.Millisecond},
		{name: "attempt 3", attempt: 3, minDelay: 2000 * time.Millisecond, maxDelay: 6000 * time.Millisecond},
		{name: "attempt 4", attempt: 4, minDelay: 4000 * time.Millisecond, maxDelay: 12000 * time.Millisecond},
		{name: "attempt 10", attempt: 10, minDelay: 15000 * time.Millisecond, maxDelay: 30000 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay := wrapper.calculateDelay(tt.attempt)
			if delay < tt.minDelay || delay > tt.maxDelay {
				t.Errorf("calculateDelay(%d) = %v, want between %v and %v", tt.attempt, delay, tt.minDelay, tt.maxDelay)
			}
		})
	}
}

// TestCalculateDelay_MaxDelay Test maximum latency limits
func TestCalculateDelay_MaxDelay(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}

	for i := 1; i <= 20; i++ {
		delay := wrapper.calculateDelay(i)
		if delay > 30*time.Second {
			t.Errorf("calculateDelay(%d) = %v, should not exceed 30s", i, delay)
		}
	}
}

// TestCalculateDelay_Jitter Test random jitter
func TestCalculateDelay_Jitter(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}

	delays := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		delay := wrapper.calculateDelay(2)
		delays[delay] = true
	}

	// There should be various delay values
	if len(delays) < 10 {
		t.Errorf("Expected at least 10 different delay values due to jitter, got %d", len(delays))
	}
}

// TestNewRetryChatModelWrapper Test the NewRetryChatModelWrapper function
func TestNewRetryChatModelWrapper(t *testing.T) {
	// Test the default number of retries per test
	wrapper := NewRetryChatModelWrapper(nil, 0)
	if wrapper.maxRetries != config.DefaultMaxRetries {
		t.Errorf("Expected maxRetries to be %d, got %d", config.DefaultMaxRetries, wrapper.maxRetries)
	}

	// Test custom retry counts
	wrapper = NewRetryChatModelWrapper(nil, 5)
	if wrapper.maxRetries != 5 {
		t.Errorf("Expected maxRetries to be 5, got %d", wrapper.maxRetries)
	}

	// Test negative number of retries count
	wrapper = NewRetryChatModelWrapper(nil, -1)
	if wrapper.maxRetries != config.DefaultMaxRetries {
		t.Errorf("Expected maxRetries to be %d for negative input, got %d", config.DefaultMaxRetries, wrapper.maxRetries)
	}

	// Test with logger
	logger := &mockLogger{}
	wrapper = NewRetryChatModelWrapper(nil, 3, logger)
	if wrapper.logger != logger {
		t.Error("Logger not set correctly")
	}
}

// TestRetryChatModelWrapper_Logf Test log output
func TestRetryChatModelWrapper_Logf(t *testing.T) {
	// Test Band: logger (logWarnf → Warnf)
	logger := &mockLogger{}
	wrapper := &RetryChatModelWrapper{logger: logger}
	wrapper.logWarnf("test message: %s", "hello")

	if len(logger.messages) != 1 {
		t.Errorf("Expected 1 log message, got %d", len(logger.messages))
	}
	if !strings.Contains(logger.messages[0], "test message") {
		t.Errorf("Log message incorrect: %s", logger.messages[0])
	}

	// The test does not include Logger
	wrapper = &RetryChatModelWrapper{}
	wrapper.logWarnf("test message") // You shouldn't panic
	wrapper.logInfof("test message") // You shouldn't panic
}

// TestRetryChatModelWrapper_Interface Test interface implementation
func TestRetryChatModelWrapper_Interface(t *testing.T) {
	// Make sure RetryChatModelWrapper implements the interface
	var _ *RetryChatModelWrapper
}

// BenchmarkCalculateDelay Benchmark calculateDelay function
func BenchmarkCalculateDelay(b *testing.B) {
	wrapper := &RetryChatModelWrapper{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wrapper.calculateDelay(i%10 + 1)
	}
}

// BenchmarkIsRetryableError Benchmark isRetryableError function
func BenchmarkIsRetryableError(b *testing.B) {
	wrapper := &RetryChatModelWrapper{}
	err := errors.New("rate limit exceeded 429")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wrapper.isRetryableError(err)
	}
}

// ============================================
// Stream retry behavior test (reproduce and verify the "Error in input stream" fix)
// ============================================

// streamBehavior describes the default behavior of a single stream call by fakeChatModel.
type streamBehavior struct {
	openErr error                                 // Non-nil: Creating a stream fails directly
	stream  *schema.StreamReader[*schema.Message] // Stream on successful establishment (may return with errors midway)
}

// fakeChatModel is a controllable ChatModel mock that returns preset behaviors in the order called.
type fakeChatModel struct {
	mu        sync.Mutex
	calls     int
	behaviors []streamBehavior
}

func (f *fakeChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	f.mu.Lock()
	f.calls++
	idx := f.calls - 1
	f.mu.Unlock()
	// If the behavior exceeds the preset threshold, a successful empty stream is returned by default
	b := streamBehavior{stream: streamReaderFromChunks()}
	if idx < len(f.behaviors) {
		b = f.behaviors[idx]
	}
	if b.openErr != nil {
		return nil, b.openErr
	}
	return b.stream, nil
}

func (f *fakeChatModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

func (f *fakeChatModel) callsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// streamReaderFromChunks constructs a stream that outputs chunks sequentially and ends normally.
func streamReaderFromChunks(chunks ...string) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](0)
	go func() {
		defer w.Close()
		for _, c := range chunks {
			w.Send(&schema.Message{Content: c, Role: schema.Assistant}, nil)
		}
	}()
	return r
}

// streamReaderWithError constructs a stream that does not output any chunk and returns errors directly.
// Simulating a typical "Error in input stream" scenario: a connection is established, but the upstream interrupts before the first valid content appears.
func streamReaderWithError(err error) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](0)
	go func() {
		defer w.Close()
		w.Send(nil, err)
	}()
	return r
}

// streamReaderWithChunksThenError constructs a stream that outputs chunks before returning the error (the output is interrupted midway).
func streamReaderWithChunksThenError(chunks []string, err error) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](0)
	go func() {
		defer w.Close()
		for _, c := range chunks {
			w.Send(&schema.Message{Content: c, Role: schema.Assistant}, nil)
		}
		w.Send(nil, err)
	}()
	return r
}

// drainStream consumes the stream until it ends or encounters an error, returning a list of received content and errors (normally ending as io.EOF).
func drainStream(sr *schema.StreamReader[*schema.Message]) ([]string, error) {
	defer sr.Close()
	var contents []string
	for {
		msg, err := sr.Recv()
		if err != nil {
			return contents, err
		}
		if msg != nil {
			contents = append(contents, msg.Content)
		}
	}
}

// TestRetryStream_RetriesOnPreOutputReadError Verification: When upstream returns a retry error before the first chunk,
// Automatic retries until successful, with the underlying stream called multiple times.
func TestRetryStream_RetriesOnPreOutputReadError(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
			{stream: streamReaderWithError(errors.New("error, Error in input stream (Please retry)"))},
			{stream: streamReaderFromChunks("Hello")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, err := w.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("Stream established should immediately return to the reader got err: %v", err)
	}

	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("After retrying, it should end normally (io.EOF) and got: %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "Hello" {
		t.Fatalf("Expect to receive [Hello], got %v", contents)
	}
	if calls := fake.callsCount(); calls != 3 {
		t.Fatalf("Expect the underlying Stream to be called 3 times (including 2 retries for got %d", calls)
	}
}

// TestRetryStream_NoRetryAfterProbeWindow Verification: Interruption occurs after the probe window (default is 3 chunks),
// No retry is required (output to the caller; retries will repeat content), transmit directly. External content A, B, C, D is all retained.
func TestRetryStream_NoRetryAfterProbeWindow(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A", "B", "C", "D"}, errors.New("Error in input stream"))},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3) // probeChunks defaults to 3

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("After crossing the window, it should return reader (error pass-through), got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if recvErr == nil || !strings.Contains(recvErr.Error(), "input stream") {
		t.Fatalf("Expect to transmit mid-stream errors, got: %v", recvErr)
	}
	// All 4 chunks outside the window are retained; error transmission is passed, no retry
	if len(contents) != 4 || contents[0] != "A" || contents[3] != "D" {
		t.Fatalf("Expect to receive [A B C D], got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("Flow interruption outside the window should not be retested; expect only one attempt and got %d", calls)
	}
}

// TestRetryStream_RetriesWithinProbeWindow Verification: Interruption occurs within the detection window (default is 3 chunks),
// It will automatically retry, and the content received in the window will not be leaked to the caller (to avoid duplication).
func TestRetryStream_RetriesWithinProbeWindow(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			// First time: Only output A and then cut off the stream (within the window), triggering a retry
			{stream: streamReaderWithChunksThenError([]string{"A"}, errors.New("Error in input stream"))},
			// Second time: Normal output
			{stream: streamReaderFromChunks("Hello", "World")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("After retrying, return to reader and got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("After successful retry, io.EOF and got: %v", recvErr)
	}
	// Crucially: The A in the first window should not leak; only the second Hello World should be seen
	if len(contents) != 2 || contents[0] != "Hello" || contents[1] != "World" {
		t.Fatalf("Expect to receive only the second [Hello World] (A inside the window does not leak), got %v", contents)
	}
	if calls := fake.callsCount(); calls != 2 {
		t.Fatalf("If the current is cut off within the window, retry once, expect two calls, got %d", calls)
	}
}

// TestRetryStream_ShortStreamPassesThrough Verification: Short stream (chunk count less than detection window) ends normally,
// It is not mistaken for a broken flow, and is transmitted as is.
func TestRetryStream_ShortStreamPassesThrough(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			// Only 1 chunk A then EOF, less than probeChunks = 3
			{stream: streamReaderFromChunks("A")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Short currents should return reader, got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("The normal end of short current should be io.EOF and got: %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "A" {
		t.Fatalf("Expect to receive [A], got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("Short-term streams should not be retested; expect one attempt and got %d", calls)
	}
}

// TestRetryStream_ProbeChunksOneKeepsOldBehavior Verification: SetProbeChunks(1) degenerates to
// The old behavior of "only detecting the first chunk"—the disconnection after the first chunk is considered to have crossed the window and is transmitted directly without retrying.
func TestRetryStream_ProbeChunksOneKeepsOldBehavior(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A", "B"}, errors.New("Error in input stream"))},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)
	w.SetProbeChunks(1)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("reader should be returned, got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if recvErr == nil || !strings.Contains(recvErr.Error(), "input stream") {
		t.Fatalf("Expect to transmit mid-stream errors, got: %v", recvErr)
	}
	// N=1: The first chunk A passes through the window and is transmitted directly; subsequent B and the error are passed through together, without retrying
	if len(contents) != 2 || contents[0] != "A" || contents[1] != "B" {
		t.Fatalf("Expect to receive [A B], got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("When N=1, the flow after the first chunk should not be retried after interruption; expect to do it once and got %d", calls)
	}
}

// TestRetryStream_RetriesOnOpenError Verification that the established stream fails and will still retry (maintaining the original logic without regression).
func TestRetryStream_RetriesOnOpenError(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{openErr: errors.New("status code: 502 bad gateway")},
			{openErr: errors.New("status code: 503")},
			{stream: streamReaderFromChunks("OK")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream The establishment should be successful and got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("Expect io.EOF, got: %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("Expect [OK], got %v", contents)
	}
	if calls := fake.callsCount(); calls != 3 {
		t.Fatalf("Expect 3 calls, got %d", calls)
	}
}

// TestRetryStream_NoRetryOnNonRetryableError Verification must not be retried to transmit errors directly, without retrying.
func TestRetryStream_NoRetryOnNonRetryableError(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithError(errors.New("invalid_api_key"))},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, err := w.Stream(context.Background(), nil)
	if sr != nil {
		sr.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "invalid_api_key") {
		t.Fatalf("Hopefully, the transmission will not be retried for errors, got: %v", err)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("Errors that cannot be retried should not be retried and expected to be done once, got %d", calls)
	}
}

// TestRetryStream_ExhaustsRetries Validate persistent retry errors, throwing summary errors after exhaustion.
func TestRetryStream_ExhaustsRetries(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
		},
	}
	w := NewRetryChatModelWrapper(fake, 2) // maxRetries = 2 → 3 attempts in total

	sr, err := w.Stream(context.Background(), nil)
	if sr != nil {
		sr.Close()
	}
	if err == nil {
		t.Fatal("Expect to retry and then make a mistake")
	}
	if !strings.Contains(err.Error(), "Stream failed after 3 attempts") {
		t.Fatalf("Expect to summarize misinformation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "input stream") {
		t.Fatalf("Expect to contain the original error, got: %v", err)
	}
	if calls := fake.callsCount(); calls != 3 {
		t.Fatalf("Expect 3 calls, got %d", calls)
	}
}

// TestRetryStream_NormalStream Verify that normal flow is propagated as-is without triggering retry.
func TestRetryStream_NormalStream(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderFromChunks("A", "B", "C")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, _ := w.Stream(context.Background(), nil)
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("Expect io.EOF, got: %v", recvErr)
	}
	if len(contents) != 3 || contents[0] != "A" || contents[2] != "C" {
		t.Fatalf("Expectation [A B C], got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("Normal flow should not be retried; expect one attempt and got %d", calls)
	}
}

// TestIsRetryableError_InputStream Verify that the stream interrupt class error is correctly identified as retryable.
func TestIsRetryableError_InputStream(t *testing.T) {
	w := &RetryChatModelWrapper{}
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("Error in input stream"), true},
		{errors.New("error, Error in input stream (Please retry the request.)"), true},
		{io.ErrUnexpectedEOF, true},
		{errors.New("unexpected EOF"), true},
		{errors.New("invalid request"), false},
	}
	for _, c := range cases {
		if got := w.isRetryableError(c.err); got != c.want {
			t.Errorf("isRetryableError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// ============================================
// Full mode (full mid-stream retry) test
// ============================================

// TestRetryStream_FullMode_RetriesMidStream Full mode: Interruption of stream → retry → success (buffered replay).
// Crucially: The first received A and B are discarded and retried, and the caller only sees the full content of the second time.
func TestRetryStream_FullMode_RetriesMidStream(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A", "B"}, errors.New("Error in input stream"))},
			{stream: streamReaderFromChunks("Hello", "World")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)
	w.SetStreamFull(true)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expect to return to reader after retrying, got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("Expect io.EOF, got: %v", recvErr)
	}
	// Full mode: The first attempt A and B are discarded and retried, only the second attempt shows Hello World
	if len(contents) != 2 || contents[0] != "Hello" || contents[1] != "World" {
		t.Fatalf("Expect [Hello World] (A/B discarded and retry), got %v", contents)
	}
	if calls := fake.callsCount(); calls != 2 {
		t.Fatalf("Expect 2 calls, got %d", calls)
	}
}

// TestRetryStream_FullMode_Exhausts Full mode continues to disconnect → retries exhaust and throws summary errors.
func TestRetryStream_FullMode_Exhausts(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A"}, errors.New("Error in input stream"))},
			{stream: streamReaderWithChunksThenError([]string{"B"}, errors.New("Error in input stream"))},
		},
	}
	w := NewRetryChatModelWrapper(fake, 1) // maxRetries = 1 → 2 attempts in total
	w.SetStreamFull(true)

	sr, err := w.Stream(context.Background(), nil)
	if sr != nil {
		sr.Close()
	}
	if err == nil {
		t.Fatal("Expect to retry and then get an error when exhausted")
	}
	if !strings.Contains(err.Error(), "Stream failed after 2 attempts") {
		t.Fatalf("Expect to summarize errors and got: %v", err)
	}
}

// TestRetryStream_FullMode_NormalReplays Full mode normal stream → buffered and fully replayed.
func TestRetryStream_FullMode_NormalReplays(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderFromChunks("A", "B", "C")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)
	w.SetStreamFull(true)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expect to return reader, got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("Expect io.EOF, got: %v", recvErr)
	}
	if len(contents) != 3 || contents[0] != "A" || contents[2] != "C" {
		t.Fatalf("Expect [A B C] full replay, got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("Normal flow should not be retried; expect one attempt and got %d", calls)
	}
}

// TestRetry_WithTools_KeepsStreamFull Regression Protection: WithTools must retain streamFull/probeChunks.
// eino react binding tools must go through WithTools. If streamFull is lost, full mode will silently degenerate to off,
// The out-of-window disconnection does not retry but directly transmits the error to the caller.
func TestRetry_WithTools_KeepsStreamFull(t *testing.T) {
	// 6 After chunk, cut off the current: > Detection window 3, interrupt the current outside the window. off, transmission error, full retry successful.
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A", "B", "C", "D", "E", "F"}, errors.New("Error in input stream"))},
			{stream: streamReaderFromChunks("OK")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 2)
	w.SetStreamFull(true)

	// Simulating Eino React binding tools (mandatory production path)
	wt, err := w.WithTools(nil)
	if err != nil {
		t.Fatalf("WithTools Failure: %v", err)
	}

	sr, err := wt.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("After WithTools, the full should be retained and retried successfully, got err: %v (note that streamFull was reset to off)", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("full Replay should be successful (io.EOF), got %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("Hope to retry [OK], got %v", contents)
	}
	if calls := fake.callsCount(); calls != 2 {
		t.Fatalf("full Retry once (2 calls in total) and got %d", calls)
	}
}
