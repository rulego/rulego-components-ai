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

// DefaultStreamProbeChunks 流式探测窗口默认大小（chunk 数）。
// Stream 建立后会预读这么多 chunk 以捕获早期断流（如 "Error in input stream"）：
// 窗口内断流会重试；越过窗口后中途断流则直接透传，不再重试。
// 值越大对早期断流的覆盖率越高，但首字延迟也越大。
const DefaultStreamProbeChunks = 3

// RetryChatModelWrapper 包装 ChatModel 以提供重试功能
type RetryChatModelWrapper struct {
	model.ToolCallingChatModel
	maxRetries  int
	probeChunks int  // 流式探测窗口大小，<=0 时取默认值（off 模式用）
	streamFull  bool // 完整 mid-stream 重试：true 时完整缓冲+重试+重放（牺牲实时）
	logger      types.Logger
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
		probeChunks:          DefaultStreamProbeChunks,
		logger:               log,
	}
}

// SetProbeChunks 设置流式探测窗口大小（chunk 数）。
// n<=0 时恢复默认值；设为 1 时退化为"仅探测首 chunk"的旧行为。
// 用于在不改动构造参数的情况下调整早期断流的探测力度。
func (w *RetryChatModelWrapper) SetProbeChunks(n int) {
	if n > 0 {
		w.probeChunks = n
	} else {
		w.probeChunks = DefaultStreamProbeChunks
	}
}

// SetStreamFull 切换流式重试模式：true=完整 mid-stream 重试（缓冲重放，牺牲实时），
// false=仅探测窗口内重试（off，保留实时）。由 CreateChatModel 按 config.StreamRetryMode 设置。
func (w *RetryChatModelWrapper) SetStreamFull(full bool) {
	w.streamFull = full
}

// logWarnf 警告日志（出错/断流/重试耗尽，排查关键，总输出）
func (w *RetryChatModelWrapper) logWarnf(format string, v ...interface{}) {
	if w.logger != nil {
		w.logger.Warnf(format, v...)
	}
}

// logInfof 重试进行中的日志。用 Debug 级别：重试是异常路径，生产 info 不打扰，排查 debug 可见。
func (w *RetryChatModelWrapper) logInfof(format string, v ...interface{}) {
	if w.logger != nil {
		w.logger.Debugf(format, v...)
	}
}

// IsRetryableError 判断错误是否可重试（包级，供 retry/failover 的 ShouldRetry/ShouldFailover 共用）。
func IsRetryableError(err error) bool {
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

	// 429 速率限制错误（containsHTTPStatus 避免时间戳/UUID 等数字串里的 429 误判）
	if containsHTTPStatus(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "Too Many Requests") {
		return true
	}

	// 5xx 服务器错误（containsHTTPStatus 避免端口/计数等数字里的 500/502/503/504 误判）
	if containsHTTPStatus(errStr, "500") ||
		containsHTTPStatus(errStr, "502") ||
		containsHTTPStatus(errStr, "503") ||
		containsHTTPStatus(errStr, "504") {
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

// isRetryableError 保留方法（委托包级 IsRetryableError），向后兼容现有调用与测试。
func (w *RetryChatModelWrapper) isRetryableError(err error) bool {
	return IsRetryableError(err)
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
			w.logInfof("[RetryChatModel] Generate attempt %d failed: %v, retrying in %v...", attempt, err, delay)

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

// probeStream 预读流的前 n 个 chunk，用于捕获建立后的早期断流。
// 返回 nil：已成功接收 n 个 chunk（流健康），应透传。
// 返回 io.EOF：短流在 n 个 chunk 内正常结束，应透传。
// 返回其它错误：窗口内断流，由调用方按可重试性决定是否重试。
func probeStream(sr *schema.StreamReader[*schema.Message], n int) error {
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		if _, err := sr.Recv(); err != nil {
			return err
		}
	}
	return nil
}

// Stream 带重试的流式生成。在建立阶段及"探测窗口"（前 probeChunks 个 chunk）内重试；
// 一旦流越过探测窗口开始向调用方输出，中途断流不再重试（避免重复内容），直接透传。
// Copy(2) 共享缓存：probe 预读窗口内 chunk 探测，passCopy 从首节点完整重放整条流（广播缓冲，不丢数据）。
func (w *RetryChatModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	// full 模式：完整缓冲整条流→出错重试→成功重放（牺牲实时，换 mid-stream 完整可重试性）
	if w.streamFull {
		return w.streamFullRetry(ctx, input, opts...)
	}
	maxAttempts := w.maxRetries + 1
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stream, err := w.ToolCallingChatModel.Stream(ctx, input, opts...)
		if err != nil {
			w.logWarnf("[RetryChatModel] Stream attempt %d open error: %v", attempt, err)
			// 建立流失败：不可重试直接透传，可重试则记录后等待重试
			if !w.isRetryableError(err) {
				return nil, err
			}
			lastErr = err
			if attempt >= maxAttempts {
				break
			}
			w.logInfof("[RetryChatModel] Stream open attempt %d failed: %v, retrying...", attempt, err)
			if !w.sleep(ctx, w.calculateDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}

		// 探测前 probeChunks 个 chunk：在向前端输出前捕获早期断流（如 "Error in input stream"）。
		// 窗口内断流则重试；越过窗口后断流直接透传，避免重复内容。
		// probe 预读的内容由 passCopy 完整重放（StreamReader.Copy 为广播缓冲，不丢数据）。
		copies := stream.Copy(2)
		probeCopy, passCopy := copies[0], copies[1]
		probeErr := probeStream(probeCopy, w.probeChunks)
		probeCopy.Close()
		if probeErr != nil && !errors.Is(probeErr, io.EOF) {
			w.logWarnf("[RetryChatModel] Stream attempt %d probe error (probeChunks=%d): %v", attempt, w.probeChunks, probeErr)
		}

		// 探测窗口内未发现错误（已收到足够 chunk，或短流正常 EOF），交给调用方
		if probeErr == nil || errors.Is(probeErr, io.EOF) {
			return passCopy, nil
		}

		// 窗口内断流，丢弃 passCopy 重试
		passCopy.Close()
		if !w.isRetryableError(probeErr) {
			return nil, probeErr
		}
		lastErr = probeErr
		if attempt >= maxAttempts {
			break
		}
		w.logInfof("[RetryChatModel] Stream probe attempt %d failed (in window): %v, retrying...", attempt, probeErr)
		if !w.sleep(ctx, w.calculateDelay(attempt)) {
			return nil, ctx.Err()
		}
	}

	if lastErr == nil {
		lastErr = errors.New("stream failed")
	}
	return nil, fmt.Errorf("Stream failed after %d attempts: %w", maxAttempts, lastErr)
}

// streamFullRetry 完整 mid-stream 重试模式：完整缓冲每条流，出错则重试，成功后重放。
// 牺牲实时性（首字延迟≈完整生成时间）换取 mid-stream 断流的完整可重试性。
func (w *RetryChatModelWrapper) streamFullRetry(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	maxAttempts := w.maxRetries + 1
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stream, err := w.ToolCallingChatModel.Stream(ctx, input, opts...)
		if err != nil {
			w.logWarnf("[RetryChatModel] streamFull attempt %d/%d open error: %v", attempt, maxAttempts, err)
			if !IsRetryableError(err) {
				w.logWarnf("[RetryChatModel] streamFull attempt %d NOT retryable, return", attempt)
				return nil, err
			}
			lastErr = err
			if attempt >= maxAttempts {
				break
			}
			w.logInfof("[RetryChatModel] streamFull attempt %d retryable, retrying...", attempt)
			if !w.sleep(ctx, w.calculateDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}
		// 完整缓冲整条流，探测是否中途断流
		chunks, streamErr := drainAndBufferStream(stream)
		if streamErr == nil {
			return streamReaderFromMessages(chunks), nil
		}
		w.logWarnf("[RetryChatModel] streamFull attempt %d/%d mid-stream error (chunks=%d): %v", attempt, maxAttempts, len(chunks), streamErr)
		if !IsRetryableError(streamErr) {
			w.logWarnf("[RetryChatModel] streamFull attempt %d NOT retryable, return", attempt)
			return nil, streamErr
		}
		lastErr = streamErr
		if attempt >= maxAttempts {
			break
		}
		w.logInfof("[RetryChatModel] streamFull attempt %d retryable, retrying...", attempt)
		if !w.sleep(ctx, w.calculateDelay(attempt)) {
			return nil, ctx.Err()
		}
	}
	w.logWarnf("[RetryChatModel] streamFull EXHAUSTED after %d attempts, lastErr=%v", maxAttempts, lastErr)
	if lastErr == nil {
		lastErr = errors.New("stream failed")
	}
	return nil, fmt.Errorf("Stream failed after %d attempts: %w", maxAttempts, lastErr)
}

// drainAndBufferStream 消费整条流到 io.EOF（返回全部 chunk，nil）或错误（返回已收 chunk，err）。
func drainAndBufferStream(stream *schema.StreamReader[*schema.Message]) ([]*schema.Message, error) {
	defer stream.Close()
	var chunks []*schema.Message
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return chunks, nil
		}
		if err != nil {
			return chunks, err
		}
		if msg != nil {
			chunks = append(chunks, msg)
		}
	}
}

// streamReaderFromMessages 从已缓冲的 chunk 构造可重放的 StreamReader。
func streamReaderFromMessages(chunks []*schema.Message) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](0)
	go func() {
		defer w.Close()
		for _, c := range chunks {
			w.Send(c, nil)
		}
	}()
	return r
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
	// 返回包装后的模型，保持重试能力。必须复制 streamFull/probeChunks：
	// eino react 绑工具必经此路径，若丢失则 full 模式会静默退化为 off（窗口外断流不再重试）。
	rw := NewRetryChatModelWrapper(newModel, w.maxRetries, w.logger)
	rw.streamFull = w.streamFull
	rw.probeChunks = w.probeChunks
	return rw, nil
}

// Ensure RetryChatModelWrapper implements model.ToolCallingChatModel
var _ model.ToolCallingChatModel = (*RetryChatModelWrapper)(nil)
