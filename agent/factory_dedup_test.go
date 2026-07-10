package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// mkAssistantWithTool 构造带单个 tool_call 的 assistant 消息。
func mkAssistantWithTool(name, args, callID string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:       callID,
			Type:     "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}},
	}
}

// mkTool 构造 tool result 消息。
func mkTool(content, callID string) *schema.Message {
	return &schema.Message{
		Role:       schema.Tool,
		Content:    content,
		ToolCallID: callID,
	}
}

// 无重复：原样返回（条数不变）。
func TestDedup_NoRepeat(t *testing.T) {
	in := []*schema.Message{
		schema.UserMessage("hi"),
		mkAssistantWithTool("read", `{"path":"a"}`, "c1"),
		mkTool("res1", "c1"),
		mkAssistantWithTool("read", `{"path":"b"}`, "c2"),
		mkTool("res2", "c2"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)
	if len(out) != len(in) {
		t.Fatalf("no-repeat should be unchanged: got %d want %d", len(out), len(in))
	}
}

// 3 连重复：折叠到 keepLast=2，删 1 对(assistant+tool)，保留轮 tool result 加提示。
func TestDedup_ThreeRepeatsCollapse(t *testing.T) {
	args := `{"mode":"overwrite","path":"/f","content":"\n"}`
	in := []*schema.Message{
		schema.UserMessage("do"),
		mkAssistantWithTool("write", args, "c1"),
		mkTool("Success: Overwrote", "c1"),
		mkAssistantWithTool("write", args, "c2"),
		mkTool("Success: Overwrote", "c2"),
		mkAssistantWithTool("write", args, "c3"),
		mkTool("Success: Overwrote", "c3"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)
	wantLen := len(in) - 2 // 删 1 对
	if len(out) != wantLen {
		t.Fatalf("3-repeat should collapse 1 pair: got %d want %d", len(out), wantLen)
	}
	found := false
	for _, m := range out {
		if m.Role == schema.Tool && strings.Contains(m.Content, "已折叠") {
			found = true
		}
	}
	if !found {
		t.Fatalf("kept tool result should contain collapse notice")
	}
	asstCount := 0
	for _, m := range out {
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 && m.ToolCalls[0].Function.Name == "write" {
			asstCount++
		}
	}
	if asstCount != 2 {
		t.Fatalf("should keep 2 write assistant msgs: got %d", asstCount)
	}
}

// 配对完整：折叠后每个保留的 assistant tool_call 都有其 tool result。
func TestDedup_PairIntact(t *testing.T) {
	args := `{"x":1}`
	in := []*schema.Message{
		schema.UserMessage("do"),
		mkAssistantWithTool("read", args, "c1"),
		mkTool("r1", "c1"),
		mkAssistantWithTool("read", args, "c2"),
		mkTool("r2", "c2"),
		mkAssistantWithTool("read", args, "c3"),
		mkTool("r3", "c3"),
		mkAssistantWithTool("read", args, "c4"),
		mkTool("r4", "c4"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)
	toolResults := map[string]bool{}
	for _, m := range out {
		if m.Role == schema.Tool {
			toolResults[m.ToolCallID] = true
		}
	}
	for _, m := range out {
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
			id := m.ToolCalls[0].ID
			if !toolResults[id] {
				t.Fatalf("assistant tool_call %s has no matching tool result after dedup", id)
			}
		}
	}
}

// 多并行 call（一个 assistant 2 个 tool_call）：签名留空，不折叠。
func TestDedup_ParallelNotCollapsed(t *testing.T) {
	in := []*schema.Message{
		schema.UserMessage("do"),
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "read", Arguments: `{"path":"a"}`}},
			{ID: "c2", Type: "function", Function: schema.FunctionCall{Name: "read", Arguments: `{"path":"a"}`}},
		}},
		mkTool("r1", "c1"),
		mkTool("r2", "c2"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)
	if len(out) != len(in) {
		t.Fatalf("parallel multi-call should not collapse: got %d want %d", len(out), len(in))
	}
}

// 中间有 user 消息打断（不紧邻）：不折叠。
func TestDedup_BrokenByUserNotCollapsed(t *testing.T) {
	args := `{"p":1}`
	in := []*schema.Message{
		schema.UserMessage("do"),
		mkAssistantWithTool("read", args, "c1"),
		mkTool("r1", "c1"),
		schema.UserMessage("again"), // 打断紧邻
		mkAssistantWithTool("read", args, "c2"),
		mkTool("r2", "c2"),
		schema.UserMessage("again2"), // 打断紧邻
		mkAssistantWithTool("read", args, "c3"),
		mkTool("r3", "c3"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)
	if len(out) != len(in) {
		t.Fatalf("user-broken repeats should not collapse: got %d want %d", len(out), len(in))
	}
}

// 幂等：对已折叠结果再跑，条数不变、不重复加提示。
func TestDedup_Idempotent(t *testing.T) {
	args := `{"m":1}`
	in := []*schema.Message{
		schema.UserMessage("do"),
		mkAssistantWithTool("write", args, "c1"),
		mkTool("s1", "c1"),
		mkAssistantWithTool("write", args, "c2"),
		mkTool("s2", "c2"),
		mkAssistantWithTool("write", args, "c3"),
		mkTool("s3", "c3"),
		mkAssistantWithTool("write", args, "c4"),
		mkTool("s4", "c4"),
	}
	once := dedupRepetitiveToolCalls(context.TODO(), in)
	twice := dedupRepetitiveToolCalls(context.TODO(), once)
	if len(twice) != len(once) {
		t.Fatalf("dedup not idempotent: once=%d twice=%d", len(once), len(twice))
	}
	noticeCount := 0
	for _, m := range twice {
		if strings.Contains(m.Content, "已折叠") {
			noticeCount++
		}
	}
	if noticeCount != 1 {
		t.Fatalf("notice should appear exactly once after re-run: got %d", noticeCount)
	}
}

// args key 顺序不同但内容相同应判为相同（normalizeArgsKeyOrder）。
func TestDedup_NormalizedArgs(t *testing.T) {
	in := []*schema.Message{
		schema.UserMessage("do"),
		mkAssistantWithTool("read", `{"path":"a","n":1}`, "c1"),
		mkTool("r1", "c1"),
		mkAssistantWithTool("read", `{"n":1,"path":"a"}`, "c2"), // key 顺序不同
		mkTool("r2", "c2"),
		mkAssistantWithTool("read", `{"path":"a","n":1}`, "c3"),
		mkTool("r3", "c3"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)
	if len(out) != len(in)-2 {
		t.Fatalf("normalized-equal args should collapse: got %d want %d", len(out), len(in)-2)
	}
}

// 多段不同工具的连续重复各自折叠。
func TestDedup_MultipleSegments(t *testing.T) {
	in := []*schema.Message{
		schema.UserMessage("do"),
		mkAssistantWithTool("read", `{"p":1}`, "r1"), mkTool("x", "r1"),
		mkAssistantWithTool("read", `{"p":1}`, "r2"), mkTool("x", "r2"),
		mkAssistantWithTool("read", `{"p":1}`, "r3"), mkTool("x", "r3"),
		mkAssistantWithTool("write", `{"p":2}`, "w1"), mkTool("y", "w1"),
		mkAssistantWithTool("write", `{"p":2}`, "w2"), mkTool("y", "w2"),
		mkAssistantWithTool("write", `{"p":2}`, "w3"), mkTool("y", "w3"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)
	if len(out) != len(in)-4 {
		t.Fatalf("two segments each collapse 1 pair: got %d want %d", len(out), len(in)-4)
	}
}

// notice 加在保留轮（最后 keepLast 个）的首个 tool result，而非被删除的轮。
func TestDedup_NoticeOnKeptRound(t *testing.T) {
	args := `{"p":1}`
	in := []*schema.Message{
		schema.UserMessage("do"),
		mkAssistantWithTool("write", args, "c1"), mkTool("old1", "c1"),
		mkAssistantWithTool("write", args, "c2"), mkTool("old2", "c2"),
		mkAssistantWithTool("write", args, "c3"), mkTool("old3", "c3"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)
	for _, m := range out {
		if m.Role == schema.Tool && strings.Contains(m.Content, "已折叠") {
			if !strings.Contains(m.Content, "old2") {
				t.Fatalf("notice should be on kept round c2 (old2), got content: %s", m.Content)
			}
			return
		}
	}
	t.Fatal("no collapse notice found on kept round")
}

// 折叠后 tool_call / tool_result 严格成对：每个保留的 tool_call.id 恰好对应 1 个 tool result，
// 反之亦然——无孤儿 tool_call、无孤儿 tool result（避免触发"工具不成对"的 API 报错）。
func TestDedup_StrictPairing(t *testing.T) {
	args := `{"p":1}`
	in := []*schema.Message{
		schema.UserMessage("do"),
		mkAssistantWithTool("write", args, "c1"), mkTool("s1", "c1"),
		mkAssistantWithTool("write", args, "c2"), mkTool("s2", "c2"),
		mkAssistantWithTool("write", args, "c3"), mkTool("s3", "c3"),
		mkAssistantWithTool("write", args, "c4"), mkTool("s4", "c4"),
	}
	out := dedupRepetitiveToolCalls(context.TODO(), in)

	callIDs := map[string]int{}
	resultIDs := map[string]int{}
	for _, m := range out {
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				callIDs[tc.ID]++
			}
		}
		if m.Role == schema.Tool {
			resultIDs[m.ToolCallID]++
		}
	}
	for id, n := range callIDs {
		if n != 1 {
			t.Fatalf("tool_call %s appears %d× in assistant (expect 1)", id, n)
		}
		if resultIDs[id] != 1 {
			t.Fatalf("tool_call %s has %d tool result(s) — orphan tool_call", id, resultIDs[id])
		}
	}
	for id, n := range resultIDs {
		if n != 1 || callIDs[id] != 1 {
			t.Fatalf("tool result %s not strictly paired (result=%d call=%d) — orphan tool result", id, n, callIDs[id])
		}
	}
}
