package agent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	aierrors "github.com/rulego/rulego-components-ai/errors"
	"github.com/rulego/rulego/api/types"
)

// RetryChatModelWrapper 包装 ChatModel 以提供重试功能
type RetryChatModelWrapper struct {
	model.ToolCallingChatModel
	maxRetries int
	logger     types.Logger
}

// NewRetryChatModelWrapper 创建带重试功能的 ChatModel 包装器
func NewRetryChatModelWrapper(baseModel model.ToolCallingChatModel, maxRetries int, logger ...types.Logger) *RetryChatModelWrapper {
	if maxRetries <= 0 {
		maxRetries = config.DefaultMaxRetries
	}
	var log types.Logger
	if len(logger) > 0 && logger[0] != nil {
		log = logger[0]
	}
	return &RetryChatModelWrapper{
		ToolCallingChatModel: baseModel,
		maxRetries:           maxRetries,
		logger:               log,
	}
}

// logf 日志输出，如果 logger 存在则使用 logger，否则不输出
func (w *RetryChatModelWrapper) logf(format string, v ...interface{}) {
	if w.logger != nil {
		w.logger.Printf(format, v...)
	}
}

// isRetryableError 判断错误是否可重试
func (w *RetryChatModelWrapper) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// 首先检查是否是 AgentError 类型
	if aierrors.IsRetryable(err) {
		return true
	}

	// 检查是否是 AgentError 的特定错误码
	if aierrors.IsCode(err, aierrors.CodeLLMRateLimit) ||
		aierrors.IsCode(err, aierrors.CodeLLMTimeout) ||
		aierrors.IsCode(err, aierrors.CodeServiceUnavailable) {
		return true
	}

	errStr := err.Error()

	// 429 速率限制错误
	if strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "Too Many Requests") {
		return true
	}

	// 5xx 服务器错误
	if strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") {
		return true
	}

	// 网络错误
	if isNetworkError(err) {
		return true
	}

	// 超时错误
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") {
		return true
	}

	return false
}

// getHTTPStatusCode 尝试从错误中提取 HTTP 状态码
func getHTTPStatusCode(err error) int {
	type httpError interface {
		HTTPStatusCode() int
	}
	if he, ok := err.(httpError); ok {
		return he.HTTPStatusCode()
	}
	return 0
}

// containsHTTPStatus 检查错误字符串中是否包含指定的 HTTP 状态码。
// 要求状态码前后不是数字字符，避免 UUID 等字符串中的误判。
func containsHTTPStatus(errStr string, code string) bool {
	for {
		idx := strings.Index(errStr, code)
		if idx < 0 {
			return false
		}
		// 检查前一个字符不是数字
		if idx > 0 && errStr[idx-1] >= '0' && errStr[idx-1] <= '9' {
			errStr = errStr[idx+len(code):]
			continue
		}
		// 检查后一个字符不是数字
		afterIdx := idx + len(code)
		if afterIdx < len(errStr) && errStr[afterIdx] >= '0' && errStr[afterIdx] <= '9' {
			errStr = errStr[afterIdx:]
			continue
		}
		return true
	}
}

// isNetworkError 判断是否为网络错误
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") {
		return true
	}
	return false
}

// calculateDelay 计算重试延迟（指数退避 + 随机抖动）
// 第1次重试: ~1s, 第2次: ~2s, 第3次: ~4s, 第4次: ~8s ...
func (w *RetryChatModelWrapper) calculateDelay(attempt int) time.Duration {
	// 初始延迟 1s，退避因子 2
	const initialDelay = 1000 // ms
	const backoffFactor = 2
	const maxDelay = 30000 // ms

	delay := float64(initialDelay)
	for i := 1; i < attempt; i++ {
		delay *= float64(backoffFactor)
	}
	// 添加随机抖动（0.5x - 1.5x）
	jitter := 0.5 + rand.Float64()
	delay = delay * jitter

	// 不超过最大延迟 30s
	if delay > float64(maxDelay) {
		delay = float64(maxDelay)
	}

	return time.Duration(delay) * time.Millisecond
}

// Generate 带重试的生成方法
func (w *RetryChatModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var lastErr error
	maxAttempts := w.maxRetries + 1 // +1 是因为第一次不算重试

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := w.ToolCallingChatModel.Generate(ctx, input, opts...)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// 如果不是可重试错误，直接返回
		if !w.isRetryableError(err) {
			return nil, err
		}

		// 如果还有重试机会，等待后重试
		if attempt < maxAttempts {
			delay := w.calculateDelay(attempt)
			w.logf("[RetryChatModel] Generate attempt %d failed: %v, retrying in %v...", attempt, err, delay)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				continue
			}
		}
	}

	return nil, fmt.Errorf("Generate failed after %d attempts: %w", maxAttempts, lastErr)
}

// Stream 带重试的流式生成方法
func (w *RetryChatModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	var lastErr error
	maxAttempts := w.maxRetries + 1

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stream, err := w.ToolCallingChatModel.Stream(ctx, input, opts...)
		if err == nil {
			return stream, nil
		}

		lastErr = err

		// 如果不是可重试错误，直接返回
		if !w.isRetryableError(err) {
			return nil, err
		}

		// 如果还有重试机会，等待后重试
		if attempt < maxAttempts {
			delay := w.calculateDelay(attempt)
			w.logf("[RetryChatModel] Stream attempt %d failed: %v, retrying in %v...", attempt, err, delay)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				continue
			}
		}
	}

	return nil, fmt.Errorf("Stream failed after %d attempts: %w", maxAttempts, lastErr)
}

// WithTools 设置工具
func (w *RetryChatModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newModel, err := w.ToolCallingChatModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	// 返回包装后的模型，保持重试能力
	return NewRetryChatModelWrapper(newModel, w.maxRetries, w.logger), nil
}

// Ensure RetryChatModelWrapper implements model.ToolCallingChatModel
var _ model.ToolCallingChatModel = (*RetryChatModelWrapper)(nil)
