package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

// recordingModel 可控 mock：前 toolCallSeq 次 Generate 返回相同的 echo tool_call，
// 之后返回纯文本结束。记录每次收到的 messages（用于验证 dedup 是否折叠了重复 tool_call）。
type recordingModel struct {
	mu            sync.Mutex
	callCount     int
	received      [][]*schema.Message
	toolCallSeq   int
	callIDCounter int
}

func (m *recordingModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.callCount++
	n := m.callCount
	m.received = append(m.received, msgs)
	m.callIDCounter++
	id := fmt.Sprintf("call-%d", m.callIDCounter)
	seq := m.toolCallSeq
	m.mu.Unlock()

	if n <= seq {
		return &schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       id,
				Type:     "function",
				Function: schema.FunctionCall{Name: "echo", Arguments: `{"msg":"same"}`},
			}},
		}, nil
	}
	return &schema.Message{Role: schema.Assistant, Content: "done"}, nil
}

func (m *recordingModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("stream not implemented in recordingModel")
}

func (m *recordingModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// echoTool 简单回显，记录实际执行次数（验证 doom 是否在第 3 次起拒绝执行）。
type echoTool struct {
	runs int32
}

func (e *echoTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "echo",
		Desc: "echo the message back",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"msg": {Type: "string", Desc: "message to echo", Required: true},
		}),
	}, nil
}

func (e *echoTool) InvokableRun(_ context.Context, args string, _ ...tool.Option) (string, error) {
	atomic.AddInt32(&e.runs, 1)
	return "echo: " + args, nil
}

// maxConsecutiveSameEcho 数 messages 里连续相同 echo tool_call 的最大长度。
// 中间的 tool result 不算打断（与 dedup 的「紧邻」判定一致）。
func maxConsecutiveSameEcho(msgs []*schema.Message) int {
	maxRun, run := 0, 0
	prevArgs := ""
	for _, m := range msgs {
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 && m.ToolCalls[0].Function.Name == "echo" {
			a := normalizeArgsKeyOrder(m.ToolCalls[0].Function.Arguments)
			if a != "" && a == prevArgs {
				run++
			} else {
				run = 1
				prevArgs = a
			}
			if run > maxRun {
				maxRun = run
			}
		} else if m.Role != schema.Tool {
			run = 0
			prevArgs = ""
		}
	}
	return maxRun
}

// 集成测试：dedup（MessageRewriter）在真实 agent 链里折叠连续重复的 tool_call，
// 使发往 model 的 history 不连续堆积同名同参调用（避免触发 provider 死循环护栏）。
func TestIntegration_DedupFoldsRepetitiveToolCalls(t *testing.T) {
	m := &recordingModel{toolCallSeq: 5} // 前 5 次 echo tool_call(相同 args)，第 6 次 done
	echo := &echoTool{}

	agent, err := CreateReactAgent(context.Background(), m, AgentOptions{
		MaxStep:     30,
		ToolsConfig: buildToolsConfig([]tool.BaseTool{echo}),
	})
	require.NoError(t, err)

	_, err = agent.Generate(context.Background(), []*schema.Message{schema.UserMessage("test")})
	require.NoError(t, err)

	// 模型连续输出 5 次相同 echo，dedup 应把发往 model 的历史折叠到 ≤ keepLast(2)
	for i, msgs := range m.received {
		if c := maxConsecutiveSameEcho(msgs); c > 2 {
			t.Fatalf("model call #%d: dedup should fold repetitive echo, found %d consecutive identical", i, c)
		}
	}
	t.Logf("dedup ok: model called %d times, echo actually ran %d times", m.callCount, atomic.LoadInt32(&echo.runs))
}

// 集成测试：doom 在工具层拒绝连续重复调用。用 VisualToolWrapper 包装 echo、ctx 注入 detector，
// 模型连续输出相同 echo 时第 3 次起被拒绝执行（echo 实际执行 ≤2 次）。
// 同时验证 detector 能否通过 ctx 传到工具层——之前静态分析的不确定性在此一锤定音。
func TestIntegration_DoomBlocksRepetitiveToolCalls(t *testing.T) {
	m := &recordingModel{toolCallSeq: 5}
	echo := &echoTool{}

	wrapped := NewVisualToolWrapper(echo, ToolWrapOptions{
		Name:      "echo",
		AgentId:   "test",
		AgentName: "test",
		MaxStep:   30,
		Logger:    NewTestLogger(t),
	})

	agent, err := CreateReactAgent(context.Background(), m, AgentOptions{
		MaxStep:     30,
		ToolsConfig: buildToolsConfig([]tool.BaseTool{wrapped}),
	})
	require.NoError(t, err)

	// 注入 doom detector + stepCounter（模拟 ReactAgentNode.buildRunContext）
	ctx := context.Background()
	stepCounter := int32(0)
	ctx = WithStepCounter(ctx, &stepCounter)
	ctx = WithDoomLoopDetector(ctx, NewDoomLoopDetector())

	_, err = agent.Generate(ctx, []*schema.Message{schema.UserMessage("test")})
	require.NoError(t, err)

	runs := atomic.LoadInt32(&echo.runs)
	if runs > 2 {
		t.Fatalf("doom should block echo from 3rd call: echo ran %d times (expect ≤2) — detector 可能没传到工具层", runs)
	}
	t.Logf("doom ok: model called %d times, echo actually ran %d times (rest blocked by doom)", m.callCount, runs)
}
