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

		// 防误判：时间戳/计数里的数字串不应被判为可重试（回归 containsHTTPStatus）
		{name: "timestamp 429 not rate limit", err: errors.New("event at 20260429 done"), expected: false},
		{name: "count 500 not server error", err: errors.New("processed 1500 items"), expected: false},

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

// TestRetryStream_NoRetryAfterProbeWindow 验证：断流发生在探测窗口（默认 3 个 chunk）之后，
// 不再重试（已向调用方输出，重试会重复内容），直接透传。窗口外内容 A、B、C、D 全部保留。
func TestRetryStream_NoRetryAfterProbeWindow(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A", "B", "C", "D"}, errors.New("Error in input stream"))},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3) // probeChunks 默认 3

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("越过窗口后应返回 reader（错误透传），got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if recvErr == nil || !strings.Contains(recvErr.Error(), "input stream") {
		t.Fatalf("期望透传 mid-stream 错误，got: %v", recvErr)
	}
	// 窗口外 4 个 chunk 全部保留，错误透传，不重试
	if len(contents) != 4 || contents[0] != "A" || contents[3] != "D" {
		t.Fatalf("期望收到 [A B C D]，got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("窗口外断流不应重试，期望 1 次，got %d", calls)
	}
}

// TestRetryStream_RetriesWithinProbeWindow 验证：断流发生在探测窗口内（默认 3 个 chunk），
// 会自动重试，且窗口内已收到的内容不会泄漏给调用方（避免重复）。
func TestRetryStream_RetriesWithinProbeWindow(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			// 第 1 次：只输出 A 后断流（窗口内），触发重试
			{stream: streamReaderWithChunksThenError([]string{"A"}, errors.New("Error in input stream"))},
			// 第 2 次：正常输出
			{stream: streamReaderFromChunks("Hello", "World")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("重试后应返回 reader，got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("重试成功后应 io.EOF，got: %v", recvErr)
	}
	// 关键：第 1 次窗口内的 A 不应泄漏，只看到第 2 次的 Hello World
	if len(contents) != 2 || contents[0] != "Hello" || contents[1] != "World" {
		t.Fatalf("期望只收到第 2 次的 [Hello World]（窗口内 A 不泄漏），got %v", contents)
	}
	if calls := fake.callsCount(); calls != 2 {
		t.Fatalf("窗口内断流应重试 1 次，期望 2 次调用，got %d", calls)
	}
}

// TestRetryStream_ShortStreamPassesThrough 验证：短流（chunk 数少于探测窗口）正常结束，
// 不被误判为断流，原样透传。
func TestRetryStream_ShortStreamPassesThrough(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			// 只有 1 个 chunk A 然后 EOF，少于 probeChunks=3
			{stream: streamReaderFromChunks("A")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("短流应返回 reader，got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("短流正常结束应 io.EOF，got: %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "A" {
		t.Fatalf("期望收到 [A]，got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("短流不应重试，期望 1 次，got %d", calls)
	}
}

// TestRetryStream_ProbeChunksOneKeepsOldBehavior 验证：SetProbeChunks(1) 退化为
// "仅探测首 chunk" 的旧行为——首 chunk 后的断流即视为越过窗口，直接透传不重试。
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
		t.Fatalf("应返回 reader，got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if recvErr == nil || !strings.Contains(recvErr.Error(), "input stream") {
		t.Fatalf("期望透传 mid-stream 错误，got: %v", recvErr)
	}
	// N=1：首 chunk A 越过窗口即透传，后续 B 和错误一并透传，不重试
	if len(contents) != 2 || contents[0] != "A" || contents[1] != "B" {
		t.Fatalf("期望收到 [A B]，got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("N=1 时首 chunk 后断流不应重试，期望 1 次，got %d", calls)
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

// ============================================
// full 模式（完整 mid-stream 重试）测试
// ============================================

// TestRetryStream_FullMode_RetriesMidStream full 模式：流中途断流 → 重试 → 成功（缓冲重放）。
// 关键：第 1 次已收到的 A、B 被丢弃重试，调用方只看到第 2 次的完整内容。
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
		t.Fatalf("期望重试后返回 reader，got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("期望 io.EOF，got: %v", recvErr)
	}
	// full 模式：第 1 次的 A、B 被丢弃重试，只看到第 2 次的 Hello World
	if len(contents) != 2 || contents[0] != "Hello" || contents[1] != "World" {
		t.Fatalf("期望 [Hello World]（A/B 被丢弃重试），got %v", contents)
	}
	if calls := fake.callsCount(); calls != 2 {
		t.Fatalf("期望 2 次调用，got %d", calls)
	}
}

// TestRetryStream_FullMode_Exhausts full 模式持续断流 → 重试耗尽，抛汇总错误。
func TestRetryStream_FullMode_Exhausts(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A"}, errors.New("Error in input stream"))},
			{stream: streamReaderWithChunksThenError([]string{"B"}, errors.New("Error in input stream"))},
		},
	}
	w := NewRetryChatModelWrapper(fake, 1) // maxRetries=1 → 共 2 次尝试
	w.SetStreamFull(true)

	sr, err := w.Stream(context.Background(), nil)
	if sr != nil {
		sr.Close()
	}
	if err == nil {
		t.Fatal("期望重试耗尽后报错")
	}
	if !strings.Contains(err.Error(), "Stream failed after 2 attempts") {
		t.Fatalf("期望汇总错误，got: %v", err)
	}
}

// TestRetryStream_FullMode_NormalReplays full 模式正常流 → 缓冲后完整重放。
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
		t.Fatalf("期望返回 reader，got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("期望 io.EOF，got: %v", recvErr)
	}
	if len(contents) != 3 || contents[0] != "A" || contents[2] != "C" {
		t.Fatalf("期望 [A B C] 完整重放，got %v", contents)
	}
	if calls := fake.callsCount(); calls != 1 {
		t.Fatalf("正常流不应重试，期望 1 次，got %d", calls)
	}
}

// TestRetry_WithTools_KeepsStreamFull 防回归：WithTools 必须保留 streamFull/probeChunks。
// eino react 绑工具必经 WithTools，若丢失 streamFull，full 模式会静默退化为 off，
// 窗口外断流不再重试而直接透传错误给调用方。
func TestRetry_WithTools_KeepsStreamFull(t *testing.T) {
	// 6 chunk 后断流：> 探测窗口 3，断流在窗口外。off 透传错误，full 重试成功。
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderWithChunksThenError([]string{"A", "B", "C", "D", "E", "F"}, errors.New("Error in input stream"))},
			{stream: streamReaderFromChunks("OK")},
		},
	}
	w := NewRetryChatModelWrapper(fake, 2)
	w.SetStreamFull(true)

	// 模拟 eino react 绑工具（生产路径必经）
	wt, err := w.WithTools(nil)
	if err != nil {
		t.Fatalf("WithTools 失败: %v", err)
	}

	sr, err := wt.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("WithTools 后 full 应保留并重试成功，got err: %v（说明 streamFull 被重置为 off）", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("full 应重放成功(io.EOF)，got %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("期望重试后 [OK]，got %v", contents)
	}
	if calls := fake.callsCount(); calls != 2 {
		t.Fatalf("full 应重试一次（共 2 次调用），got %d", calls)
	}
}
