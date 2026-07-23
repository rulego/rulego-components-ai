package agent

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// doom-loop detection threshold: same as tool+input for 3 consecutive circuit blows.
// MVP only performs "log alerts + result prefix warnings," without hard circuit breakers (post-installation).
const (
	doomHistorySize     = 20 // Keeps the snapshot of the most recent N tool calls
	doomRepeatThreshold = 3  // Same name and participant this time, → MILD reminder
	doomStrongThreshold = 5  // Same name and participant this time → STRONG prompt (abandoning plan/seeking help, repeating mission failure)
	doomFailThreshold   = 5  // Consecutive failures have reached the → doom warning
)

// DoomLoopDetector detects that the agent is stuck in a dead loop (repeating the same call).
// Sharing the same instance within the agent loop via context (see WithDoomLoopDetector),
// This allows cross-tool doom modes to be caught as well.
type DoomLoopDetector struct {
	mu      sync.Mutex
	history []doomCallSnapshot
}

type doomCallSnapshot struct {
	tool     string
	argsHash string
	failed   bool
}

// NewDoomLoopDetector creates a detector.
func NewDoomLoopDetector() *DoomLoopDetector {
	return &DoomLoopDetector{}
}

func hashArgs(args string) string {
	h := sha1.Sum([]byte(normalizeArgsKeyOrder(args)))
	return hex.EncodeToString(h[:])
}

// normalizeArgsKeyOrder Normalizes JSON key order: unmarshal becomes map and then marshal (Go json by key dictionary order),
// Avoid duplicate reports (review M1) when LLMs are given {"path":"a","n":1} and {"n":1,"path":"a"} are judged differently (review M1).
func normalizeArgsKeyOrder(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return args
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return args // If the string is not JSON or parsing fails, revert the original string
	}
	b, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return string(b)
}

// Before the BeforeCall tool executes: Counts the number of calls with the same name and reference in the most recent doomHistorySize sliding window
// (Excluding this time), return the rated warning text (blank string = no warning). No changes to the status.
//
// Sliding window instead of "strictly continuous": agents often trigger multiple tool_call in parallel, and concurrent interleaving causes continuous detection at the end
// Missed reports (test: 6 consecutive identical writes failed to alert due to interleaved errors, breaking the provider guardrail). Window counting is robust to staggered pairs.
//
// Two-level prompts (gentle → forceful progression):
//   - count ∈ [doomRepeatThreshold, doomStrongThreshold): MILD, reminds you to change methods/parameters
//   - count > = doomStrongThreshold: STRONG, requires abandoning the current plan/seeking help, then repeating the task to fail
func (d *DoomLoopDetector) BeforeCall(tool, args string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	hash := hashArgs(args)
	repeat := 0
	for i := len(d.history) - 1; i >= 0 && i >= len(d.history)-doomHistorySize; i-- {
		if d.history[i].tool == tool && d.history[i].argsHash == hash {
			repeat++
		}
	}
	count := repeat + 1 // Including this event
	if count < doomRepeatThreshold {
		return ""
	}
	if count >= doomStrongThreshold {
		return fmt.Sprintf("工具 %s 已用相同参数调用 %d 次，之前的温和提醒未奏效。请立即：1) 放弃当前思路；2) 简述你在做什么、为什么失败；3) 换完全不同的方法或参数，或向用户求助。继续重复相同调用将导致任务失败结束。", tool, count)
	}
	return fmt.Sprintf("工具 %s 已用相同参数调用 %d 次（可能文件已变化或方法无效）。请重新读取相关文件确认当前状态，或换用其他方法/参数，不要重复无效调用。", tool, count)
}

// After the AfterCall tool executes, it records the call and detects "consecutive failures," returning a warning text (empty string = no warning).
func (d *DoomLoopDetector) AfterCall(tool, args string, failed bool) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.history = append(d.history, doomCallSnapshot{tool: tool, argsHash: hashArgs(args), failed: failed})
	if len(d.history) > doomHistorySize {
		d.history = d.history[len(d.history)-doomHistorySize:]
	}

	failRun := 0
	for i := len(d.history) - 1; i >= 0; i-- {
		if d.history[i].failed {
			failRun++
		} else {
			break
		}
	}
	if failRun >= doomFailThreshold {
		return fmt.Sprintf("⚠️ Doom-loop: 已连续 %d 次工具调用失败。停止猜测，按 systematic-debugging：完整阅读错误信息、定位根因后再重试。", failRun)
	}
	return ""
}

// ---- context injection (agent-level sharing)----

type doomLoopDetectorKey struct{}

// WithDoomLoopDetector stores the detector in the context (injected once by the agent loop when building and running the context).
func WithDoomLoopDetector(ctx context.Context, d *DoomLoopDetector) context.Context {
	return context.WithValue(ctx, doomLoopDetectorKey{}, d)
}

// GetDoomLoopDetector fetchs the detector from the context; Uninjected returns nil (the caller degenerates to per-wrapper).
func GetDoomLoopDetector(ctx context.Context) *DoomLoopDetector {
	if d, ok := ctx.Value(doomLoopDetectorKey{}).(*DoomLoopDetector); ok {
		return d
	}
	return nil
}
