package agent

import "testing"

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
