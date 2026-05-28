package builtin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/session"
	imageutil "github.com/rulego/rulego-components-ai/utils/image"
)

// TestFilterRecentToolCalls 测试工具调用过滤函数
func TestFilterRecentToolCalls(t *testing.T) {
	// 辅助函数：创建消息
	createMsg := func(role string, content string, toolCalls []session.ToolCallInfo) *session.SessionMessage {
		return &session.SessionMessage{
			Role:      role,
			Content:   content,
			ToolCalls: toolCalls,
		}
	}

	// 辅助函数：创建工具调用信息
	createToolCall := func(id, name string) session.ToolCallInfo {
		return session.ToolCallInfo{
			ID:   id,
			Name: name,
		}
	}

	tests := []struct {
		name      string
		msgs      []*session.SessionMessage
		keepCount int
		wantLen   int
		wantDesc  string // 描述期望的结果
	}{
		{
			name:      "空消息列表",
			msgs:      []*session.SessionMessage{},
			keepCount: 5,
			wantLen:   0,
			wantDesc:  "空列表应返回空",
		},
		{
			name:      "keepCount为0，不过滤",
			msgs:      []*session.SessionMessage{createMsg("user", "hello", nil)},
			keepCount: 0,
			wantLen:   1,
			wantDesc:  "keepCount为0应不过滤",
		},
		{
			name:      "keepCount为负数，不过滤",
			msgs:      []*session.SessionMessage{createMsg("user", "hello", nil)},
			keepCount: -1,
			wantLen:   1,
			wantDesc:  "keepCount为负数应不过滤",
		},
		{
			name: "只有用户消息，不过滤",
			msgs: []*session.SessionMessage{
				createMsg("user", "hello", nil),
				createMsg("user", "world", nil),
				createMsg("user", "test", nil),
			},
			keepCount: 1,
			wantLen:   3,
			wantDesc:  "纯用户消息应全部保留",
		},
		{
			name: "工具调用组数少于keepCount，不过滤",
			msgs: []*session.SessionMessage{
				createMsg("user", "question1", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("call1", "tool1")}),
				createMsg(string(schema.Tool), "result1", nil),
				createMsg("user", "question2", nil),
			},
			keepCount: 5,
			wantLen:   4,
			wantDesc:  "工具调用组数少于keepCount应不过滤",
		},
		{
			name: "工具调用组数等于keepCount，不过滤",
			msgs: []*session.SessionMessage{
				createMsg("user", "q1", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c1", "t1")}),
				createMsg(string(schema.Tool), "r1", nil),
				createMsg("user", "q2", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c2", "t2")}),
				createMsg(string(schema.Tool), "r2", nil),
			},
			keepCount: 2,
			wantLen:   6,
			wantDesc:  "工具调用组数等于keepCount应不过滤",
		},
		{
			name: "保留最近1组工具调用",
			msgs: []*session.SessionMessage{
				createMsg("user", "q1", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c1", "t1")}), // 组1开始
				createMsg(string(schema.Tool), "r1", nil),
				createMsg("user", "q2", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c2", "t2")}), // 组2开始
				createMsg(string(schema.Tool), "r2", nil),
				createMsg("user", "q3", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c3", "t3")}), // 组3开始
				createMsg(string(schema.Tool), "r3", nil),
			},
			keepCount: 1,
			wantLen:   5, // 3 user + 1 assistant(toolcalls 组3) + 1 tool(组3) = 5, 组1和组2被过滤
			wantDesc:  "应保留最近1组工具调用和所有非工具调用消息",
		},
		{
			name: "保留最近2组工具调用",
			msgs: []*session.SessionMessage{
				createMsg("user", "q1", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c1", "t1")}), // 组1
				createMsg(string(schema.Tool), "r1", nil),
				createMsg("user", "q2", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c2", "t2")}), // 组2
				createMsg(string(schema.Tool), "r2", nil),
				createMsg("user", "q3", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c3", "t3")}), // 组3
				createMsg(string(schema.Tool), "r3", nil),
			},
			keepCount: 2,
			wantLen:   7, // 3 user + 2 assistant(toolcalls 组2,组3) + 2 tool(组2,组3) = 7, 组1被过滤
			wantDesc:  "应保留最近2组工具调用",
		},
		{
			name: "一组工具调用包含多个工具结果",
			msgs: []*session.SessionMessage{
				createMsg("user", "q1", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c1", "t1")}), // 组1：3个工具结果
				createMsg(string(schema.Tool), "r1a", nil),
				createMsg(string(schema.Tool), "r1b", nil),
				createMsg(string(schema.Tool), "r1c", nil),
				createMsg("user", "q2", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c2", "t2")}), // 组2：2个工具结果
				createMsg(string(schema.Tool), "r2a", nil),
				createMsg(string(schema.Tool), "r2b", nil),
			},
			keepCount: 1,
			wantLen:   5, // 2 user + 1 assistant(toolcalls 组2) + 2 tool results(组2) = 5, 组1被过滤
			wantDesc:  "应正确处理一组包含多个工具结果的情况",
		},
		{
			name: "普通助手消息不被过滤",
			msgs: []*session.SessionMessage{
				createMsg("user", "q1", nil),
				createMsg(string(schema.Assistant), "answer1", nil), // 普通助手消息
				createMsg("user", "q2", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c1", "t1")}), // 组1
				createMsg(string(schema.Tool), "r1", nil),
				createMsg("user", "q3", nil),
				createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("c2", "t2")}), // 组2
				createMsg(string(schema.Tool), "r2", nil),
			},
			keepCount: 1,
			wantLen:   6, // 3 user + 1 assistant(normal) + 1 assistant(toolcalls) + 1 tool = 6
			wantDesc:  "普通助手消息应全部保留",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterRecentToolCalls(tt.msgs, tt.keepCount)
			if len(got) != tt.wantLen {
				t.Errorf("filterRecentToolCalls() returned %d messages, want %d. %s", len(got), tt.wantLen, tt.wantDesc)
				// 打印详细信息帮助调试
				t.Logf("Input messages:")
				for i, m := range tt.msgs {
					t.Logf("  [%d] role=%s, toolCalls=%d", i, m.Role, len(m.ToolCalls))
				}
				t.Logf("Output messages:")
				for i, m := range got {
					t.Logf("  [%d] role=%s, toolCalls=%d", i, m.Role, len(m.ToolCalls))
				}
			}
		})
	}
}

// TestFilterRecentToolCalls_GroupIntegrity 测试工具调用组的完整性
func TestFilterRecentToolCalls_GroupIntegrity(t *testing.T) {
	createMsg := func(role string, content string, toolCalls []session.ToolCallInfo) *session.SessionMessage {
		return &session.SessionMessage{
			Role:      role,
			Content:   content,
			ToolCalls: toolCalls,
		}
	}
	createToolCall := func(id, name string) session.ToolCallInfo {
		return session.ToolCallInfo{ID: id, Name: name}
	}

	// 场景：3组工具调用，保留最近1组
	// 验证：保留的组中，assistant消息和其对应的tool结果消息都应存在
	msgs := []*session.SessionMessage{
		createMsg("user", "q1", nil),
		createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("call_1", "read_file")}), // 组1
		createMsg(string(schema.Tool), "file content 1", nil),
		createMsg("user", "q2", nil),
		createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("call_2", "read_file")}), // 组2
		createMsg(string(schema.Tool), "file content 2", nil),
		createMsg("user", "q3", nil),
		createMsg(string(schema.Assistant), "", []session.ToolCallInfo{createToolCall("call_3", "read_file")}), // 组3（应保留）
		createMsg(string(schema.Tool), "file content 3", nil),
	}

	result := filterRecentToolCalls(msgs, 1)

	// 验证结果
	// 应包含：user q1, user q2, user q3, assistant(call_3), tool(result 3)
	// 不应包含：assistant(call_1), tool(result 1), assistant(call_2), tool(result 2)

	// 检查用户消息全部保留
	userCount := 0
	for _, m := range result {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 3 {
		t.Errorf("Expected 3 user messages, got %d", userCount)
	}

	// 检查只保留了一组工具调用（1 assistant with toolcalls + 1 tool result）
	assistantWithToolCallsCount := 0
	toolResultCount := 0
	for _, m := range result {
		if m.Role == string(schema.Assistant) && len(m.ToolCalls) > 0 {
			assistantWithToolCallsCount++
			// 验证是最近的一组（call_3）
			if m.ToolCalls[0].ID != "call_3" {
				t.Errorf("Expected tool call ID 'call_3', got '%s'", m.ToolCalls[0].ID)
			}
		}
		if m.Role == string(schema.Tool) {
			toolResultCount++
		}
	}
	if assistantWithToolCallsCount != 1 {
		t.Errorf("Expected 1 assistant with tool calls, got %d", assistantWithToolCallsCount)
	}
	if toolResultCount != 1 {
		t.Errorf("Expected 1 tool result, got %d", toolResultCount)
	}
}

// TestFilterRecentToolCalls_MultipleToolsInOneCall 测试一次调用多个工具的场景
func TestFilterRecentToolCalls_MultipleToolsInOneCall(t *testing.T) {
	createMsg := func(role string, content string, toolCalls []session.ToolCallInfo) *session.SessionMessage {
		return &session.SessionMessage{
			Role:      role,
			Content:   content,
			ToolCalls: toolCalls,
		}
	}
	createToolCalls := func(count int) []session.ToolCallInfo {
		var calls []session.ToolCallInfo
		for i := 0; i < count; i++ {
			calls = append(calls, session.ToolCallInfo{
				ID:   string(rune('a' + i)),
				Name: "tool_" + string(rune('a'+i)),
			})
		}
		return calls
	}

	// 场景：一次助手响应调用3个工具，产生3个工具结果
	// 这应该算作1组工具调用
	msgs := []*session.SessionMessage{
		createMsg("user", "请帮我读取三个文件", nil),
		createMsg(string(schema.Assistant), "", createToolCalls(3)), // 1组，3个工具调用
		createMsg(string(schema.Tool), "result a", nil),
		createMsg(string(schema.Tool), "result b", nil),
		createMsg(string(schema.Tool), "result c", nil),
	}

	// keepCount = 1，应该保留所有（因为只有1组）
	result := filterRecentToolCalls(msgs, 1)
	if len(result) != 5 {
		t.Errorf("Expected 5 messages (1 group), got %d", len(result))
	}

	// 添加更多组，验证过滤
	msgs2 := []*session.SessionMessage{
		createMsg("user", "q1", nil),
		createMsg(string(schema.Assistant), "", createToolCalls(3)), // 组1
		createMsg(string(schema.Tool), "r1a", nil),
		createMsg(string(schema.Tool), "r1b", nil),
		createMsg(string(schema.Tool), "r1c", nil),
		createMsg("user", "q2", nil),
		createMsg(string(schema.Assistant), "", createToolCalls(2)), // 组2
		createMsg(string(schema.Tool), "r2a", nil),
		createMsg(string(schema.Tool), "r2b", nil),
	}

	// keepCount = 1，应保留：2 user + 1 assistant(组2) + 2 tool(组2) = 5
	result2 := filterRecentToolCalls(msgs2, 1)
	if len(result2) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(result2))
	}
}

func TestSessionAspectAfterSkipsInvalidToolCalls(t *testing.T) {
	ctx := context.Background()
	storage := session.NewMemoryStorage()
	manager := session.NewManager(storage, nil)
	req := session.SessionRequest{
		AgentID: "agent-1",
		Channel: "feishu",
		Scope:   session.ScopePerPeer,
		ScopeID: "peer-1",
		UserID:  "user-1",
	}
	ctx, sess, err := session.NewSessionContext(ctx, manager, req)
	if err != nil {
		t.Fatalf("NewSessionContext() error = %v", err)
	}

	aspectInstance := NewSessionAspect(manager, session.ScopePerPeer, nil)
	point := &aspect.AgentPoint{
		AgentId:  req.AgentID,
		ThreadId: req.ScopeID,
		UserId:   req.UserID,
	}
	output := &aspect.AgentOutput{
		SessionKey: sess.Key,
		OriginalMessages: []*schema.Message{
			{
				Role:    schema.User,
				Content: "继续调查",
			},
		},
		ToolCalls: []aspect.ToolCallResult{
			{
				CallId:    "call-empty-bash",
				Name:      "bash",
				Arguments: "",
				Result:    "Error: Invalid parameters - command cannot be empty",
			},
			{
				CallId:    "call-valid-bash",
				Name:      "bash",
				Arguments: "{\"command\":\"pwd\"}",
				Result:    "d:\\github\\rulego-project",
			},
			{
				CallId:    "call-empty-skill",
				Name:      "skill",
				Arguments: "   ",
				Result:    "failed to get skill: skill not found",
			},
		},
	}

	if _, err := aspectInstance.After(ctx, point, output); err != nil {
		t.Fatalf("After() error = %v", err)
	}

	history, err := manager.GetHistory(ctx, sess.Key, 10)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	// After() no longer saves user messages (moved to Before()), only assistant + tool messages
	if len(history) != 2 {
		t.Fatalf("expected 2 messages in history, got %d", len(history))
	}
	if history[0].Role != string(schema.Assistant) {
		t.Fatalf("expected first message role %q, got %q", string(schema.Assistant), history[0].Role)
	}
	if len(history[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 valid tool call to be saved, got %d", len(history[0].ToolCalls))
	}
	if history[0].ToolCalls[0].ID != "call-valid-bash" {
		t.Fatalf("expected saved tool call ID %q, got %q", "call-valid-bash", history[0].ToolCalls[0].ID)
	}
	if history[0].ToolCalls[0].Arguments != "{\"command\":\"pwd\"}" {
		t.Fatalf("expected saved tool arguments %q, got %q", "{\"command\":\"pwd\"}", history[0].ToolCalls[0].Arguments)
	}
	if history[1].Role != string(schema.Tool) {
		t.Fatalf("expected second message role %q, got %q", string(schema.Tool), history[1].Role)
	}
	if history[1].ToolCallID != "call-valid-bash" {
		t.Fatalf("expected tool result to reference %q, got %q", "call-valid-bash", history[1].ToolCallID)
	}
}

func TestSessionAspectAfterSkipsAssistantToolMessageWhenAllToolCallsInvalid(t *testing.T) {
	ctx := context.Background()
	storage := session.NewMemoryStorage()
	manager := session.NewManager(storage, nil)
	req := session.SessionRequest{
		AgentID: "agent-2",
		Channel: "feishu",
		Scope:   session.ScopePerPeer,
		ScopeID: "peer-2",
		UserID:  "user-2",
	}
	ctx, sess, err := session.NewSessionContext(ctx, manager, req)
	if err != nil {
		t.Fatalf("NewSessionContext() error = %v", err)
	}

	aspectInstance := NewSessionAspect(manager, session.ScopePerPeer, nil)
	point := &aspect.AgentPoint{
		AgentId:  req.AgentID,
		ThreadId: req.ScopeID,
		UserId:   req.UserID,
	}
	output := &aspect.AgentOutput{
		SessionKey: sess.Key,
		OriginalMessages: []*schema.Message{
			{
				Role:    schema.User,
				Content: "继续",
			},
		},
		ToolCalls: []aspect.ToolCallResult{
			{
				CallId:    "call-empty-1",
				Name:      "bash",
				Arguments: "",
				Result:    "Error: Invalid parameters - command cannot be empty",
			},
			{
				CallId:    "call-empty-2",
				Name:      "skill",
				Arguments: " ",
				Result:    "failed to get skill: skill not found",
			},
		},
	}

	if _, err := aspectInstance.After(ctx, point, output); err != nil {
		t.Fatalf("After() error = %v", err)
	}

	history, err := manager.GetHistory(ctx, sess.Key, 10)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	// After() no longer saves user messages (moved to Before()), and all tool calls are invalid,
	// so no messages should be saved
	if len(history) != 0 {
		t.Fatalf("expected 0 messages (user saved by Before, tool calls all invalid), got %d messages", len(history))
	}
}

// TestSessionAspectAfterSkipsEmptyJSONObjectToolCalls 测试 SessionAspect 会跳过空对象参数工具调用。
func TestSessionAspectAfterSkipsEmptyJSONObjectToolCalls(t *testing.T) {
	ctx := context.Background()
	storage := session.NewMemoryStorage()
	manager := session.NewManager(storage, nil)
	req := session.SessionRequest{
		AgentID: "agent-3",
		Channel: "feishu",
		Scope:   session.ScopePerPeer,
		ScopeID: "peer-3",
		UserID:  "user-3",
	}
	ctx, sess, err := session.NewSessionContext(ctx, manager, req)
	if err != nil {
		t.Fatalf("NewSessionContext() error = %v", err)
	}

	aspectInstance := NewSessionAspect(manager, session.ScopePerPeer, nil)
	point := &aspect.AgentPoint{
		AgentId:  req.AgentID,
		ThreadId: req.ScopeID,
		UserId:   req.UserID,
	}
	output := &aspect.AgentOutput{
		SessionKey: sess.Key,
		OriginalMessages: []*schema.Message{
			{
				Role:    schema.User,
				Content: "继续排查",
			},
		},
		ToolCalls: []aspect.ToolCallResult{
			{
				CallId:    "call-empty-json-bash",
				Name:      "bash",
				Arguments: "{}",
				Result:    "Error: Invalid parameters - command cannot be empty",
			},
			{
				CallId:    "call-valid-bash",
				Name:      "bash",
				Arguments: "{\"command\":\"pwd\"}",
				Result:    "d:\\github\\rulego-project",
			},
		},
	}

	if _, err := aspectInstance.After(ctx, point, output); err != nil {
		t.Fatalf("After() error = %v", err)
	}

	history, err := manager.GetHistory(ctx, sess.Key, 10)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	// After() no longer saves user messages (moved to Before()), only assistant + tool messages
	if len(history) != 2 {
		t.Fatalf("expected 2 messages in history, got %d", len(history))
	}
	if history[0].Role != string(schema.Assistant) {
		t.Fatalf("expected first message role %q, got %q", string(schema.Assistant), history[0].Role)
	}
	if len(history[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 valid tool call to be saved, got %d", len(history[0].ToolCalls))
	}
	if history[0].ToolCalls[0].ID != "call-valid-bash" {
		t.Fatalf("expected saved tool call ID %q, got %q", "call-valid-bash", history[0].ToolCalls[0].ID)
	}
}

// TestSessionAspectAfterSkipsToolCallsMissingRequiredFields 测试 SessionAspect 会跳过缺少必填字段的工具调用。
func TestSessionAspectAfterSkipsToolCallsMissingRequiredFields(t *testing.T) {
	ctx := context.Background()
	storage := session.NewMemoryStorage()
	manager := session.NewManager(storage, nil)
	req := session.SessionRequest{
		AgentID: "agent-4",
		Channel: "feishu",
		Scope:   session.ScopePerPeer,
		ScopeID: "peer-4",
		UserID:  "user-4",
	}
	ctx, sess, err := session.NewSessionContext(ctx, manager, req)
	if err != nil {
		t.Fatalf("NewSessionContext() error = %v", err)
	}

	aspectInstance := NewSessionAspect(manager, session.ScopePerPeer, nil)
	point := &aspect.AgentPoint{
		AgentId:  req.AgentID,
		ThreadId: req.ScopeID,
		UserId:   req.UserID,
	}
	output := &aspect.AgentOutput{
		SessionKey: sess.Key,
		OriginalMessages: []*schema.Message{
			{
				Role:    schema.User,
				Content: "继续排查",
			},
		},
		ToolCalls: []aspect.ToolCallResult{
			{
				CallId:    "call-missing-command",
				Name:      "bash",
				Arguments: "{\"args\":[\"pwd\"]}",
				Result:    "Error: Invalid parameters - command cannot be empty",
			},
			{
				CallId:    "call-missing-skill-name",
				Name:      "skill",
				Arguments: "{\"path\":\"demo-skill\"}",
				Result:    "failed to get skill: skill not found",
			},
		},
	}

	if _, err := aspectInstance.After(ctx, point, output); err != nil {
		t.Fatalf("After() error = %v", err)
	}

	history, err := manager.GetHistory(ctx, sess.Key, 10)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	// After() no longer saves user messages (moved to Before()), and all tool calls are invalid,
	// so no messages should be saved
	if len(history) != 0 {
		t.Fatalf("expected 0 messages (user saved by Before, tool calls all invalid), got %d messages", len(history))
	}
}

// TestConvertSessionMessagesToSchema_SkipsUnpairedToolCallHistory 测试恢复历史时会跳过不成对的工具调用消息。
func TestConvertSessionMessagesToSchema_SkipsUnpairedToolCallHistory(t *testing.T) {
	msgs := []*session.SessionMessage{
		{
			Role:    string(schema.User),
			Content: "先执行工具",
		},
		{
			Role:    string(schema.Assistant),
			Content: "",
			ToolCalls: []session.ToolCallInfo{
				{ID: "call-1", Name: "bash", Arguments: "{\"command\":\"pwd\"}"},
				{ID: "call-2", Name: "bash", Arguments: "{\"command\":\"ls\"}"},
			},
		},
		{
			Role:       string(schema.Tool),
			Content:    "d:\\github\\rulego-project",
			ToolCallID: "call-1",
		},
		{
			Role:       string(schema.Tool),
			Content:    "孤立工具结果",
			ToolCallID: "call-orphan",
		},
		{
			Role:    string(schema.Assistant),
			Content: "",
			ToolCalls: []session.ToolCallInfo{
				{ID: "call-3", Name: "bash", Arguments: "{\"command\":\"git status\"}"},
			},
		},
		{
			Role:       string(schema.Tool),
			Content:    "On branch main",
			ToolCallID: "call-3",
		},
		{
			Role:    string(schema.User),
			Content: "继续总结",
		},
	}

	result := convertSessionMessagesToSchema(msgs)
	if len(result) != 4 {
		t.Fatalf("expected 4 schema messages after dropping unpaired tool call history, got %d", len(result))
	}
	if result[0].Role != schema.User {
		t.Fatalf("expected first message role %q, got %q", schema.User, result[0].Role)
	}
	if result[1].Role != schema.Assistant {
		t.Fatalf("expected second message role %q, got %q", schema.Assistant, result[1].Role)
	}
	if len(result[1].ToolCalls) != 1 || result[1].ToolCalls[0].ID != "call-3" {
		t.Fatalf("expected only paired assistant tool call call-3 to be restored, got %+v", result[1].ToolCalls)
	}
	if result[2].Role != schema.Tool || result[2].ToolCallID != "call-3" {
		t.Fatalf("expected paired tool result for call-3, got role=%q toolCallID=%q", result[2].Role, result[2].ToolCallID)
	}
	if result[3].Role != schema.User {
		t.Fatalf("expected fourth message role %q, got %q", schema.User, result[3].Role)
	}
}

// TestEstimateTokenCount 测试 token 估算函数
func TestEstimateTokenCount(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		minToken int // 估算结果应 >= minToken
		maxToken int // 估算结果应 <= maxToken
	}{
		{
			name:     "空字符串",
			text:     "",
			minToken: 0,
			maxToken: 0,
		},
		{
			name:     "纯英文短句",
			text:     "hello world",
			minToken: 1,
			maxToken: 5,
		},
		{
			name:     "纯中文短句",
			text:     "你好世界",
			minToken: 1,
			maxToken: 6,
		},
		{
			name:     "中英混合",
			text:     "你好hello世界world",
			minToken: 1,
			maxToken: 10,
		},
		{
			name:     "单个字符",
			text:     "a",
			minToken: 1,
			maxToken: 1,
		},
		{
			name:     "单个中文字符",
			text:     "你",
			minToken: 1,
			maxToken: 1,
		},
		{
			name:     "长英文文本",
			text:     "This is a longer English text for testing token estimation accuracy",
			minToken: 5,
			maxToken: 25,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTokenCount(tt.text)
			if got < tt.minToken || got > tt.maxToken {
				t.Errorf("estimateTokenCount(%q) = %d, want between %d and %d", tt.text, got, tt.minToken, tt.maxToken)
			}
		})
	}
}

// TestStripImagePathMarkers 测试清理图片路径标记
func TestStripImagePathMarkers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "无标记",
			input: "这是一条普通消息",
			want:  "这是一条普通消息",
		},
		{
			name:  "单个标记在末尾",
			input: "看看这张图[图片：/path/to/image.jpg]",
			want:  "看看这张图",
		},
		{
			name:  "单个标记带换行",
			input: "看看这张图\n[图片：/path/to/image.jpg]\n",
			want:  "看看这张图",
		},
		{
			name:  "多个标记",
			input: "[图片：a.jpg]\n看看这张图[图片：b.png]\n还有这个",
			want:  "看看这张图还有这个",
		},
		{
			name:  "只有标记无其他内容",
			input: "[图片：/path/to/image.jpg]",
			want:  "",
		},
		{
			name:  "标记在开头",
			input: "[图片：a.jpg]这是一条消息",
			want:  "这是一条消息",
		},
		{
			name:  "空字符串",
			input: "",
			want:  "",
		},
		{
			name:  "仅有空格和标记",
			input: "  [图片：a.jpg]  ",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripImagePathMarkers(tt.input)
			if got != tt.want {
				t.Errorf("stripImagePathMarkers(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestFilterImageURLs 测试图片 URL 过滤
func TestFilterImageURLs(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
		wantN int // 期望结果数量
	}{
		{
			name:  "空列表",
			input: nil,
			wantN: 0,
		},
		{
			name:  "全部有效-HTTP和本地路径",
			input: []string{"http://example.com/a.jpg", "https://example.com/b.png", "/tmp/test.png"},
			wantN: 3,
		},
		{
			name:  "过滤无效路径",
			input: []string{"http://example.com/a.jpg", "invalid-path", "/tmp/test.png"},
			wantN: 2,
		},
		{
			name:  "base64格式",
			input: []string{"data:image/png;base64,iVBORw0KGgo="},
			wantN: 1,
		},
		{
			name:  "全部无效",
			input: []string{"no-extension", "also-invalid"},
			wantN: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterImageURLs(tt.input)
			if len(got) != tt.wantN {
				t.Errorf("filterImageURLs() returned %d results, want %d; got=%v", len(got), tt.wantN, got)
			}
		})
	}
}

// TestConvertSessionMessagesToSchema_WithImages 测试带图片的历史消息转换
func TestConvertSessionMessagesToSchema_WithImages(t *testing.T) {
	t.Run("用户消息有图片字段-有文本内容", func(t *testing.T) {
		msgs := []*session.SessionMessage{
			{
				Role:    string(schema.User),
				Content: "看看这张图",
				Images:  []string{"data:image/png;base64,iVBORw0KGgo="},
			},
		}
		result := convertSessionMessagesToSchema(msgs)
		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
		// 应该生成 UserInputMultiContent（图片 + 文本）
		if len(result[0].UserInputMultiContent) != 2 {
			t.Fatalf("expected 2 parts in UserInputMultiContent, got %d", len(result[0].UserInputMultiContent))
		}
		// 第一部分应该是图片
		if result[0].UserInputMultiContent[0].Type != schema.ChatMessagePartTypeImageURL {
			t.Fatalf("expected first part type %q, got %q", schema.ChatMessagePartTypeImageURL, result[0].UserInputMultiContent[0].Type)
		}
		// 第二部分应该是文本
		if result[0].UserInputMultiContent[1].Type != schema.ChatMessagePartTypeText {
			t.Fatalf("expected second part type %q, got %q", schema.ChatMessagePartTypeText, result[0].UserInputMultiContent[1].Type)
		}
		if result[0].UserInputMultiContent[1].Text != "看看这张图" {
			t.Fatalf("expected text %q, got %q", "看看这张图", result[0].UserInputMultiContent[1].Text)
		}
	})

	t.Run("用户消息有图片字段-无文本内容", func(t *testing.T) {
		msgs := []*session.SessionMessage{
			{
				Role:    string(schema.User),
				Content: "", // 空文本
				Images:  []string{"data:image/png;base64,iVBORw0KGgo="},
			},
		}
		result := convertSessionMessagesToSchema(msgs)
		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
		// 只有图片 part，不添加空文本 part（这是修复空 text part 导致 LLM 返回空的关键逻辑）
		if len(result[0].UserInputMultiContent) != 1 {
			t.Fatalf("expected 1 part (image only, no empty text), got %d", len(result[0].UserInputMultiContent))
		}
		if result[0].UserInputMultiContent[0].Type != schema.ChatMessagePartTypeImageURL {
			t.Fatalf("expected image part, got type %q", result[0].UserInputMultiContent[0].Type)
		}
	})

	t.Run("用户消息无图片字段", func(t *testing.T) {
		msgs := []*session.SessionMessage{
			{
				Role:    string(schema.User),
				Content: "纯文本消息",
			},
		}
		result := convertSessionMessagesToSchema(msgs)
		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
		// 没有 UserInputMultiContent
		if len(result[0].UserInputMultiContent) != 0 {
			t.Fatalf("expected 0 parts, got %d", len(result[0].UserInputMultiContent))
		}
		if result[0].Content != "纯文本消息" {
			t.Fatalf("expected content %q, got %q", "纯文本消息", result[0].Content)
		}
	})

	t.Run("多张图片", func(t *testing.T) {
		msgs := []*session.SessionMessage{
			{
				Role:    string(schema.User),
				Content: "对比这两张图",
				Images:  []string{"data:image/png;base64,aaa=", "data:image/jpeg;base64,bbb="},
			},
		}
		result := convertSessionMessagesToSchema(msgs)
		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
		// 2 图片 + 1 文本 = 3 parts
		if len(result[0].UserInputMultiContent) != 3 {
			t.Fatalf("expected 3 parts, got %d", len(result[0].UserInputMultiContent))
		}
		imgCount := 0
		textCount := 0
		for _, part := range result[0].UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeImageURL {
				imgCount++
			} else if part.Type == schema.ChatMessagePartTypeText {
				textCount++
			}
		}
		if imgCount != 2 || textCount != 1 {
			t.Fatalf("expected 2 image + 1 text parts, got %d image + %d text", imgCount, textCount)
		}
	})
}

// TestConvertRecentHistoryImagesToBase64 测试历史图片转换
func TestConvertRecentHistoryImagesToBase64(t *testing.T) {
	t.Run("空历史", func(t *testing.T) {
		converted, total := convertRecentHistoryImagesToBase64(nil)
		if converted != 0 || total != 0 {
			t.Fatalf("expected (0,0), got (%d,%d)", converted, total)
		}
	})

	t.Run("无多模态内容", func(t *testing.T) {
		history := []*schema.Message{
			{Role: schema.User, Content: "纯文本消息"},
			{Role: schema.Assistant, Content: "回复"},
		}
		converted, total := convertRecentHistoryImagesToBase64(history)
		if converted != 0 || total != 0 {
			t.Fatalf("expected (0,0), got (%d,%d)", converted, total)
		}
	})

	t.Run("本地文件路径图片被降级为纯文本", func(t *testing.T) {
		localPath := "/tmp/test_image_does_not_exist.png"
		urlStr := localPath
		history := []*schema.Message{
			{
				Role: schema.User,
				UserInputMultiContent: []schema.MessageInputPart{
					{
						Type: schema.ChatMessagePartTypeText,
						Text: "看看这个",
					},
					{
						Type: schema.ChatMessagePartTypeImageURL,
						Image: &schema.MessageInputImage{
							MessagePartCommon: schema.MessagePartCommon{
								URL: &urlStr,
							},
						},
					},
				},
			},
		}

		converted, total := convertRecentHistoryImagesToBase64(history)
		// 文件不存在，转换失败，total=1, converted=0
		if total != 1 {
			t.Fatalf("expected total=1, got %d", total)
		}
		if converted != 0 {
			t.Fatalf("expected converted=0 (file doesn't exist), got %d", converted)
		}
		// 清理：本地文件路径的消息应被降级为纯文本
		if len(history[0].UserInputMultiContent) != 0 {
			t.Fatalf("expected UserInputMultiContent to be nil after cleanup, got %d parts", len(history[0].UserInputMultiContent))
		}
		if history[0].Content != "看看这个\n[图片]" {
			t.Fatalf("expected content %q after cleanup, got %q", "看看这个\n[图片]", history[0].Content)
		}
	})

	t.Run("HTTP URL 图片不被降级", func(t *testing.T) {
		httpURL := "https://example.com/image.png"
		history := []*schema.Message{
			{
				Role: schema.User,
				UserInputMultiContent: []schema.MessageInputPart{
					{
						Type: schema.ChatMessagePartTypeImageURL,
						Image: &schema.MessageInputImage{
							MessagePartCommon: schema.MessagePartCommon{
								URL: &httpURL,
							},
						},
					},
				},
			},
		}

		convertRecentHistoryImagesToBase64(history)
		// HTTP URL 不被清理
		if len(history[0].UserInputMultiContent) == 0 {
			t.Fatal("expected UserInputMultiContent to be preserved for HTTP URL")
		}
	})

	t.Run("base64 图片不被降级", func(t *testing.T) {
		base64Data := "data:image/png;base64,iVBORw0KGgo="
		history := []*schema.Message{
			{
				Role: schema.User,
				UserInputMultiContent: []schema.MessageInputPart{
					{
						Type: schema.ChatMessagePartTypeImageURL,
						Image: &schema.MessageInputImage{
							MessagePartCommon: schema.MessagePartCommon{
								URL: &base64Data,
							},
						},
					},
				},
			},
		}

		convertRecentHistoryImagesToBase64(history)
		// base64 URL 不被清理（不是本地文件路径）
		if len(history[0].UserInputMultiContent) == 0 {
			t.Fatal("expected UserInputMultiContent to be preserved for base64 data")
		}
	})
}

// TestSaveUserMessageBeforeLLM 测试 Before 阶段用户消息预存
func TestSaveUserMessageBeforeLLM(t *testing.T) {
	ctx := context.Background()
	storage := session.NewMemoryStorage()
	manager := session.NewManager(storage, nil)
	req := session.SessionRequest{
		AgentID: "agent-save-user",
		Channel: "feishu",
		Scope:   session.ScopePerPeer,
		ScopeID: "peer-save-user",
		UserID:  "user-1",
	}
	sess, err := manager.GetOrCreate(ctx, req)
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}

	aspectInstance := NewSessionAspect(manager, session.ScopePerPeer, nil)

	t.Run("普通文本消息", func(t *testing.T) {
		input := &aspect.AgentInput{
			Messages: []*schema.Message{
				{
					Role:    schema.User,
					Content: "你好世界",
				},
			},
			Metadata: make(map[string]string),
		}

		aspectInstance.saveUserMessageBeforeLLM(ctx, input, sess.Key)

		history, err := manager.GetHistory(ctx, sess.Key, 10)
		if err != nil {
			t.Fatalf("GetHistory() error = %v", err)
		}

		found := false
		for _, msg := range history {
			if msg.Role == string(schema.User) && msg.Content == "你好世界" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected user message to be saved in history")
		}
	})
}

// TestSaveUserMessageBeforeLLM_WithImages 测试用户消息带图片的保存
func TestSaveUserMessageBeforeLLM_WithImages(t *testing.T) {
	ctx := context.Background()
	storage := session.NewMemoryStorage()
	manager := session.NewManager(storage, nil)
	req := session.SessionRequest{
		AgentID: "agent-save-img",
		Channel: "feishu",
		Scope:   session.ScopePerPeer,
		ScopeID: "peer-save-img",
		UserID:  "user-1",
	}
	sess, err := manager.GetOrCreate(ctx, req)
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}

	aspectInstance := NewSessionAspect(manager, session.ScopePerPeer, nil)

	t.Run("从Extra提取图片路径", func(t *testing.T) {
		images := []string{"/tmp/test.png"}
		imagesJSON, _ := json.Marshal(images)
		input := &aspect.AgentInput{
			Messages: []*schema.Message{
				{
					Role:    schema.User,
					Content: "看看这张图[图片：/tmp/test.png]",
					Extra:   map[string]any{"images": string(imagesJSON)},
				},
			},
			Metadata: make(map[string]string),
		}

		aspectInstance.saveUserMessageBeforeLLM(ctx, input, sess.Key)

		history, err := manager.GetHistory(ctx, sess.Key, 10)
		if err != nil {
			t.Fatalf("GetHistory() error = %v", err)
		}

		found := false
		for _, msg := range history {
			if msg.Role == string(schema.User) {
				found = true
				// stripImagePathMarkers 应该清理掉 [图片：path] 标记
				if msg.Content != "看看这张图" {
					t.Fatalf("expected content %q (markers stripped), got %q", "看看这张图", msg.Content)
				}
				// 图片应从 Extra 提取并保存
				if len(msg.Images) == 0 {
					t.Fatal("expected images to be saved")
				}
				break
			}
		}
		if !found {
			t.Fatal("expected user message to be saved")
		}
	})

	t.Run("从UserInputMultiContent提取图片并提取文本", func(t *testing.T) {
		imgURL := "https://example.com/img.png"
		input := &aspect.AgentInput{
			Messages: []*schema.Message{
				{
					Role:    schema.User,
					Content: "", // 空的 Content
					UserInputMultiContent: []schema.MessageInputPart{
						{
							Type: schema.ChatMessagePartTypeText,
							Text: "描述图片",
						},
						{
							Type: schema.ChatMessagePartTypeImageURL,
							Image: &schema.MessageInputImage{
								MessagePartCommon: schema.MessagePartCommon{
									URL: &imgURL,
								},
							},
						},
					},
				},
			},
			Metadata: make(map[string]string),
		}

		aspectInstance.saveUserMessageBeforeLLM(ctx, input, sess.Key)

		history, err := manager.GetHistory(ctx, sess.Key, 20)
		if err != nil {
			t.Fatalf("GetHistory() error = %v", err)
		}

		// 找最后一条用户消息
		var lastUserMsg *session.SessionMessage
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == string(schema.User) {
				lastUserMsg = history[i]
				break
			}
		}
		if lastUserMsg == nil {
			t.Fatal("expected user message to be saved")
		}
		// 文本应从 UserInputMultiContent 中提取
		if lastUserMsg.Content != "描述图片" {
			t.Fatalf("expected content %q (extracted from multi content), got %q", "描述图片", lastUserMsg.Content)
		}
		// 图片 URL 应被保存
		if len(lastUserMsg.Images) == 0 {
			t.Fatal("expected images to be saved from UserInputMultiContent")
		}
	})
}

// TestBeforeAndAfterIntegration 测试完整的 Before+After 集成流程
func TestBeforeAndAfterIntegration(t *testing.T) {
	ctx := context.Background()
	storage := session.NewMemoryStorage()
	manager := session.NewManager(storage, nil)
	req := session.SessionRequest{
		AgentID: "agent-integration",
		Channel: "feishu",
		Scope:   session.ScopePerPeer,
		ScopeID: "peer-integration",
		UserID:  "user-1",
	}
	ctx, _, err := session.NewSessionContext(ctx, manager, req)
	if err != nil {
		t.Fatalf("NewSessionContext() error = %v", err)
	}

	aspectInstance := NewSessionAspect(manager, session.ScopePerPeer, nil)
	point := &aspect.AgentPoint{
		AgentId:  req.AgentID,
		ThreadId: req.ScopeID,
		UserId:   req.UserID,
	}

	// 1. Before: 加载会话 + 预存用户消息
	input := &aspect.AgentInput{
		Messages: []*schema.Message{
			{
				Role:    schema.User,
				Content: "你好",
			},
		},
		Metadata: map[string]string{
			aspect.MetaLoadHistory: "true",
		},
	}
	inputAfterBefore, err := aspectInstance.Before(ctx, point, input)
	if err != nil {
		t.Fatalf("Before() error = %v", err)
	}
	if inputAfterBefore.SessionKey == "" {
		t.Fatal("expected SessionKey to be set after Before()")
	}

	// 2. After: 保存助手回复和工具调用
	output := &aspect.AgentOutput{
		SessionKey: inputAfterBefore.SessionKey,
		OriginalMessages: []*schema.Message{
			{
				Role:    schema.User,
				Content: "你好",
			},
		},
		Content: "你好！有什么可以帮助你的吗？",
		ToolCalls: []aspect.ToolCallResult{
			{
				CallId:    "call-valid",
				Name:      "search",
				Arguments: "{\"query\":\"test\"}",
				Result:    "search result",
			},
		},
	}
	_, err = aspectInstance.After(ctx, point, output)
	if err != nil {
		t.Fatalf("After() error = %v", err)
	}

	// 3. 验证历史消息完整性
	history, err := manager.GetHistory(ctx, inputAfterBefore.SessionKey, 20)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}

	// 应包含：1 user + 1 assistant(tool call) + 1 tool result + 1 assistant reply = 4
	if len(history) != 4 {
		t.Fatalf("expected 4 messages in history, got %d", len(history))
	}

	if history[0].Role != string(schema.User) {
		t.Fatalf("expected first message role %q, got %q", string(schema.User), history[0].Role)
	}
	if history[0].Content != "你好" {
		t.Fatalf("expected first message content %q, got %q", "你好", history[0].Content)
	}

	if history[1].Role != string(schema.Assistant) {
		t.Fatalf("expected second message role %q, got %q", string(schema.Assistant), history[1].Role)
	}
	if len(history[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(history[1].ToolCalls))
	}

	if history[2].Role != string(schema.Tool) {
		t.Fatalf("expected third message role %q, got %q", string(schema.Tool), history[2].Role)
	}

	if history[3].Role != string(schema.Assistant) {
		t.Fatalf("expected fourth message role %q, got %q", string(schema.Assistant), history[3].Role)
	}
	if history[3].Content != "你好！有什么可以帮助你的吗？" {
		t.Fatalf("expected fourth message content %q, got %q", "你好！有什么可以帮助你的吗？", history[3].Content)
	}
}

// TestIsLocalFilePathFromImageUtil 验证 imageutil.IsLocalFilePath 的行为（确保去重后行为一致）
func TestIsLocalFilePathFromImageUtil(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/tmp/test.png", true},
		{"C:\\Users\\test\\image.jpg", true},
		{"http://example.com/a.jpg", false},
		{"https://example.com/b.png", false},
		{"data:image/png;base64,iVBORw0KGgo=", false},
		{"file:///tmp/test.png", true},
		{"no-extension", false},
		{"path/to/image.webp", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := imageutil.IsLocalFilePath(tt.input)
			if got != tt.want {
				t.Errorf("IsLocalFilePath(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestExtractImagesFromExtra 测试从 Extra 提取图片
func TestExtractImagesFromExtra(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  int
	}{
		{
			name:  "nil extra",
			extra: nil,
			want:  0,
		},
		{
			name:  "无 images 字段",
			extra: map[string]any{"other": "value"},
			want:  0,
		},
		{
			name:  "images 不是字符串",
			extra: map[string]any{"images": 123},
			want:  0,
		},
		{
			name:  "images 不是有效 JSON",
			extra: map[string]any{"images": "not-json"},
			want:  0,
		},
		{
			name: "有效图片列表",
			extra: func() map[string]any {
				images, _ := json.Marshal([]string{"/tmp/a.png", "/tmp/b.jpg"})
				return map[string]any{"images": string(images)}
			}(),
			want: 2,
		},
		{
			name: "空图片列表",
			extra: func() map[string]any {
				images, _ := json.Marshal([]string{})
				return map[string]any{"images": string(images)}
			}(),
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractImagesFromExtra(tt.extra)
			if len(got) != tt.want {
				t.Errorf("extractImagesFromExtra() returned %d images, want %d", len(got), tt.want)
			}
		})
	}
}
