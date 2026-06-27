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

// mockLogger 用于测试的模拟日志记录器
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

// TestIsRetryableError 测试 isRetryableError 函数
func TestIsRetryableError(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		// nil 错误
		{name: "nil error", err: nil, expected: false},

		// 429 速率限制错误
		{name: "429 error", err: errors.New("status code 429"), expected: true},
		{name: "rate limit error", err: errors.New("rate limit exceeded"), expected: true},
		{name: "too many requests", err: errors.New("Too Many Requests"), expected: true},

		// 5xx 服务器错误
		{name: "500 error", err: errors.New("internal server error 500"), expected: true},
		{name: "502 error", err: errors.New("bad gateway 502"), expected: true},
		{name: "503 error", err: errors.New("service unavailable 503"), expected: true},
		{name: "504 error", err: errors.New("gateway timeout 504"), expected: true},

		// 超时错误
		{name: "timeout error", err: errors.New("request timeout"), expected: true},
		{name: "deadline exceeded", err: errors.New("deadline exceeded"), expected: true},

		// 网络错误
		{name: "connection refused", err: errors.New("connection refused"), expected: true},
		{name: "connection reset", err: errors.New("connection reset by peer"), expected: true},
		{name: "broken pipe", err: errors.New("broken pipe"), expected: true},

		// 不可重试错误
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

// TestIsRetryableError_NetworkError 测试网络错误类型
func TestIsRetryableError_NetworkError(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}

	// 测试 net.Error 类型
	netErr := &net.OpError{Err: errors.New("network error")}
	if !wrapper.isRetryableError(netErr) {
		t.Error("net.OpError should be retryable")
	}

	// 测试 url.Error 类型
	urlErr := &url.Error{
		Op:  "Get",
		URL: "http://example.com",
		Err: errors.New("connection failed"),
	}
	if !wrapper.isRetryableError(urlErr) {
		t.Error("url.Error should be retryable")
	}
}

// TestIsNetworkError 测试 isNetworkError 函数
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

// TestCalculateDelay 测试 calculateDelay 函数
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

// TestCalculateDelay_MaxDelay 测试最大延迟限制
func TestCalculateDelay_MaxDelay(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}

	for i := 1; i <= 20; i++ {
		delay := wrapper.calculateDelay(i)
		if delay > 30*time.Second {
			t.Errorf("calculateDelay(%d) = %v, should not exceed 30s", i, delay)
		}
	}
}

// TestCalculateDelay_Jitter 测试随机抖动
func TestCalculateDelay_Jitter(t *testing.T) {
	wrapper := &RetryChatModelWrapper{}

	delays := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		delay := wrapper.calculateDelay(2)
		delays[delay] = true
	}

	// 应该有多种不同的延迟值
	if len(delays) < 10 {
		t.Errorf("Expected at least 10 different delay values due to jitter, got %d", len(delays))
	}
}

// TestNewRetryChatModelWrapper 测试 NewRetryChatModelWrapper 函数
func TestNewRetryChatModelWrapper(t *testing.T) {
	// 测试默认重试次数
	wrapper := NewRetryChatModelWrapper(nil, 0)
	if wrapper.maxRetries != config.DefaultMaxRetries {
		t.Errorf("Expected maxRetries to be %d, got %d", config.DefaultMaxRetries, wrapper.maxRetries)
	}

	// 测试自定义重试次数
	wrapper = NewRetryChatModelWrapper(nil, 5)
	if wrapper.maxRetries != 5 {
		t.Errorf("Expected maxRetries to be 5, got %d", wrapper.maxRetries)
	}

	// 测试负数重试次数
	wrapper = NewRetryChatModelWrapper(nil, -1)
	if wrapper.maxRetries != config.DefaultMaxRetries {
		t.Errorf("Expected maxRetries to be %d for negative input, got %d", config.DefaultMaxRetries, wrapper.maxRetries)
	}

	// 测试带 logger
	logger := &mockLogger{}
	wrapper = NewRetryChatModelWrapper(nil, 3, logger)
	if wrapper.logger != logger {
		t.Error("Logger not set correctly")
	}
}

// TestRetryChatModelWrapper_Logf 测试日志输出
func TestRetryChatModelWrapper_Logf(t *testing.T) {
	// 测试带 logger
	logger := &mockLogger{}
	wrapper := &RetryChatModelWrapper{logger: logger}
	wrapper.logf("test message: %s", "hello")

	if len(logger.messages) != 1 {
		t.Errorf("Expected 1 log message, got %d", len(logger.messages))
	}
	if !strings.Contains(logger.messages[0], "test message") {
		t.Errorf("Log message incorrect: %s", logger.messages[0])
	}

	// 测试不带 logger
	wrapper = &RetryChatModelWrapper{}
	wrapper.logf("test message") // 不应该 panic
}

// TestRetryChatModelWrapper_Interface 测试接口实现
func TestRetryChatModelWrapper_Interface(t *testing.T) {
	// 确保 RetryChatModelWrapper 实现了接口
	var _ *RetryChatModelWrapper
}

// BenchmarkCalculateDelay 基准测试 calculateDelay 函数
func BenchmarkCalculateDelay(b *testing.B) {
	wrapper := &RetryChatModelWrapper{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wrapper.calculateDelay(i%10 + 1)
	}
}

// BenchmarkIsRetryableError 基准测试 isRetryableError 函数
func BenchmarkIsRetryableError(b *testing.B) {
	wrapper := &RetryChatModelWrapper{}
	err := errors.New("rate limit exceeded 429")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wrapper.isRetryableError(err)
	}
}

// ============================================
// Stream 重试行为测试（复现并验证 "Error in input stream" 的修复）
// ============================================

// streamBehavior 描述 fakeChatModel 单次 Stream 调用的预设行为。
type streamBehavior struct {
	openErr error                                 // 非 nil：建立流直接失败
	stream  *schema.StreamReader[*schema.Message] // 建立成功时的流（可能在中途返回错误）
}

// fakeChatModel 是可控的 ChatModel mock，按调用顺序依次返回预设行为。
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
	// 超出预设行为时默认返回一个成功的空流
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

// streamReaderFromChunks 构造一个依次输出 chunks 后正常结束的流。
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

// streamReaderWithError 构造一个不输出任何 chunk、直接返回错误的流。
// 模拟 "Error in input stream" 的典型场景：连接已建立，但首个有效内容前上游即中断。
func streamReaderWithError(err error) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](0)
	go func() {
		defer w.Close()
		w.Send(nil, err)
	}()
	return r
}

// streamReaderWithChunksThenError 构造一个先输出 chunks 再返回错误的流（已输出后的中途断流）。
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

// drainStream 消费流直到结束或出错，返回收到的 content 列表与错误（正常结束时为 io.EOF）。
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

// TestRetryStream_RetriesOnPreOutputReadError 验证：上游在首个 chunk 前返回可重试错误时，
// 自动重试直到成功，底层 Stream 被调用多次。
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
		t.Fatalf("Stream 建立应立即返回 reader，got err: %v", err)
	}

	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("重试后应正常结束(io.EOF)，got: %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "Hello" {
		t.Fatalf("期望收到 [Hello]，got %v", contents)
	}
	if calls := fake.callsCount(); calls != 3 {
		t.Fatalf("期望底层 Stream 被调用 3 次（含 2 次重试），got %d", calls)
	}
}

// TestRetryStream_RetriesOnMidStreamError 验证：上游已输出若干 chunk 后中途断流，
// 重试后拿到完整内容且不重复（中途断流的部分被丢弃）。
func TestRetryStream_RetriesOnMidStreamError(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A", "B"}, errors.New("Error in input stream"))},
			{stream: streamReaderFromChunks("OK")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream 应返回 reader，got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("重试后应正常结束(io.EOF)，got: %v", recvErr)
	}
	// 第一次的 A、B 因断流被整体丢弃，只保留重试后的 OK，绝不重复
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("期望仅 [OK]（中途断流的部分被丢弃、无重复），got %v", contents)
	}
	if calls := fake.callsCount(); calls != 2 {
		t.Fatalf("期望 2 次调用（1 次中途断流 + 1 次重试成功），got %d", calls)
	}
}

// TestRetryStream_RetriesOnOpenError 验证建立流失败仍会重试（保持原有逻辑不回归）。
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
		t.Fatalf("Stream 建立应成功，got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("期望 io.EOF，got: %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("期望 [OK]，got %v", contents)
	}
	if calls := fake.callsCount(); calls != 3 {
		t.Fatalf("期望 3 次调用，got %d", calls)
	}
}

// TestRetryStream_NoRetryOnNonRetryableError 验证不可重试错误直接透传、不重试。
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
		t.Fatalf("期望透传不可重试错误，got: %v", err)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("不可重试错误不应重试，期望 1 次，got %d", calls)
	}
}

// TestRetryStream_ExhaustsRetries 验证持续可重试错误在用尽次数后抛出汇总错误。
func TestRetryStream_ExhaustsRetries(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
			{stream: streamReaderWithError(errors.New("Error in input stream"))},
		},
	}
	w := NewRetryChatModelWrapper(fake, 2) // maxRetries=2 → 共 3 次尝试

	sr, err := w.Stream(context.Background(), nil)
	if sr != nil {
		sr.Close()
	}
	if err == nil {
		t.Fatal("期望用尽重试后抛错")
	}
	if !strings.Contains(err.Error(), "Stream failed after 3 attempts") {
		t.Fatalf("期望汇总错误信息，got: %v", err)
	}
	if !strings.Contains(err.Error(), "input stream") {
		t.Fatalf("期望包含原始错误，got: %v", err)
	}
	if calls := fake.callsCount(); calls != 3 {
		t.Fatalf("期望 3 次调用，got %d", calls)
	}
}

// TestRetryStream_NormalStream 验证正常流被原样透传、不触发重试。
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
		t.Fatalf("期望 io.EOF，got: %v", recvErr)
	}
	if len(contents) != 3 || contents[0] != "A" || contents[2] != "C" {
		t.Fatalf("期望 [A B C]，got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("正常流不应重试，期望 1 次，got %d", calls)
	}
}

// TestIsRetryableError_InputStream 验证流中断类错误被正确识别为可重试。
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
