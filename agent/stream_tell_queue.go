package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rulego/rulego/api/types"
)

// DefaultStreamTellQueueCap Default queue capacity (number of messages).
// Take 1024: The streaming scene frontend only forwards SSE, with consumption speeds usually ≥ LLM generation speed, and queues often nearly empty;
// Stacking up to the ceiling basically means frontend inactivity (network disconnection/page closure) or full mode replay bursts that are not consumed in time.
// 1024 RuleMsg (including data+metadata) Memory Controllable (MB level): Can absorb normal instantaneous slow consumption and
// The sudden replay of full mode provides a clear judgment point for "frontend inactivation."
const DefaultStreamTellQueueCap = 1024

// The maximum duration of block backflow after buffering is full in DefaultStreamTellBlockTimeout full mode.
// If full, it blocks and other front-end consumption (data is already in memory and is not lost); Only if blockTimeout continues to consume and no action is considered a true frontend disconnect and abort stop-loss.
const DefaultStreamTellBlockTimeout = 30 * time.Second

// StreamTellQueue serializes the streaming TellNext: chunk content and tool events are all stored in the same FIFO channel,
// Distributed downstream by a single goroutine in the order of joining.
//
// Stream responses have two TellNext (Stream) paths—onChunk (chunk content) and SSEHandler.sendEvent (tool event).
// Everyone queues up, so the production side won't be held back by slow front-end consumption; Unified into one queue is to prevent two paths from rushing to order concurrently.
//
// Buffer full policies are distinguished by stream mode (blockTimeout control):
//   - off mode (blockTimeout<=0): Immediate stop-loss for upstream abort flow. Off means smooth real-time streaming, normal consumption doesn't reach the limit,
//     If full, the front end is deemed inactive; Also, you cannot reverse the upstream Recv, otherwise the "Error in input stream" will be re-introduced.
//   - Full mode (blockTimeout>0): blocks backward blockTimeout, only aborts after timeout. Full replay is burst traffic (entire stream).
//     After buffering, it will surge out instantly), even if the front end is normal, it may briefly fill up; At this point, you should wait for consumption, not misjudge inactivation—the playback data is already in memory,
//     Blocking backpressure does not lose data or backpressure to the LLM (buffering phase has ended). Only if you do not spend continuously (truly disconnect) will you stop loss over time.
type StreamTellQueue struct {
	ctx          types.RuleContext
	ch           chan *types.RuleMsg
	done         chan struct{}
	once         sync.Once
	abort        context.CancelFunc // When the buffer is full (front end inactive), the upstream flow stop loss is canceled; Can be nil
	abortOnce    sync.Once          // Ensure ABORT (including logs) is triggered only once
	aborted      atomic.Bool        // Has abort ever been triggered due to full buffer? (executeStream issues a failure to distinguish truncation)
	blockTimeout time.Duration      // When full, the block is blocked and the backcompression duration is prolonged; <=0 abort(off) immediately; >0 blocks timeout before abort(full)
	logger       types.Logger
}

// NewStreamTellQueue creates an off-mode queue: if full, immediately abort without overloading upstream LLMs. cap<=0 takes the default value.
func NewStreamTellQueue(ctx types.RuleContext, cap int, abort context.CancelFunc, logger ...types.Logger) *StreamTellQueue {
	return newStreamTellQueue(ctx, cap, abort, 0, logger...)
}

// NewStreamTellQueueWithBlock creates a full mode queue: when full, block and back-push blockTimeout, only abort after timeout.
// For burst traffic in full mode replay—blocking and other front-end consumption, no data loss, and continuous blockTimeout without consumption—only then is the inactivation stop loss recognized.
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

// Enqueue: A message to join the team. When the buffer is full, the handling is determined by blockTimeout (see type notes).
func (q *StreamTellQueue) Enqueue(msg types.RuleMsg) {
	// off mode (blockTimeout<=0): When full, immediately abort stops loss, without reselling upstream Recv (avoid "Error in input stream").
	if q.blockTimeout <= 0 {
		select {
		case q.ch <- &msg:
		default:
			q.triggerAbort()
		}
		return
	}
	// Full mode: When full, block the backlash (wait for frontend consumption, replay data is stored in memory without loss), and only after continuous blockTimeout without consumption is the abort stop loss.
	timer := time.NewTimer(q.blockTimeout)
	defer timer.Stop()
	select {
	case q.ch <- &msg:
	case <-timer.C:
		q.triggerAbort()
	}
}

// triggerAbort Triggers abort once (cancel upstream stream + mark aborted). Idempotent, only effective once.
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

// Has Aborted ever triggered abort due to full buffer (front-end inactivation)?
// executeStream distinguishes between "stream interrupted midway" and "normal completion"—ExecuteStream swallows cancel errors (returns nil err),
// If not checked, the truncated content will be considered successful and sent to the frontend.
func (q *StreamTellQueue) Aborted() bool {
	return q.aborted.Load()
}

// closeOnce idempotent: close the entry gate. Both the defer close of panic paths and the wait of normal paths are called, so idempotencies are required.
func (q *StreamTellQueue) closeOnce() {
	q.once.Do(func() { close(q.ch) })
}

// Wait: Close the enqueuement and wait for all join-in notifications to be completed by TellNext. Power equal. Called after the stream ends normally.
func (q *StreamTellQueue) Wait() {
	q.closeOnce()
	<-q.done
}

// Close Closing the entry gate (to prevent goroutine leaks), do not wait for the queue message to be consumed. Power equal.
// Used to cover panic/abnormal paths in defers; Normal paths use Wait.
func (q *StreamTellQueue) Close() {
	q.closeOnce()
}
