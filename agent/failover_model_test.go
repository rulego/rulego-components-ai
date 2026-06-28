package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// endpointModel 可控的端点 mock，支持预设 Generate/Stream 行为，用于 failover 测试。
type endpointModel struct {
	name        string
	genResult   *schema.Message
	genErr      error
	stream      *schema.StreamReader[*schema.Message]
	streamErr   error
	genCalls    int
	streamCalls int
}

func (m *endpointModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.genCalls++
	if m.genErr != nil {
		return nil, m.genErr
	}
	if m.genResult != nil {
		return m.genResult, nil
	}
	return schema.AssistantMessage(m.name, nil), nil
}

func (m *endpointModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamCalls++
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	if m.stream != nil {
		return m.stream, nil
	}
	return streamReaderFromChunks(m.name), nil
}

func (m *endpointModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// TestFailover_GeneratePrimaryFailsToBackup 主端点 Generate 可重试失败 → 切备用成功。
func TestFailover_GeneratePrimaryFailsToBackup(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("status code: 502 bad gateway")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	msg, err := w.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("期望 failover 后成功，got err: %v", err)
	}
	if msg.Content != "OK" {
		t.Fatalf("期望内容 OK，got %s", msg.Content)
	}
	if backup.genCalls != 1 {
		t.Fatalf("备用端点应被调用 1 次，got %d", backup.genCalls)
	}
}

// TestFailover_GenerateAllFail 所有端点都失败 → 汇总错误。
func TestFailover_GenerateAllFail(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup := &endpointModel{name: "backup", genErr: errors.New("503")}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	_, err := w.Generate(context.Background(), nil)
	if err == nil {
		t.Fatal("期望全部失败后报错")
	}
	if !strings.Contains(err.Error(), "failed over all endpoints") {
		t.Fatalf("期望汇总错误，got: %v", err)
	}
}

// TestFailover_GenerateNonRetryableNoSwitch 主端点不可重试错误 → 不切换备用。
func TestFailover_GenerateNonRetryableNoSwitch(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("invalid_api_key")}
	backup := &endpointModel{name: "backup"}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	_, err := w.Generate(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "invalid_api_key") {
		t.Fatalf("期望透传不可重试错误，got: %v", err)
	}
	if backup.genCalls != 0 {
		t.Fatalf("不可重试错误不应切换，备用被调用 %d 次", backup.genCalls)
	}
}

// TestFailover_StreamPrimaryFailsToBackup 主端点 Stream 失败 → 切备用成功。
func TestFailover_StreamPrimaryFailsToBackup(t *testing.T) {
	primary := &endpointModel{name: "primary", streamErr: errors.New("Error in input stream")}
	backup := &endpointModel{name: "backup", stream: streamReaderFromChunks("OK")}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("期望 failover 后成功，got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("期望 io.EOF，got: %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("期望 [OK]，got %v", contents)
	}
	if backup.streamCalls != 1 {
		t.Fatalf("备用端点 Stream 应被调用 1 次，got %d", backup.streamCalls)
	}
}

// TestFailover_StreamAllFail 所有端点 Stream 失败 → 汇总错误。
func TestFailover_StreamAllFail(t *testing.T) {
	primary := &endpointModel{name: "primary", streamErr: errors.New("502")}
	backup := &endpointModel{name: "backup", streamErr: errors.New("503")}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	sr, err := w.Stream(context.Background(), nil)
	if sr != nil {
		sr.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "failed over all endpoints") {
		t.Fatalf("期望汇总错误，got: %v", err)
	}
}

// TestFailover_NoFailoverEqualsPrimary 无备用端点时等价于直接用 primary。
func TestFailover_NoFailoverEqualsPrimary(t *testing.T) {
	primary := &endpointModel{name: "primary", genResult: schema.AssistantMessage("P", nil)}
	w := NewFailoverChatModelWrapper(primary, nil)

	msg, err := w.Generate(context.Background(), nil)
	if err != nil || msg.Content != "P" {
		t.Fatalf("期望 primary 直接返回 P，got msg=%v err=%v", msg, err)
	}
}

// TestFailover_GeneratePrimarySuccessNoSwitch 主端点成功 → 不切换备用。
func TestFailover_GeneratePrimarySuccessNoSwitch(t *testing.T) {
	primary := &endpointModel{name: "primary", genResult: schema.AssistantMessage("OK", nil)}
	backup := &endpointModel{name: "backup"}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	msg, err := w.Generate(context.Background(), nil)
	if err != nil || msg.Content != "OK" {
		t.Fatalf("期望 primary 直接返回 OK，got msg=%v err=%v", msg, err)
	}
	if backup.genCalls != 0 {
		t.Fatalf("主成功时备用不应被调用，got %d", backup.genCalls)
	}
}

// TestFailover_GenerateMultiBackup 主 + 备1 失败 → 备2 成功（多端点依次切换）。
func TestFailover_GenerateMultiBackup(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup1 := &endpointModel{name: "backup1", genErr: errors.New("503")}
	backup2 := &endpointModel{name: "backup2", genResult: schema.AssistantMessage("OK2", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup1, backup2})

	msg, err := w.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("期望 failover 到 backup2 成功，got err: %v", err)
	}
	if msg.Content != "OK2" {
		t.Fatalf("期望 OK2，got %s", msg.Content)
	}
	if backup1.genCalls != 1 || backup2.genCalls != 1 {
		t.Fatalf("期望 backup1/backup2 各调用 1 次，got %d/%d", backup1.genCalls, backup2.genCalls)
	}
}

// TestFailover_StreamPrimarySuccessNoSwitch 主端点 Stream 成功 → 不切换备用。
func TestFailover_StreamPrimarySuccessNoSwitch(t *testing.T) {
	primary := &endpointModel{name: "primary", stream: streamReaderFromChunks("OK")}
	backup := &endpointModel{name: "backup"}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("期望 primary 直接成功，got err: %v", err)
	}
	contents, _ := drainStream(sr)
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("期望 [OK]，got %v", contents)
	}
	if backup.streamCalls != 0 {
		t.Fatalf("主成功时备用 Stream 不应被调用，got %d", backup.streamCalls)
	}
}

// ===== 熔断器（circuit breaker）测试 =====

// TestCircuit_OpensAndSkipsPrimary 主连续失败达阈值 → 熔断 open → 后续跳过主直接用备用。
func TestCircuit_OpensAndSkipsPrimary(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")} // 主持续失败
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(2, 50*time.Millisecond) // 阈值 2，冷却 50ms

	// 前 2 次：试主失败（累计）→ 切备用成功。主被试 2 次（达阈值 → open）。
	for i := 0; i < 2; i++ {
		msg, err := w.Generate(context.Background(), nil)
		if err != nil || msg.Content != "OK" {
			t.Fatalf("第 %d 次应 failover 成功，got msg=%v err=%v", i+1, msg, err)
		}
	}
	if primary.genCalls != 2 {
		t.Fatalf("阈值 2，主应被试 2 次后熔断，got %d", primary.genCalls)
	}

	// 第 3 次：主已 open → 跳过主直接备用。主不再被试。
	primary.genCalls = 0
	msg, err := w.Generate(context.Background(), nil)
	if err != nil || msg.Content != "OK" {
		t.Fatalf("熔断后应直接用 backup，got msg=%v err=%v", msg, err)
	}
	if primary.genCalls != 0 {
		t.Fatalf("熔断后应跳过主，但主被试 %d 次", primary.genCalls)
	}
}

// TestCircuit_HalfOpenRecovery open 冷却到期 → half-open → 试探主成功 → 恢复 closed。
func TestCircuit_HalfOpenRecovery(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(2, 50*time.Millisecond)

	// 2 次失败 → open
	w.Generate(context.Background(), nil)
	w.Generate(context.Background(), nil)

	time.Sleep(60 * time.Millisecond) // 冷却到期 → half-open

	// 让主这次成功（模拟主恢复）
	primary.genErr = nil
	primary.genResult = schema.AssistantMessage("PRIMARY", nil)
	msg, err := w.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("half-open 主成功应返回，got err: %v", err)
	}
	if msg.Content != "PRIMARY" {
		t.Fatalf("half-open 应试主并成功，got %s", msg.Content)
	}
	// 恢复 closed 后，下次请求直接用主
	primary.genCalls = 0
	w.Generate(context.Background(), nil)
	if primary.genCalls != 1 {
		t.Fatalf("恢复 closed 后应直接试主，got %d", primary.genCalls)
	}
}

// TestCircuit_HalfOpenReOpensOnFailure half-open 试探主失败 → 重新 open（继续跳过主）。
func TestCircuit_HalfOpenReOpensOnFailure(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")} // 主持续失败
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(2, 50*time.Millisecond)

	// 2 次失败 → open
	w.Generate(context.Background(), nil)
	w.Generate(context.Background(), nil)

	time.Sleep(60 * time.Millisecond) // 冷却到期 → half-open

	// half-open 试主一次，主仍失败 → 重新 open
	primary.genCalls = 0
	w.Generate(context.Background(), nil)
	if primary.genCalls != 1 {
		t.Fatalf("half-open 应试主一次，got %d", primary.genCalls)
	}
	// 重新 open，下次跳过主
	primary.genCalls = 0
	w.Generate(context.Background(), nil)
	if primary.genCalls != 0 {
		t.Fatalf("half-open 失败重新 open 后应跳过主，got %d", primary.genCalls)
	}
}

// TestCircuit_DisabledAlwaysTriesPrimary 未启用熔断器（无 WithCircuit）→ 每次请求都试主。
func TestCircuit_DisabledAlwaysTriesPrimary(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}) // 无熔断

	for i := 0; i < 5; i++ {
		w.Generate(context.Background(), nil)
	}
	if primary.genCalls != 5 {
		t.Fatalf("无熔断应每次请求都试主，期望 5 次，got %d", primary.genCalls)
	}
}

// TestFailover_WithTools_SharesCircuit WithTools 后熔断器状态共享持续：
// 主已熔断 open 时，WithTools 产生的新 wrapper 仍跳过主（不会因重置而再试主）。
// 防回归：原实现 WithTools 会 newCircuitBreaker 重置，导致 eino react 每次绑工具都熔断失效。
func TestFailover_WithTools_SharesCircuit(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("status code: 502 bad gateway")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})
	w = w.WithCircuit(2, time.Hour) // 阈值 2，冷却 1h（测试期间保持 open）

	// 触发主失败 2 次达阈值 → 熔断 open
	_, _ = w.Generate(context.Background(), nil)
	_, _ = w.Generate(context.Background(), nil)
	callsBefore := primary.genCalls

	// WithTools 绑工具（eino react 每次 Stream 常这么做）
	w2, err := w.WithTools(nil)
	if err != nil {
		t.Fatalf("WithTools 失败: %v", err)
	}

	// 新 wrapper Generate：熔断器共享应仍 open → 跳过主直接 backup
	_, err = w2.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("期望 failover 到 backup 成功，got err: %v", err)
	}
	if primary.genCalls != callsBefore {
		t.Errorf("WithTools 后熔断器应共享：主不应被调用（open），但 genCalls 从 %d 增到 %d",
			callsBefore, primary.genCalls)
	}
}

// TestCircuit_HalfOpenAllowsOnlyOneProbe half-open 期间只放行一个请求试探主，其余走备用。
// 防回归：避免冷却到期瞬间并发请求全部打到刚（未）恢复的主。
func TestCircuit_HalfOpenAllowsOnlyOneProbe(t *testing.T) {
	c := newCircuitBreaker(1, time.Hour)
	// 直接构造 open 且已过冷却 → 下次 allowPrimary 转 half-open
	c.state = circuitOpen
	c.openUntil = time.Now().Add(-time.Second)

	// 首个请求：转 half-open + 占用试探权 → 放行试主
	if !c.allowPrimary() {
		t.Fatal("half-open 首个请求应被放行试探主")
	}
	if c.getState() != circuitHalfOpen {
		t.Fatalf("应处于 half-open，got %v", c.getState())
	}
	// 后续请求：试探权已占用 → 拒绝（走备用）
	for i := 0; i < 5; i++ {
		if c.allowPrimary() {
			t.Fatalf("half-open 期间只应放行一个请求，第 %d 个额外请求被错误放行", i+2)
		}
	}

	// 试探成功 → 释放试探权 + 恢复 closed
	c.recordSuccess()
	if c.getState() != circuitClosed {
		t.Fatalf("试探成功应恢复 closed，got %v", c.getState())
	}
	// 恢复后允许用主
	if !c.allowPrimary() {
		t.Fatal("closed 应允许用主")
	}
}

// TestCircuit_HalfOpenProbeReleasedOnFailure half-open 试探失败释放试探权，下个冷却到期可再次试探。
func TestCircuit_HalfOpenProbeReleasedOnFailure(t *testing.T) {
	c := newCircuitBreaker(1, time.Hour)
	c.state = circuitHalfOpen
	c.halfOpenProbing = true // 模拟已有请求在试探

	c.recordFailure() // 试探失败 → 重新 open + 释放试探权
	if c.getState() != circuitOpen {
		t.Fatal("half-open 试探失败应重新 open")
	}
	// 冷却到期后应能再次抢占试探（证明试探权已释放）
	c.openUntil = time.Now().Add(-time.Second)
	if !c.allowPrimary() {
		t.Fatal("试探权释放后，冷却到期应能再次抢占试探")
	}
}
