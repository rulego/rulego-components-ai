package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rulego/rulego/api/types"
	rulegotest "github.com/rulego/rulego/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingCtx 创建记录 TellNext 数据顺序的测试 RuleContext。slow>0 时每次 TellNext sleep，模拟慢前端。
func recordingCtx(record *[]string, mu *sync.Mutex, slow time.Duration) types.RuleContext {
	return rulegotest.NewRuleContext(types.NewConfig(), func(msg types.RuleMsg, relationType string, err error) {
		if slow > 0 {
			time.Sleep(slow)
		}
		mu.Lock()
		*record = append(*record, msg.GetData())
		mu.Unlock()
	})
}

func sseMsg(data string) types.RuleMsg {
	return types.NewMsg(0, "TEST", types.TEXT, types.NewMetadata(), data)
}

// TestStreamTellQueue_FIFOOrder chunk 与工具事件入同一队列，TellNext 顺序与入队一致。
// 这是修复"chunk 与工具事件乱序"的核心：两条路径（onChunk / SSEHandler.sendEvent）都入队，
// 单 goroutine 按 FIFO 消费，顺序由入队顺序决定。
func TestStreamTellQueue_FIFOOrder(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, 0)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewStreamTellQueue(ctx, 64, cancel)
	// 模拟真实时序：chunk1 → 工具事件 → chunk2
	q.Enqueue(sseMsg("chunk1"))
	q.Enqueue(sseMsg("tool_start"))
	q.Enqueue(sseMsg("tool_result"))
	q.Enqueue(sseMsg("chunk2"))
	q.Wait()

	assert.Equal(t, []string{"chunk1", "tool_start", "tool_result", "chunk2"}, got)
}

// TestStreamTellQueue_AsyncDecouples 慢 TellNext（慢前端）不阻塞入队：入队连续快速，慢消费在后台。
func TestStreamTellQueue_AsyncDecouples(t *testing.T) {
	const (
		n    = 5
		slow = 200 * time.Millisecond
	)
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, slow)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewStreamTellQueue(ctx, n, cancel)
	start := time.Now()
	for i := 0; i < n; i++ {
		q.Enqueue(sseMsg("c"))
	}
	enqueueSpan := time.Since(start)
	q.Wait()

	require.Len(t, got, n)
	backpressure := time.Duration(n) * slow
	if enqueueSpan > backpressure/2 {
		t.Errorf("入队被慢 TellNext 阻塞：enqueueSpan=%v，预期 << %v", enqueueSpan, backpressure)
	}
	t.Logf("入队 %d 条耗时 %v（慢 TellNext sleep=%v，背压预期≈%v）", n, enqueueSpan, slow, backpressure)
}

// TestStreamTellQueue_WaitBlocks Wait 必须等所有 TellNext 完成才返回（保证不丢）。
func TestStreamTellQueue_WaitBlocks(t *testing.T) {
	const (
		n    = 3
		slow = 100 * time.Millisecond
	)
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, slow)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewStreamTellQueue(ctx, n, cancel)
	for i := 0; i < n; i++ {
		q.Enqueue(sseMsg("c"))
	}
	start := time.Now()
	q.Wait()
	elapsed := time.Since(start)

	require.Len(t, got, n)
	// Wait 至少等到 n 条 TellNext 完成（n*slow），减容差
	minExpected := time.Duration(n)*slow - 50*time.Millisecond
	if elapsed < minExpected {
		t.Errorf("Wait 未等消费完：elapsed=%v，预期 ≥ %v", elapsed, minExpected)
	}
}

// TestStreamTellQueue_CloseIdempotent Close 幂等（多次不 panic），与 Wait 混用安全。
// 锁住 panic 兜底修复：defer Close + 正常 Wait 都会关闭，once 保证不重复 close panic。
func TestStreamTellQueue_CloseIdempotent(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, 0)
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewStreamTellQueue(ctx, 4, cancel)
	q.Enqueue(sseMsg("a"))
	q.Close() // 关闭入队口
	q.Close() // 幂等，不 panic
	q.Wait()  // 混用 Wait，也不 panic（once 保证）
	q.Close() // Wait 后再 Close，仍不 panic

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("期望 [a]，got %v", got)
	}
	// 未触发 abort
	select {
	case <-sctx.Done():
		t.Error("正常流不应触发 abort")
	default:
	}
}

// TestStreamTellQueue_FastConsumerNoAbort 快消费（消费跟得上）→ 队列不满 → 不触发 abort（不误杀）。
func TestStreamTellQueue_FastConsumerNoAbort(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, 0) // 快消费
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewStreamTellQueue(ctx, 32, cancel)
	for i := 0; i < 16; i++ { // 入队 16 < 容量 32，且消费快，缓冲不满
		q.Enqueue(sseMsg("m"))
	}
	q.Wait()

	require.Len(t, got, 16)
	select {
	case <-sctx.Done():
		t.Fatal("快消费不应触发 abort（误杀正常流）")
	default:
	}
}

// TestStreamTellQueue_SlowConsumerAbortsUpstream 慢消费 → 缓冲满 → 触发 abort（取消上游流止损）。
// 这是"有界 + 满则断流"的核心：前端失活时不阻塞 Recv、不无限吃内存，而是主动放弃上游流。
func TestStreamTellQueue_SlowConsumerAbortsUpstream(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, 100*time.Millisecond) // 慢 TellNext
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewStreamTellQueue(ctx, 4, cancel) // 小容量，便于快速填满
	// 快速入队超过容量；消费慢 → 缓冲满 → abort
	for i := 0; i < 20; i++ {
		q.Enqueue(sseMsg("m"))
	}

	select {
	case <-sctx.Done():
		// abort 触发 ✅
	case <-time.After(2 * time.Second):
		t.Fatal("慢消费缓冲满应触发 abort（cancel 上游流）")
	}
	if !q.Aborted() {
		t.Error("缓冲满 abort 后 Aborted() 应为 true")
	}
	q.Wait()
}

// TestStreamTellQueue_AbortedFlag 正常流 Aborted()=false；缓冲满 abort 后 Aborted()=true。
// executeStream 据此区分"被截断"与"正常完成"。
func TestStreamTellQueue_AbortedFlag(t *testing.T) {
	var mu sync.Mutex
	// 正常流：不入满，不 abort
	ctx1 := recordingCtx(&[]string{}, &mu, 0)
	_, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	q1 := NewStreamTellQueue(ctx1, 32, cancel1)
	for i := 0; i < 8; i++ { // 8 < 容量 32，快消费
		q1.Enqueue(sseMsg("m"))
	}
	q1.Wait()
	if q1.Aborted() {
		t.Error("正常流不应标记 aborted")
	}

	// 慢消费 → 缓冲满 → abort → Aborted()=true
	ctx2 := recordingCtx(&[]string{}, &mu, 100*time.Millisecond)
	sctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	q2 := NewStreamTellQueue(ctx2, 4, cancel2)
	for i := 0; i < 20; i++ {
		q2.Enqueue(sseMsg("m"))
	}
	<-sctx2.Done() // 等 abort 触发
	if !q2.Aborted() {
		t.Error("缓冲满 abort 后 Aborted() 应为 true")
	}
	q2.Wait()
}
