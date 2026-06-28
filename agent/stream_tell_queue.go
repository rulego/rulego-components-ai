package agent

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rulego/rulego/api/types"
)

// DefaultStreamTellQueueCap 默认队列容量（消息数）。
// 取 256：流式场景前端只转发 SSE，消费速度通常 ≥ LLM 生成速度，队列常年接近空；
// 堆到上限基本意味着前端失活（断网/关页），此时应止损而非无限缓冲。
// 256 条 RuleMsg（含 data+metadata）内存可控（约 MB 级）：既能吸收正常瞬时慢消费，
// 又给"前端失活"一个明确判定点。
const DefaultStreamTellQueueCap = 256

// StreamTellQueue 把流式的 TellNext 串行化：chunk 内容和工具事件都进同一个 FIFO channel，
// 由单个 goroutine 按入队顺序发给下游。
//
// 流式响应有两条 TellNext(Stream) 路径——onChunk（chunk 内容）和 SSEHandler.sendEvent（工具事件）。
// 都走队列，生产端才不会被前端慢消费拖住；统一到一个队列，是为了避免两条路径并发抢顺序。
//
// 容量有界（DefaultStreamTellQueueCap）：正常瞬时慢消费由缓冲吸收，不反压 LLM 读取（避免
// "Error in input stream"）；缓冲满视为前端失活，调用 abort 取消上游流止损——既不阻塞 Recv
// 重新引入背压，也不无限吃内存。
type StreamTellQueue struct {
	ctx       types.RuleContext
	ch        chan *types.RuleMsg
	done      chan struct{}
	once      sync.Once
	abort     context.CancelFunc // 缓冲满时取消上游流止损；可为 nil
	abortOnce sync.Once          // 保证 abort（含日志）只触发一次
	aborted   atomic.Bool        // 是否因缓冲满触发过 abort（executeStream 据此发 Failure 区分截断）
	logger    types.Logger
}

// NewStreamTellQueue 创建队列并启动消费 goroutine。
// cap 为缓冲大小，<=0 取 DefaultStreamTellQueueCap。
// abort 在缓冲满（前端失活）时被调用一次，用于取消上游 LLM 流止损；可为 nil（满时仅丢弃新消息）。
func NewStreamTellQueue(ctx types.RuleContext, cap int, abort context.CancelFunc, logger ...types.Logger) *StreamTellQueue {
	if cap <= 0 {
		cap = DefaultStreamTellQueueCap
	}
	var log types.Logger
	if len(logger) > 0 {
		log = logger[0]
	}
	q := &StreamTellQueue{
		ctx:    ctx,
		ch:     make(chan *types.RuleMsg, cap),
		done:   make(chan struct{}),
		abort:  abort,
		logger: log,
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

// Enqueue 入队一条消息。缓冲满时视为前端失活：取消上游流止损（不阻塞 Recv，避免
// "Error in input stream"；不无限吃内存）。abort 幂等，只触发一次；触发当条消息丢弃。
func (q *StreamTellQueue) Enqueue(msg types.RuleMsg) {
	select {
	case q.ch <- &msg:
	default:
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
