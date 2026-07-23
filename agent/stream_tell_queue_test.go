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

// recordingCtx creates a test RuleContext that records the TellNext data order. At slow>0, each TellNext sleeps to simulate a slow frontend.
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

// TestStreamTellQueue_FIFOOrder chunk and tool events are queued in the same queue, and TellNext follows the queue order.
// This is the core issue of fixing "chunk and tool event disorder": both paths (onChunk / SSEHandler.sendEvent) are joined,
// Single goroutines are consumed according to FIFO, with the order determined by the order of joining the team.
func TestStreamTellQueue_FIFOOrder(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, 0)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewStreamTellQueue(ctx, 64, cancel)
	// Simulating real timing: chunk1 → tool event → chunk2
	q.Enqueue(sseMsg("chunk1"))
	q.Enqueue(sseMsg("tool_start"))
	q.Enqueue(sseMsg("tool_result"))
	q.Enqueue(sseMsg("chunk2"))
	q.Wait()

	assert.Equal(t, []string{"chunk1", "tool_start", "tool_result", "chunk2"}, got)
}

// TestStreamTellQueue_AsyncDecouples Slow TellNext (slow frontend) does not block onboarding: onboarding is continuous and fast, with slow spending in the background.
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
		t.Errorf("Blocked by slow TellNext onboarding: enqueueSpan = %v, expected << %v", enqueueSpan, backpressure)
	}
	t.Logf("Joining %d takes %v (slow, TellNext sleep = %v, expected backpressure ≈%v)", n, enqueueSpan, slow, backpressure)
}

// TestStreamTellQueue_WaitBlocks Wait must wait until all TellNext finishes before returning (guaranteed not to be lost).
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
	// Wait: At least wait until n TellNext lines complete (n*slow), reducing tolerance
	minExpected := time.Duration(n)*slow - 50*time.Millisecond
	if elapsed < minExpected {
		t.Errorf("Wait Before spending is finished: elapsed = %v, expected ≥ %v", elapsed, minExpected)
	}
}

// TestStreamTellQueue_CloseIdempotent Close idempotency (multiple times without panic), safe mixed with Wait.
// Lock the panic as a backup fix: Defer Close + normal Wait will both close, but once ensures no repeated close panic.
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
	q.Close() // Closing the entry gate
	q.Close() // Idempotent, no panic
	q.Wait()  // Mix Wait, and don't panic (once guaranteed)
	q.Close() // Wait and then close again, still no panic

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("Expect [a], got %v", got)
	}
	// ABORT was not triggered
	select {
	case <-sctx.Done():
		t.Error("Normal flow should not trigger abort")
	default:
	}
}

// TestStreamTellQueue_FastConsumerNoAbort Fast consumption (keep up with consumption) → If the queue is not full→ do not trigger ABORT (no accidental kill).
func TestStreamTellQueue_FastConsumerNoAbort(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, 0) // Consume quickly
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewStreamTellQueue(ctx, 32, cancel)
	for i := 0; i < 16; i++ { // Joining the fleet at 16< with a capacity of 32, and fast consumption to buffer against incompleteness
		q.Enqueue(sseMsg("m"))
	}
	q.Wait()

	require.Len(t, got, 16)
	select {
	case <-sctx.Done():
		t.Fatal("Fast consumption should not trigger abort (accidental killing of normal flow)")
	default:
	}
}

// TestStreamTellQueue_SlowConsumerAbortsUpstream Slow consumption → Buffer full → triggers ABORT (cancel upstream stop loss).
// This is the core of "bounded + full flow cutoff": when the frontend is inactive, it doesn't block Recv, doesn't consume unlimited memory, but actively abandons upstream streams.
func TestStreamTellQueue_SlowConsumerAbortsUpstream(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, 100*time.Millisecond) // Tell Next
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewStreamTellQueue(ctx, 4, cancel) // Small capacity makes it easy to fill quickly
	// Rapid onboarding exceeds capacity; Slow consumption → buffer full → abort
	for i := 0; i < 20; i++ {
		q.Enqueue(sseMsg("m"))
	}

	select {
	case <-sctx.Done():
		// abort trigger ✅
	case <-time.After(2 * time.Second):
		t.Fatal("Slow consumption buffer should be fully buffered to trigger abort (cancel upstream flow)")
	}
	if !q.Aborted() {
		t.Error("After buffering abort, Aborted() should be true")
	}
	q.Wait()
}

// TestStreamTellQueue_AbortedFlag Normal flow Aborted() = false; After buffering to full abort, Aborted()=true.
// executeStream distinguishes between "truncated" and "completed normally" based on this.
func TestStreamTellQueue_AbortedFlag(t *testing.T) {
	var mu sync.Mutex
	// Normal flow: no fullness, no abort
	ctx1 := recordingCtx(&[]string{}, &mu, 0)
	_, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	q1 := NewStreamTellQueue(ctx1, 32, cancel1)
	for i := 0; i < 8; i++ { // 8< Capacity 32, fast consumption
		q1.Enqueue(sseMsg("m"))
	}
	q1.Wait()
	if q1.Aborted() {
		t.Error("Normal flow should not be marked aborted")
	}

	// Slow consumption → buffer full → abort → aborted() = true
	ctx2 := recordingCtx(&[]string{}, &mu, 100*time.Millisecond)
	sctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	q2 := NewStreamTellQueue(ctx2, 4, cancel2)
	for i := 0; i < 20; i++ {
		q2.Enqueue(sseMsg("m"))
	}
	<-sctx2.Done() // Wait for ABORT to trigger
	if !q2.Aborted() {
		t.Error("After buffering abort, Aborted() should be true")
	}
	q2.Wait()
}

// TestStreamTellQueue_FullMode_BlockOnFull full mode (blockTimeout>0): slow consumption causes buffer fullness when blocking backpressure,
// Not immediately abort; After the consumption keeps up, they continue to join the queue and deliver everything. Unlike the 'full and immediately abort' mode in OFF mode.
// This is precisely the key to replaying burst traffic in full mode and preventing the misjudgment of frontend inactivity.
func TestStreamTellQueue_FullMode_BlockOnFull(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	ctx := recordingCtx(&got, &mu, 20*time.Millisecond) // Slow but steady consumption
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewStreamTellQueueWithBlock(ctx, 4, cancel, 3*time.Second) // full: When full, it blocks for 3 seconds before abort
	// Fast onboarding > Capacity 4: Blocks backlash and other consumption, but consumption continues (20ms per thread) and does not time out
	for i := 0; i < 12; i++ {
		q.Enqueue(sseMsg("m"))
	}

	// During joining, do not abort (blocking and pushing back, spending keeps up)
	select {
	case <-sctx.Done():
		t.Fatal("full Slow consumption model should block backlash and not abort")
	default:
	}
	q.Wait()
	require.Len(t, got, 12)
	if q.Aborted() {
		t.Error("full After following the pattern of consumption, aborted should not be marked")
	}
}

// TestStreamTellQueue_FullMode_TimeoutAborts In full mode, the frontend continuously does not consume (truly disconnected) →
// After buffering is full, block timeout → ABORT stop-loss. Unlike the "immediate abort" mode in off mode (full provides a waiting window).
func TestStreamTellQueue_FullMode_TimeoutAborts(t *testing.T) {
	// TellNext permanent block (simulates frontend disconnection, consumption completely stops)
	blockCtx := rulegotest.NewRuleContext(types.NewConfig(), func(msg types.RuleMsg, relationType string, err error) {
		select {}
	})
	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewStreamTellQueueWithBlock(blockCtx, 1, cancel, 100*time.Millisecond) // Full: Blocks 100ms when full
	q.Enqueue(sseMsg("a"))                                                      // Join the queue (consume takes and blocks in TellNext)
	q.Enqueue(sseMsg("b"))                                                      // Fill (consume stuck in TellNext(a), ch:[b])
	q.Enqueue(sseMsg("c"))                                                      // Full → blocks 100ms → timeout ABORT

	select {
	case <-sctx.Done():
		// abort trigger ✅
	case <-time.After(1 * time.Second):
		t.Fatal("full The pattern should not be consumed continuously abort the time limit")
	}
	if !q.Aborted() {
		t.Error("After timeout abort, Aborted() should be true")
	}
	// Untuned q.Wait(): consume goroutine permanently blocks TellNext, Wait freezes
}
