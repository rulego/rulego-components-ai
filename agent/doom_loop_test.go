package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestDoomLoop_RepeatSameArgs(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	// 前两次：history 不足，无警告
	for i := 0; i < 2; i++ {
		if w := d.BeforeCall("read", args); w != "" {
			t.Fatalf("call %d: unexpected warn: %s", i+1, w)
		}
		d.AfterCall("read", args, false)
	}
	// 第 3 次（history 已 2 次相同）应触发
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
		d.AfterCall("edit", args, true) // 4 次失败，failRun=4 < 5 不警告
	}
	if w := d.AfterCall("edit", args, false); w != "" {
		t.Fatalf("success should reset and not warn: %s", w)
	}
	// 成功后连续失败计数重启，单次失败不应警告
	if w := d.AfterCall("edit", args, true); w != "" {
		t.Fatalf("single failure after reset should not warn: %s", w)
	}
}

// 滑动窗口：交错相同调用也应检测（旧"末尾严格连续"会漏检，这正是改用窗口的动机）。
// 场景 read(a)→bash→read(a)→bash→read(a)：第 3 次 read(a) 在窗口内累计 3 次应告警。
func TestDoomLoop_InterleavedRepeat(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	d.AfterCall("read", args, false)
	d.AfterCall("bash", `{"cmd":"x"}`, false)
	d.AfterCall("read", args, false)
	d.AfterCall("bash", `{"cmd":"y"}`, false)
	// 窗口内 read(a) 已 2 次，本次第 3 次 → 应告警
	if w := d.BeforeCall("read", args); w == "" {
		t.Fatal("interleaved repeat should warn under sliding window")
	}
}

// 窗口边界：被推出 doomHistorySize 窗口的旧调用不计入重复计数。
func TestDoomLoop_WindowBoundary(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	d.AfterCall("read", args, false)
	d.AfterCall("read", args, false)
	// 用 20 个其他调用把它们推出窗口（doomHistorySize=20）
	for i := 0; i < 20; i++ {
		d.AfterCall("bash", fmt.Sprintf(`{"c":%d}`, i), false)
	}
	// 窗口内已无 read(a)，本次为窗口内首次 → 不告警
	if w := d.BeforeCall("read", args); w != "" {
		t.Fatalf("reads pushed out of window should not count: %s", w)
	}
}

// 两级提示：count 3~4 为 MILD（提醒换方法），count ≥5 为 STRONG（放弃计划/求助）。
func TestDoomLoop_TwoLevelPrompt(t *testing.T) {
	args := `{"path":"a.go"}`

	// count=3（history 2 次 + 本次）→ MILD
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

	// count=5（history 4 次 + 本次）→ STRONG
	d2 := NewDoomLoopDetector()
	for i := 0; i < 4; i++ {
		d2.AfterCall("write", args, false)
	}
	wStrong := d2.BeforeCall("write", args)
	if !strings.Contains(wStrong, "放弃当前思路") {
		t.Fatalf("count=5 should be STRONG: %s", wStrong)
	}

	// count=4 → 仍 MILD（边界：strong 阈值是 5，4 不触发 strong）
	d3 := NewDoomLoopDetector()
	for i := 0; i < 3; i++ {
		d3.AfterCall("write", args, false)
	}
	w4 := d3.BeforeCall("write", args)
	if w4 == "" || strings.Contains(w4, "放弃当前思路") {
		t.Fatalf("count=4 should be MILD: %s", w4)
	}
}
