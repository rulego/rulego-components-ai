// Package doomloop 工具重复调用死循环检测:同名同参重复与连续失败
// 达阈值时向 LLM 回注警告文本;纯标准库,agent 与 agent/lite 共用。

package doomloop

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// doom-loop 检测阈值：同参重复只拦"无效重复"（失败重试、结果一成不变）。
// 结果每次都变化的同参重复是有效观察（如云台转动后的同参抓拍、轮询到状态变化），放行。
const (
	doomHistorySize     = 20 // 保留最近 N 次工具调用快照
	doomRepeatThreshold = 3  // 无效重复达此次数 → MILD 提示
	doomStrongThreshold = 5  // 无效重复达此次数 → STRONG 提示（放弃计划/求助，再重复任务失败）
	doomFailThreshold   = 5  // 连续失败达此次数 → doom 警告
)

// DoomLoopDetector 检测 agent 陷入死循环（重复相同调用 / 连续失败）。
// 通过 context 在 agent loop 内共享同一实例（见 WithDoomLoopDetector），
// 使跨工具的 doom 模式也能被抓到。
type DoomLoopDetector struct {
	mu      sync.Mutex
	history []doomCallSnapshot
}

type doomCallSnapshot struct {
	tool      string
	argsHash  string
	failed    bool
	resultKey string // 成功结果指纹，用于识别"结果一成不变"的无效重复
}

// NewDoomLoopDetector 创建检测器。
func NewDoomLoopDetector() *DoomLoopDetector {
	return &DoomLoopDetector{}
}

func hashArgs(args string) string {
	h := sha1.Sum([]byte(NormalizeArgsKeyOrder(args)))
	return hex.EncodeToString(h[:])
}

func hashResult(result string) string {
	h := sha1.Sum([]byte(result))
	return hex.EncodeToString(h[:])
}

// NormalizeArgsKeyOrder 规范化 JSON key 顺序：unmarshal 成 map 再 marshal（Go json 按 key 字典序），
// 避免 LLM 两次给 {"path":"a","n":1} 与 {"n":1,"path":"a"} 被判不同而漏报重复（审查 M1）。
func NormalizeArgsKeyOrder(args string) string {
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

// BeforeCall 工具执行前调用：在最近 doomHistorySize 次的滑动窗口内分析同名同参调用，
// 命中"无效重复"时返回分级警告文案（空串=放行）。不修改状态。
//
// 无效重复指失败重试（再试大概率仍失败）与结果一成不变的重复（再调学不到新信息）；
// 结果每次都变化的同参重复是有效观察，放行。
//
// 两级提示（温和→强硬递进）：
//   - 计数 ∈ [doomRepeatThreshold, doomStrongThreshold)：MILD，提醒换方法/参数
//   - 计数 >= doomStrongThreshold：STRONG，要求放弃当前计划/求助，再重复将任务失败
func (d *DoomLoopDetector) BeforeCall(tool, args string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	hash := hashArgs(args)
	total, failed, sameKey := 0, 0, 0
	var lastSuccessKey string
	for i := len(d.history) - 1; i >= 0 && i >= len(d.history)-doomHistorySize; i-- {
		e := d.history[i]
		if e.tool != tool || e.argsHash != hash {
			continue
		}
		total++
		if e.failed {
			failed++
			continue
		}
		if lastSuccessKey == "" {
			lastSuccessKey = e.resultKey
			sameKey = 1
		} else if e.resultKey == lastSuccessKey {
			sameKey++
		}
	}
	// 失败重试以失败次数计，结果一成不变以同参总数计，取最大者作为有效计数
	basis := 0
	if failed >= 2 {
		basis = failed
	}
	if sameKey >= 2 && total > basis {
		basis = total
	}
	count := basis + 1 // 含本次
	if count < doomRepeatThreshold {
		return ""
	}
	if count >= doomStrongThreshold {
		return fmt.Sprintf("工具 %s 已用相同参数调用 %d 次，之前的温和提醒未奏效。请立即：1) 放弃当前思路；2) 简述你在做什么、为什么失败；3) 换完全不同的方法或参数，或向用户求助。继续重复相同调用将导致任务失败结束。", tool, count)
	}
	return fmt.Sprintf("工具 %s 已用相同参数调用 %d 次（失败重试或结果无变化，重复无新信息）。请换用其他方法/参数，或等环境变化后再试。", tool, count)
}

// AfterCall 工具执行后调用：记录本次调用（含结果指纹）并检测"连续失败"，
// 返回警告文案（空串=无警告）。result 为工具结果文本，用于识别结果一成不变的重复。
func (d *DoomLoopDetector) AfterCall(tool, args string, failed bool, result string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.history = append(d.history, doomCallSnapshot{tool: tool, argsHash: hashArgs(args), failed: failed, resultKey: hashResult(result)})
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
