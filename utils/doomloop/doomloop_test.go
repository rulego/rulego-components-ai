package doomloop

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
		d.AfterCall("read", args, false, "package main")
	}
	// 第 3 次（前两次结果一致，无新信息）应触发
	if w := d.BeforeCall("read", args); w == "" {
		t.Fatal("expected doom warn on 3rd identical call with unchanged result")
	}
}

// 结果变化的同参重复是有效观察（云台转动后的同参抓拍、轮询到状态变化），放行。
func TestDoomLoop_ObservationRepeatAllowed(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"camera":"cam-1"}`
	for i := 0; i < 7; i++ {
		if w := d.BeforeCall("camera_snapshot", args); w != "" {
			t.Fatalf("call %d: productive observation should not warn: %s", i+1, w)
		}
		d.AfterCall("camera_snapshot", args, false, fmt.Sprintf("已拍下画面 snapshot_%d.jpg", i))
	}
}

// 失败的同参重试：第 3 次起提示换方法（原始防重试风暴语义保留）。
func TestDoomLoop_FailedRepeatWarns(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	for i := 0; i < 2; i++ {
		if w := d.BeforeCall("write", args); w != "" {
			t.Fatalf("call %d: unexpected warn: %s", i+1, w)
		}
		d.AfterCall("write", args, true, "Error: permission denied")
	}
	if w := d.BeforeCall("write", args); w == "" {
		t.Fatal("expected doom warn on 3rd failed retry")
	}
	// 混入成功结果后失败重试仍应计数警告
	d.AfterCall("write", args, true, "Error: permission denied")
	if w := d.BeforeCall("write", args); w == "" {
		t.Fatal("expected doom warn after 3 failures")
	}
}

func TestDoomLoop_DifferentArgsNoWarn(t *testing.T) {
	d := NewDoomLoopDetector()
	d.AfterCall("read", `{"path":"a.go"}`, false, "a")
	d.AfterCall("read", `{"path":"b.go"}`, false, "b")
	if w := d.BeforeCall("read", `{"path":"c.go"}`); w != "" {
		t.Fatalf("different args should not warn: %s", w)
	}
}

func TestDoomLoop_ConsecutiveFailures(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	var warn string
	for i := 0; i < 5; i++ {
		warn = d.AfterCall("edit", args, true, "boom")
	}
	if warn == "" {
		t.Fatal("expected consecutive-failure warn after 5 failures")
	}
}

func TestDoomLoop_FailThenSuccessResets(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	for i := 0; i < 4; i++ {
		d.AfterCall("edit", args, true, "boom") // 4 次失败，failRun=4 < 5 不警告
	}
	if w := d.AfterCall("edit", args, false, "ok"); w != "" {
		t.Fatalf("success should reset and not warn: %s", w)
	}
	// 成功后连续失败计数重启，单次失败不应警告
	if w := d.AfterCall("edit", args, true, "boom"); w != "" {
		t.Fatalf("single failure after reset should not warn: %s", w)
	}
}

// 滑动窗口：交错相同调用也应检测（旧"末尾严格连续"会漏检，这正是改用窗口的动机）。
// 场景 read(a)→bash→read(a)→bash→read(a)：read(a) 结果无变化，第 3 次应告警。
func TestDoomLoop_InterleavedRepeat(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	d.AfterCall("read", args, false, "same")
	d.AfterCall("bash", `{"cmd":"x"}`, false, "ok")
	d.AfterCall("read", args, false, "same")
	d.AfterCall("bash", `{"cmd":"y"}`, false, "ok")
	// 窗口内 read(a) 已 2 次且结果一致，本次第 3 次 → 应告警
	if w := d.BeforeCall("read", args); w == "" {
		t.Fatal("interleaved repeat should warn under sliding window")
	}
}

// 窗口边界：被推出 doomHistorySize 窗口的旧调用不计入重复计数。
func TestDoomLoop_WindowBoundary(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"path":"a.go"}`
	d.AfterCall("read", args, false, "same")
	d.AfterCall("read", args, false, "same")
	// 用 20 个其他调用把它们推出窗口（doomHistorySize=20）
	for i := 0; i < 20; i++ {
		d.AfterCall("bash", fmt.Sprintf(`{"c":%d}`, i), false, "ok")
	}
	// 窗口内已无 read(a)，本次为窗口内首次 → 不告警
	if w := d.BeforeCall("read", args); w != "" {
		t.Fatalf("reads pushed out of window should not count: %s", w)
	}
}

// 两级提示：无效重复计数 3~4 为 MILD（提醒换方法），≥5 为 STRONG（放弃计划/求助）。
func TestDoomLoop_TwoLevelPrompt(t *testing.T) {
	args := `{"path":"a.go"}`

	// count=3（history 2 次 + 本次，结果一致）→ MILD
	d := NewDoomLoopDetector()
	d.AfterCall("write", args, false, "done")
	d.AfterCall("write", args, false, "done")
	wMild := d.BeforeCall("write", args)
	if wMild == "" {
		t.Fatal("count=3 should warn (MILD)")
	}
	if strings.Contains(wMild, "放弃当前思路") {
		t.Fatalf("count=3 should be MILD not STRONG: %s", wMild)
	}

	// count=5（history 4 次 + 本次，结果一致）→ STRONG
	d2 := NewDoomLoopDetector()
	for i := 0; i < 4; i++ {
		d2.AfterCall("write", args, false, "done")
	}
	wStrong := d2.BeforeCall("write", args)
	if !strings.Contains(wStrong, "放弃当前思路") {
		t.Fatalf("count=5 should be STRONG: %s", wStrong)
	}

	// count=4 → 仍 MILD（边界：strong 阈值是 5，4 不触发 strong）
	d3 := NewDoomLoopDetector()
	for i := 0; i < 3; i++ {
		d3.AfterCall("write", args, false, "done")
	}
	w4 := d3.BeforeCall("write", args)
	if w4 == "" || strings.Contains(w4, "放弃当前思路") {
		t.Fatalf("count=4 should be MILD: %s", w4)
	}
}

// 结果一成不变的同参成功重复与失败重试并存时，取最大计数，保证升级到 STRONG 不被打断。
func TestDoomLoop_BlockedThenEscalate(t *testing.T) {
	d := NewDoomLoopDetector()
	args := `{"device":"d1"}`
	// 两次相同结果的成功 → 第 3 次 MILD（模拟第 3 次被拦，记失败）
	d.AfterCall("control_device", args, false, "已执行")
	d.AfterCall("control_device", args, false, "已执行")
	if w := d.BeforeCall("control_device", args); w == "" || strings.Contains(w, "放弃当前思路") {
		t.Fatalf("3rd identical call should be MILD: %q", w)
	}
	d.AfterCall("control_device", args, true, "") // 被拦的调用记失败
	d.AfterCall("control_device", args, true, "")
	// 窗口内同参已有 4 次（2 成功同结果 + 2 失败），本次第 5 次 → STRONG
	w := d.BeforeCall("control_device", args)
	if !strings.Contains(w, "放弃当前思路") {
		t.Fatalf("5th identical call should be STRONG: %q", w)
	}
}
