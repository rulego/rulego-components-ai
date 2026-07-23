package agent

// Reproduce the "Error in input stream" that still appears after deployment of Solution 1 (off/probe window): simulate the stream with a mock
// Interruption of current is triggered, verification is off, transparent, and full retry. It does not rely on the internet.

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestRepro_OffMode_SecondTurnBreak_PassesError Recreate user pain points (off mode).
//
// Simulating ReAct two-wheel model.Stream:
//   - Round 1: Normal (output tool call)
//   - Round 2 (after tool results): Cut off the flow outside the probe window (default 3 chunk).
//
// In off mode, this error is passed to the caller/frontend = frontend displays "Error in input stream".
func TestRepro_OffMode_SecondTurnBreak_PassesError(t *testing.T) {
	chunks6 := []string{"这", "是", "工", "具", "结", "果"} // 6 chunk, the current breakdown occurs at the 6th chunk (> window 3)
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderFromChunks("tool_call: search")},                                   // Round 1: Model (Output Tool Call)
			{stream: streamReaderWithChunksThenError(chunks6, errors.New("Error in input stream"))}, // Round 2: Model cuts off
		},
	}
	w := NewRetryChatModelWrapper(fake, 3) // off mode (Option 1)
	w.SetStreamFull(false)

	// Round 1: Normal (analog output tool call)
	sr1, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Round 1 Stream Establishment Failure: %v", err)
	}
	c1, e1 := drainStream(sr1)
	t.Logf("Round 1 (tool call): Content = %v recvErr = %v", c1, e1)

	// Round 2: Model traffic disconnections after tool results (user pain points)
	sr2, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Round 2 Stream Establishment successful (no errors found in the detection window), got err: %v", err)
	}
	c2, e2 := drainStream(sr2)
	t.Logf("Contents received in round 2: %v", c2)

	if e2 == nil || !strings.Contains(e2.Error(), "input stream") {
		t.Fatalf("❌ Reproduction failure: off Interruption of flow outside the mode window should pass the error to the caller, got e2 = %v", e2)
	}
	t.Logf("✅ Successful reproduction: off Mode (Scheme 1) second round of disconnected flow transmission error -> %v", e2)
	t.Logf("Root cause: detection window = 3, the disconnection occurs at chunk 6 (outside the window), cannot be reattempted, error occurs at the front end")
}

// TestRepro_FullMode_SecondTurnBreak_Retries Solution Verification (Full Mode):
//
// For the same two rounds, the second round of interrupted current → full mode fully buffered to detect errors→ retry → the third successful attempt,
// The caller receives the complete content without error transmission.
func TestRepro_FullMode_SecondTurnBreak_Retries(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderFromChunks("tool_call: search")},                                                        // Round 1
			{stream: streamReaderWithChunksThenError([]string{"断", "流", "内", "容"}, errors.New("Error in input stream"))}, // Round 2: Flow interruption
			{stream: streamReaderFromChunks("这是", "工具结果后的", "完整回复")},                                                     // Round 3 (retry) was successful
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)
	w.SetStreamFull(true)

	// Round 1
	sr1, _ := w.Stream(context.Background(), nil)
	drainStream(sr1)

	// Round 2: Full retry
	sr2, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("full mode should be successfully retried and got err: %v", err)
	}
	c2, e2 := drainStream(sr2)
	if !errors.Is(e2, io.EOF) {
		t.Fatalf("full After retesting the mode, io.EOF should be got: %v", e2)
	}
	full := strings.Join(c2, "")
	t.Logf("✅ full Pattern: The second round of disconnection is retried and resolved, and the caller receives the full content: %q", full)
	if full == "" {
		t.Error("The content should not be empty")
	}
}
