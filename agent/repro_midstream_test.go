package agent

// 复现方案一（off/probe 窗口）部署后仍出现的 “Error in input stream”：用 mock 模拟流
// 中途断流，验证 off 透传、full 重试。不依赖网络。

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestRepro_OffMode_SecondTurnBreak_PassesError 复现用户痛点（off 模式 / 方案一）：
//
// 模拟 ReAct 两轮 model.Stream：
//   - 第 1 轮：正常（输出工具调用）
//   - 第 2 轮（工具结果后）：在探测窗口（默认 3 chunk）外断流
//
// off 模式下该错误透传给调用方/前端 = 前端显示 "Error in input stream"。
func TestRepro_OffMode_SecondTurnBreak_PassesError(t *testing.T) {
	chunks6 := []string{"这", "是", "工", "具", "结", "果"} // 6 chunk，断流发生在第 6 chunk（> 窗口 3）
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderFromChunks("tool_call: search")},                                    // 第 1 轮 model（输出工具调用）
			{stream: streamReaderWithChunksThenError(chunks6, errors.New("Error in input stream"))}, // 第 2 轮 model 断流
		},
	}
	w := NewRetryChatModelWrapper(fake, 3) // off 模式（方案一）
	w.SetStreamFull(false)

	// 第 1 轮：正常（模拟输出工具调用）
	sr1, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("第 1 轮 Stream 建立失败: %v", err)
	}
	c1, e1 := drainStream(sr1)
	t.Logf("第 1 轮（工具调用）: 内容=%v recvErr=%v", c1, e1)

	// 第 2 轮：工具结果后的 model 断流（用户痛点）
	sr2, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("第 2 轮 Stream 建立成功（探测窗口内未发现错误），got err: %v", err)
	}
	c2, e2 := drainStream(sr2)
	t.Logf("第 2 轮收到内容: %v", c2)

	if e2 == nil || !strings.Contains(e2.Error(), "input stream") {
		t.Fatalf("❌ 复现失败：off 模式窗口外断流应透传错误给调用方，got e2=%v", e2)
	}
	t.Logf("✅ 复现成功：off 模式（方案一）下第 2 轮断流透传错误 -> %v", e2)
	t.Logf("   根因：探测窗口=3，断流发生在第 6 chunk（窗口外），无法重试，错误冒到前端")
}

// TestRepro_FullMode_SecondTurnBreak_Retries 解法验证（full 模式）：
//
// 同样两轮，第 2 轮断流 → full 模式完整缓冲探测到错误 → 重试 → 第 3 次成功，
// 调用方拿到完整内容，无错误透传。
func TestRepro_FullMode_SecondTurnBreak_Retries(t *testing.T) {
	fake := &fakeChatModel{
		behaviors: []streamBehavior{
			{stream: streamReaderFromChunks("tool_call: search")},                                       // 第 1 轮
			{stream: streamReaderWithChunksThenError([]string{"断", "流", "内", "容"}, errors.New("Error in input stream"))}, // 第 2 轮断流
			{stream: streamReaderFromChunks("这是", "工具结果后的", "完整回复")},                              // 第 3 轮（重试）成功
		},
	}
	w := NewRetryChatModelWrapper(fake, 3)
	w.SetStreamFull(true)

	// 第 1 轮
	sr1, _ := w.Stream(context.Background(), nil)
	drainStream(sr1)

	// 第 2 轮：full 重试
	sr2, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("full 模式应重试成功，got err: %v", err)
	}
	c2, e2 := drainStream(sr2)
	if !errors.Is(e2, io.EOF) {
		t.Fatalf("full 模式重试后应 io.EOF，got: %v", e2)
	}
	full := strings.Join(c2, "")
	t.Logf("✅ full 模式：第 2 轮断流被重试解决，调用方拿到完整内容: %q", full)
	if full == "" {
		t.Error("内容不应为空")
	}
}
