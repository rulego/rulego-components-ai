package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rulego/rulego/api/types"
)

// DefaultStreamTellQueueCap 默认队列容量（消息数）。
// 取 1024：流式场景前端只转发 SSE，消费速度通常 ≥ LLM 生成速度，队列常年接近空；
// 堆到上限基本意味着前端失活（断网/关页）或 full 模式重放突发未被及时消费。
// 1024 条 RuleMsg（含 data+metadata）内存可控（MB 级）：既能吸收正常瞬时慢消费和
// full 模式的重放突发，又给"前端失活"一个明确判定点。
const DefaultStreamTellQueueCap = 1024

// DefaultStreamTellBlockTimeout full 模式下缓冲满后阻塞反压的时长上限。
// 满则阻塞等前端消费（重放数据已在内存，不丢）；持续 blockTimeout 仍消费不动才认定前端真断开、abort 止损。
const DefaultStreamTellBlockTimeout = 30 * time.Second

// StreamTellQueue 把流式的 TellNext 串行化：chunk 内容和工具事件都进同一个 FIFO channel，
// 由单个 goroutine 按入队顺序发给下游。
//
// 流式响应有两条 TellNext(Stream) 路径——onChunk（chunk 内容）和 SSEHandler.sendEvent（工具事件）。
// 都走队列，生产端才不会被前端慢消费拖住；统一到一个队列，是为了避免两条路径并发抢顺序。
//
// 缓冲满时的策略按流式模式区分（blockTimeout 控制）：
//   - off 模式（blockTimeout<=0）：立即 abort 上游流止损。off 是平滑实时流，正常消费堆不到上限，
//     满了即认定前端失活；且不能反压上游 Recv，否则会重新引入 "Error in input stream"。
//   - full 模式（blockTimeout>0）：阻塞反压 blockTimeout，超时才 abort。full 重放是突发流量（整条流
//     缓冲后瞬间涌出），即使前端正常也可能短暂堆满；此时应等消费而非误判失活——重放数据已在内存，
//     阻塞反压不丢数据、也不会反压到 LLM（缓冲阶段已结束）。只有持续不消费（真断开）才超时止损。
type StreamTellQueue struct {
	ctx          types.RuleContext
	ch           chan *types.RuleMsg
	done         chan struct{}
	once         sync.Once
	abort        context.CancelFunc // 缓冲满（前端失活）时取消上游流止损；可为 nil
	abortOnce    sync.Once          // 保证 abort（含日志）只触发一次
	aborted      atomic.Bool        // 是否因缓冲满触发过 abort（executeStream 据此发 Failure 区分截断）
	blockTimeout time.Duration      // 满则阻塞反压时长；<=0 立即 abort（off），>0 阻塞超时才 abort（full）
	logger       types.Logger
}

// NewStreamTellQueue 创建 off 模式队列：满则立即 abort，不反压上游 LLM。cap<=0 取默认值。
func NewStreamTellQueue(ctx types.RuleContext, cap int, abort context.CancelFunc, logger ...types.Logger) *StreamTellQueue {
	return newStreamTellQueue(ctx, cap, abort, 0, logger...)
}

// NewStreamTellQueueWithBlock 创建 full 模式队列：满则阻塞反压 blockTimeout，超时才 abort。
// 用于 full 模式重放的突发流量——阻塞等前端消费、不丢数据，持续 blockTimeout 不消费才认定失活止损。
func NewStreamTellQueueWithBlock(ctx types.RuleContext, cap int, abort context.CancelFunc, blockTimeout time.Duration, logger ...types.Logger) *StreamTellQueue {
	return newStreamTellQueue(ctx, cap, abort, blockTimeout, logger...)
}

func newStreamTellQueue(ctx types.RuleContext, cap int, abort context.CancelFunc, blockTimeout time.Duration, logger ...types.Logger) *StreamTellQueue {
	if cap <= 0 {
		cap = DefaultStreamTellQueueCap
	}
	var log types.Logger
	if len(logger) > 0 {
		log = logger[0]
	}
	q := &StreamTellQueue{
		ctx:          ctx,
		ch:           make(chan *types.RuleMsg, cap),
		done:         make(chan struct{}),
		abort:        abort,
		blockTimeout: blockTimeout,
		logger:       log,
	}
	go q.consume()
	return q
}

func (q *StreamTellQueue) consume() {
	defer close(q.done)
	for msg := range q.ch {
		q.ctx.TellNext(*msg, types.Stream)
	}
}

// Enqueue 入队一条消息。缓冲满时的处理由 blockTimeout 决定（见类型注释）。
func (q *StreamTellQueue) Enqueue(msg types.RuleMsg) {
	// off 模式（blockTimeout<=0）：满则立即 abort 止损，不反压上游 Recv（避免 "Error in input stream"）。
	if q.blockTimeout <= 0 {
		select {
		case q.ch <- &msg:
		default:
			q.triggerAbort()
		}
		return
	}
	// full 模式：满则阻塞反压（等前端消费，重放数据在内存不丢），持续 blockTimeout 不消费才 abort 止损。
	timer := time.NewTimer(q.blockTimeout)
	defer timer.Stop()
	select {
	case q.ch <- &msg:
	case <-timer.C:
		q.triggerAbort()
	}
}

// triggerAbort 触发一次 abort（取消上游流 + 标记 aborted）。幂等，只生效一次。
func (q *StreamTellQueue) triggerAbort() {
	q.abortOnce.Do(func() {
		q.aborted.Store(true)
		if q.logger != nil {
			q.logger.Printf("[StreamTellQueue] buffer full (frontend slow/disconnected), aborting upstream stream")
		}
		if q.abort != nil {
			q.abort()
		}
	})
}

// Aborted 是否因缓冲满（前端失活）触发过 abort。
// executeStream 据此区分"流被中途截断"与"正常完成"——ExecuteStream 会吞掉 cancel 错误（返回 nil err），
// 若不检查会把截断内容当成功发给前端。
func (q *StreamTellQueue) Aborted() bool {
	return q.aborted.Load()
}

// closeOnce 幂等关闭入队口。panic 路径的 defer Close 与正常路径的 Wait 都会调用，故需幂等。
func (q *StreamTellQueue) closeOnce() {
	q.once.Do(func() { close(q.ch) })
}

// Wait 关闭入队并等已入队消息全部 TellNext 完成。幂等。流正常结束后调用。
func (q *StreamTellQueue) Wait() {
	q.closeOnce()
	<-q.done
}

// Close 关闭入队口（防消费 goroutine 泄漏），不等已入队消息消费完。幂等。
// 用于 defer 兜底 panic/异常路径；正常路径用 Wait。
func (q *StreamTellQueue) Close() {
	q.closeOnce()
}
