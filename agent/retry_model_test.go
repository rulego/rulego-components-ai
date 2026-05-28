package agent

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

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
