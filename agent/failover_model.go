package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego/api/types"
)

// ===== 主端点熔断器 =====

// circuitState 熔断状态机：closed（正常）→ open（熔断）→ half-open（半开试探）。
type circuitState int32

const (
	circuitClosed   circuitState = iota // 正常，允许试主端点
	circuitOpen                         // 熔断，跳过主直接用备用
	circuitHalfOpen                     // 半开，试探主一次
)

// circuitBreaker 保护主端点的熔断器。
//
// 行为：
//   - closed：主端点连续失败达 failureThreshold 次 → 转 open（openUntil = now + cooldown）。
//     主成功 → 清零计数，保持 closed。
//   - open：allowPrimary 返回 false（跳过主直接用备用）。冷却到期（now > openUntil）→ 转 half-open。
//   - half-open：allowPrimary 只放行"一个"请求试探主（halfOpenProbing 占用），其余返回 false 走备用，
//     避免冷却到期瞬间并发请求全打主。试探成功 → closed；失败 → 重新 open（重置冷却）。
//     recordSuccess/recordFailure 释放试探权。
//
// 线程安全。
type circuitBreaker struct {
	mu               sync.Mutex
	state            circuitState
	failureCount     int
	failureThreshold int
	cooldown         time.Duration
	openUntil        time.Time
	halfOpenProbing  bool // half-open 时：true=已有请求在试探主，其余请求走备用
}

// newCircuitBreaker 创建熔断器。threshold/cooldown <=0 时用默认值（3 次 / 30s）。
func newCircuitBreaker(threshold int, cooldown time.Duration) *circuitBreaker {
	if threshold <= 0 {
		threshold = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &circuitBreaker{failureThreshold: threshold, cooldown: cooldown}
}

// allowPrimary 是否允许尝试主端点。open 到期会自动转 half-open。
// half-open 下只放行一个请求试探主（halfOpenProbing），其余返回 false 走备用，
// 避免冷却到期瞬间并发请求全打主。试探结果由 recordSuccess/recordFailure 释放试探权。
func (c *circuitBreaker) allowPrimary() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case circuitClosed:
		return true
	case circuitOpen:
		if !time.Now().After(c.openUntil) {
			return false // 熔断中，跳过主
		}
		c.state = circuitHalfOpen // 冷却到期，转半开
		fallthrough
	case circuitHalfOpen:
		// 只放行一个请求试探主，其余走备用
		if !c.halfOpenProbing {
			c.halfOpenProbing = true
			return true
		}
		return false
	}
	return true
}

// recordSuccess 主端点成功 → 恢复 closed（清零计数），并释放 half-open 试探权。
func (c *circuitBreaker) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = circuitClosed
	c.failureCount = 0
	c.halfOpenProbing = false
}

// recordFailure 主端点失败。half-open 失败立即重新熔断（并释放试探权）；closed 累计达阈值熔断。
func (c *circuitBreaker) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failureCount++
	if c.state == circuitHalfOpen {
		c.state = circuitOpen
		c.openUntil = time.Now().Add(c.cooldown)
		c.halfOpenProbing = false // 释放试探权，重新熔断
		return
	}
	if c.failureCount >= c.failureThreshold {
		c.state = circuitOpen
		c.openUntil = time.Now().Add(c.cooldown)
	}
}

// getState 返回当前状态（日志/测试用）。
func (c *circuitBreaker) getState() circuitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// ===== FailoverChatModelWrapper =====

// FailoverChatModelWrapper 故障转移包装器：主端点失败（可重试错误）后依次切换备用端点。
//
// primary 与 failovers 通常各自已是 *RetryChatModelWrapper（先在同模型内重试，耗尽后才触发 failover），
// 从而组合出 "retry（同模型）→ failover（切模型）" 的完整链路。
//
// 可选熔断器（WithCircuit 启用）：主连续失败达阈值后熔断，冷却期内跳过主直接用备用，
// 避免主长时间故障时每个请求都等主 retry 耗尽。
//
// 行为与流式模式相关：
//   - Generate：任一端点返回可重试错误 → 切下一个；不可重试错误直接返回。
//   - Stream（off 模式）：端点 Stream 返回错误（建立失败 / 探测窗口内重试耗尽）→ 切下一个；
//     一旦某端点 Stream 成功返回 reader，后续 mid-stream 错误由该 reader 透传，不再 failover。
//   - Stream（full 模式）：端点 Stream 内部完整缓冲重试，mid-stream 断流耗尽才返回错误 → 切下一个。
type FailoverChatModelWrapper struct {
	primary   model.ToolCallingChatModel
	failovers []model.ToolCallingChatModel
	circuit   *circuitBreaker // 主端点熔断器，nil 表示不启用（每次都试主）
	logger    types.Logger
}

// NewFailoverChatModelWrapper 创建故障转移包装器。failovers 为空时等价于直接用 primary。
func NewFailoverChatModelWrapper(primary model.ToolCallingChatModel, failovers []model.ToolCallingChatModel, logger ...types.Logger) *FailoverChatModelWrapper {
	var log types.Logger
	if len(logger) > 0 && logger[0] != nil {
		log = logger[0]
	}
	return &FailoverChatModelWrapper{primary: primary, failovers: failovers, logger: log}
}

// WithCircuit 启用主端点熔断器（builder 模式）。threshold 连续失败次数，cooldown 冷却时长；<=0 用默认。
func (w *FailoverChatModelWrapper) WithCircuit(threshold int, cooldown time.Duration) *FailoverChatModelWrapper {
	w.circuit = newCircuitBreaker(threshold, cooldown)
	return w
}

func (w *FailoverChatModelWrapper) logf(format string, v ...interface{}) {
	if w.logger != nil {
		w.logger.Printf(format, v...)
	}
}

// models 按优先级返回主 + 备用端点模型。
func (w *FailoverChatModelWrapper) models() []model.ToolCallingChatModel {
	all := make([]model.ToolCallingChatModel, 0, 1+len(w.failovers))
	all = append(all, w.primary)
	all = append(all, w.failovers...)
	return all
}

// startIdx 计算本次应从第几个端点开始尝试（熔断开启且主 open 时跳过主）。
// 返回 (startIdx, triedPrimary)。triedPrimary 表示是否实际尝试了主（用于熔断计数）。
func (w *FailoverChatModelWrapper) startIdx() (int, bool) {
	if w.circuit == nil {
		return 0, true
	}
	if !w.circuit.allowPrimary() {
		w.logf("[FailoverChatModel] primary circuit open, skipping to failover endpoints")
		return 1, false
	}
	return 0, true
}

// Generate 同步生成：按优先级尝试各端点，可重试错误触发 failover；主端点成功/失败反馈给熔断器。
func (w *FailoverChatModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	models := w.models()
	idx, tryPrimary := w.startIdx()
	if idx > 0 && len(models) <= 1 {
		return nil, fmt.Errorf("primary circuit open and no failover endpoints available")
	}

	var lastErr error
	for i := idx; i < len(models); i++ {
		msg, err := models[i].Generate(ctx, input, opts...)
		if err == nil {
			if i == 0 && tryPrimary && w.circuit != nil {
				w.circuit.recordSuccess()
			}
			if i > 0 {
				w.logf("[FailoverChatModel] Generate failed over to endpoint #%d after error: %v", i, lastErr)
			}
			return msg, nil
		}
		if i == 0 && tryPrimary && w.circuit != nil {
			w.circuit.recordFailure()
		}
		if !IsRetryableError(err) {
			return nil, err // 不可重试错误直接返回，不切换
		}
		lastErr = err
		w.logf("[FailoverChatModel] Generate endpoint #%d failed: %v, trying next...", i, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available model")
	}
	return nil, fmt.Errorf("Generate failed over all endpoints: %w", lastErr)
}

// Stream 流式生成：按优先级尝试各端点。端点 Stream 返回错误（含 retry 耗尽）才切换；
// 返回 reader 即视为该端点已承担本次流，后续 mid-stream 错误透传。
func (w *FailoverChatModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	models := w.models()
	idx, tryPrimary := w.startIdx()
	if idx > 0 && len(models) <= 1 {
		return nil, fmt.Errorf("primary circuit open and no failover endpoints available")
	}

	var lastErr error
	for i := idx; i < len(models); i++ {
		stream, err := models[i].Stream(ctx, input, opts...)
		if err == nil {
			if i == 0 && tryPrimary && w.circuit != nil {
				w.circuit.recordSuccess()
			}
			if i > 0 {
				w.logf("[FailoverChatModel] Stream failed over to endpoint #%d after error: %v", i, lastErr)
			}
			return stream, nil
		}
		if i == 0 && tryPrimary && w.circuit != nil {
			w.circuit.recordFailure()
		}
		if !IsRetryableError(err) {
			return nil, err
		}
		lastErr = err
		w.logf("[FailoverChatModel] Stream endpoint #%d failed: %v, trying next...", i, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available model")
	}
	return nil, fmt.Errorf("Stream failed over all endpoints: %w", lastErr)
}

// WithTools 为所有端点绑定工具，返回保持 failover 结构的新包装器。
// 熔断器指针共享，跨 WithTools 持续累计失败——避免每次绑工具都重置导致熔断形同虚设。
func (w *FailoverChatModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newPrimary, err := w.primary.WithTools(tools)
	if err != nil {
		return nil, err
	}
	newFailovers := make([]model.ToolCallingChatModel, len(w.failovers))
	for i, f := range w.failovers {
		nf, err := f.WithTools(tools)
		if err != nil {
			return nil, err
		}
		newFailovers[i] = nf
	}
	fo := NewFailoverChatModelWrapper(newPrimary, newFailovers, w.logger)
	fo.circuit = w.circuit // 共享熔断器状态
	return fo, nil
}

// Ensure FailoverChatModelWrapper implements model.ToolCallingChatModel
var _ model.ToolCallingChatModel = (*FailoverChatModelWrapper)(nil)
