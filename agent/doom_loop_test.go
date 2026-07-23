package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestDoomLoop_RepeatSameArgs(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	// The first two times: insufficient history, no warning
	for i := 0; i < 2; i++ {
		if w := d.BeforeCall("read", args); w != "" {
			t.Fatalf("call %d: unexpected warn: %s", i+1, w)
		}
		d.AfterCall("read", args, false)
	}
	// The third time (history has been the same twice) should be triggered
	if w := d.BeforeCall("read", args); w == "" {
		t.Fatal("expected doom warn on 3rd identical call")
	}
}

func TestDoomLoop_DifferentArgsNoWarn(t *testing.T) {
	d := NewDoomLoopDetector()
	d.AfterCall("read", `{"path":"a.go"}`, false)
	d.AfterCall("read", `{"path":"b.go"}`, false)
	if w := d.BeforeCall("read", `{"path":"c.go"}`); w != "" {
		t.Fatalf("different args should not warn: %s", w)
	}
}

func TestDoomLoop_ConsecutiveFailures(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	var warn string
	for i := 0; i < 5; i++ {
		warn = d.AfterCall("edit", args, true)
	}
	if warn == "" {
		t.Fatal("expected consecutive-failure warn after 5 failures")
	}
}

func TestDoomLoop_FailThenSuccessResets(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	for i := 0; i < 4; i++ {
		d.AfterCall("edit", args, true) // 4 failures, failRun = 4 < 5 No warning
	}
	if w := d.AfterCall("edit", args, false); w != "" {
		t.Fatalf("success should reset and not warn: %s", w)
	}
	// After success, the count restarts after consecutive failures; single failures should not be warned
	if w := d.AfterCall("edit", args, true); w != "" {
		t.Fatalf("single failure after reset should not warn: %s", w)
	}
}

// Sliding window: Interleaved identical calls should also be detected (the old "strict continuous" at the end would be missed, which is the motivation for switching to a window).
// Scene read(a)→bash→read(a)→bash→read(a): The third read(a) should trigger an alarm after accumulating 3 times in the window.
func TestDoomLoop_InterleavedRepeat(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	d.AfterCall("read", args, false)
	d.AfterCall("bash", `{"cmd":"x"}`, false)
	d.AfterCall("read", args, false)
	d.AfterCall("bash", `{"cmd":"y"}`, false)
	// Read(a) has been done twice in the window, and this third time → should trigger an alarm
	if w := d.BeforeCall("read", args); w == "" {
		t.Fatal("interleaved repeat should warn under sliding window")
	}
}

// Window boundary: Old calls to the doomHistorySize window are not counted against duplicate counts.
func TestDoomLoop_WindowBoundary(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	d.AfterCall("read", args, false)
	d.AfterCall("read", args, false)
	// Use 20 other calls to push them out of the window (doomHistorySize=20)
	for i := 0; i < 20; i++ {
		d.AfterCall("bash", fmt.Sprintf(`{"c":%d}`, i), false)
	}
	// There is no read(a) left in the window; this is the first time the → has not alerted in the window
	if w := d.BeforeCall("read", args); w != "" {
		t.Fatalf("reads pushed out of window should not count: %s", w)
	}
}

// Two-level hints: count 3~4 is MILD (Reminder Change Method), count ≥5 is STRONG (Give Up Plan/Seek Help).
func TestDoomLoop_TwoLevelPrompt(t *testing.T) {
	args := `{"path":"a.go"}`

	// count=3 (history 2 times + this time) → MILD
	d := NewDoomLoopDetector()
	d.AfterCall("write", args, false)
	d.AfterCall("write", args, false)
	wMild := d.BeforeCall("write", args)
	if wMild == "" {
		t.Fatal("count=3 should warn (MILD)")
	}
	if strings.Contains(wMild, "放弃当前思路") {
		t.Fatalf("count=3 should be MILD not STRONG: %s", wMild)
	}

	// count=5 (history 4 times + this time) → STRONG
	d2 := NewDoomLoopDetector()
	for i := 0; i < 4; i++ {
		d2.AfterCall("write", args, false)
	}
	wStrong := d2.BeforeCall("write", args)
	if !strings.Contains(wStrong, "放弃当前思路") {
		t.Fatalf("count=5 should be STRONG: %s", wStrong)
	}

	// count=4 → still MILD (boundary: strong threshold is 5, 4 does not trigger strong)
	d3 := NewDoomLoopDetector()
	for i := 0; i < 3; i++ {
		d3.AfterCall("write", args, false)
	}
	w4 := d3.BeforeCall("write", args)
	if w4 == "" || strings.Contains(w4, "放弃当前思路") {
		t.Fatalf("count=4 should be MILD: %s", w4)
	}
}
