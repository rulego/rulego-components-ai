package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	// 流式响应中断错误（如 "Error in input stream"、unexpected EOF）
	if strings.Contains(errStr, "input stream") ||
		strings.Contains(errStr, "unexpected EOF") {
		return true
	}

	// 超时错误
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") {
		return true
	}

	return false
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

// Stream 带重试的流式生成。仅在建立阶段（首 chunk 之前）重试，以保留流式实时性；
// 首 chunk 后的中途断流不再重试（避免重复内容），直接透传。
// Copy(2) 共享缓存：probe 只读首 chunk 探测，passCopy 从首节点实时读取整条流。
func (w *RetryChatModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	maxAttempts := w.maxRetries + 1
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stream, err := w.ToolCallingChatModel.Stream(ctx, input, opts...)
		if err != nil {
			// 建立流失败：不可重试直接透传，可重试则记录后等待重试
			if !w.isRetryableError(err) {
				return nil, err
			}
			lastErr = err
			if attempt >= maxAttempts {
				break
			}
			w.logf("[RetryChatModel] Stream open attempt %d failed: %v, retrying...", attempt, err)
			if !w.sleep(ctx, w.calculateDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}

		// 探测首 chunk，确认上游已开始输出
		copies := stream.Copy(2)
		probeCopy, passCopy := copies[0], copies[1]
		_, probeErr := probeCopy.Recv()
		probeCopy.Close()

		// 首 chunk 到达（含空流 EOF），交给调用方
		if probeErr == nil || errors.Is(probeErr, io.EOF) {
			return passCopy, nil
		}

		// 首 chunk 前出错，丢弃 passCopy 重试
		passCopy.Close()
		if !w.isRetryableError(probeErr) {
			return nil, probeErr
		}
		lastErr = probeErr
		if attempt >= maxAttempts {
			break
		}
		w.logf("[RetryChatModel] Stream probe attempt %d failed: %v, retrying...", attempt, probeErr)
		if !w.sleep(ctx, w.calculateDelay(attempt)) {
			return nil, ctx.Err()
		}
	}

	if lastErr == nil {
		lastErr = errors.New("stream failed")
	}
	return nil, fmt.Errorf("Stream failed after %d attempts: %w", maxAttempts, lastErr)
}

// sleep 等待指定时长，期间响应 ctx 取消。返回 false 表示 ctx 已取消。
func (w *RetryChatModelWrapper) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
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
