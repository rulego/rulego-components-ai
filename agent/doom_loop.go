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

// doom-loop 检测阈值：同 tool+input 连续 3 次熔断。
// MVP 仅做"日志告警 + 结果前缀警告"，不做硬熔断（后置）。
const (
	doomHistorySize     = 20 // 保留最近 N 次工具调用快照
	doomRepeatThreshold = 3  // 连续相同 (tool, args) 达到此次数 → doom 警告
	doomFailThreshold   = 5  // 连续失败达到此次数 → doom 警告
)

// DoomLoopDetector 检测 agent 陷入死循环（重复相同调用 / 连续失败）。
// 通过 context 在 agent loop 内共享同一实例（见 WithDoomLoopDetector），
// 使跨工具的 doom 模式也能被抓到。
type DoomLoopDetector struct {
	mu      sync.Mutex
	history []doomCallSnapshot
}

type doomCallSnapshot struct {
	tool     string
	argsHash string
	failed   bool
}

// NewDoomLoopDetector 创建检测器。
func NewDoomLoopDetector() *DoomLoopDetector {
	return &DoomLoopDetector{}
}

func hashArgs(args string) string {
	h := sha1.Sum([]byte(normalizeArgsKeyOrder(args)))
	return hex.EncodeToString(h[:])
}

// normalizeArgsKeyOrder 规范化 JSON key 顺序：unmarshal 成 map 再 marshal（Go json 按 key 字典序），
// 避免 LLM 两次给 {"path":"a","n":1} 与 {"n":1,"path":"a"} 被判不同而漏报重复（审查 M1）。
func normalizeArgsKeyOrder(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return args
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return args // 非 JSON 或解析失败，回退原字符串
	}
	b, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return string(b)
}

// BeforeCall 工具执行前调用：基于历史检测"连续相同调用"，返回警告文案（空串=无警告）。不修改状态。
func (d *DoomLoopDetector) BeforeCall(tool, args string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	hash := hashArgs(args)
	repeat := 0
	for i := len(d.history) - 1; i >= 0; i-- {
		s := d.history[i]
		if s.tool == tool && s.argsHash == hash {
			repeat++
		} else {
			break
		}
	}
	if repeat+1 >= doomRepeatThreshold {
		return fmt.Sprintf("⚠️ Doom-loop: %s 已用相同参数连续调用 %d 次。文件可能已变化或当前方法无效——重新读取文件(read op=file)或换思路后再试，不要重复无效调用。", tool, repeat+1)
	}
	return ""
}

// AfterCall 工具执行后调用：记录本次调用并检测"连续失败"，返回警告文案（空串=无警告）。
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

// ---- context 注入（agent 级共享）----

type doomLoopDetectorKey struct{}

// WithDoomLoopDetector 把检测器存入 context（由 agent loop 在构建运行上下文时注入一次）。
func WithDoomLoopDetector(ctx context.Context, d *DoomLoopDetector) context.Context {
	return context.WithValue(ctx, doomLoopDetectorKey{}, d)
}

// GetDoomLoopDetector 从 context 取检测器；未注入返回 nil（调用方退化为 per-wrapper）。
func GetDoomLoopDetector(ctx context.Context) *DoomLoopDetector {
	if d, ok := ctx.Value(doomLoopDetectorKey{}).(*DoomLoopDetector); ok {
		return d
	}
	return nil
}
