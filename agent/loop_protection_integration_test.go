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

// recordingModel controllable mock: before toolCallSeq and Generate each time, the same echo tool_call is returned,
// Then return to plain text to finish. Records each received message (used to verify whether dedup has folded duplicate tool_call).
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

// echoTool simply displays and records the actual number of executions (verifying whether doom rejects execution from the third attempt).
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

// maxConsecutiveSameEcho counts the maximum length of consecutive identical echo tool_call in messages.
// The intermediate tool result is not considered an interrupt (consistent with the "adjacent" determination of dedup).
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

// Integration testing: dedup (MessageRewriter) folds continuous and repeated tool_call in the real agent chain,
// Causes the history sent to the model to be discontinuously, with the same name and same parameters (avoiding triggering the provider deadloop guardrail).
func TestIntegration_DedupFoldsRepetitiveToolCalls(t *testing.T) {
	m := &recordingModel{toolCallSeq: 5} // The first 5 times echo tool_call (same args), the 6th time done
	echo := &echoTool{}

	agent, err := CreateReactAgent(context.Background(), m, AgentOptions{
		MaxStep:     30,
		ToolsConfig: buildToolsConfig([]tool.BaseTool{echo}),
	})
	require.NoError(t, err)

	_, err = agent.Generate(context.Background(), []*schema.Message{schema.UserMessage("test")})
	require.NoError(t, err)

	// If the model outputs the same echo five times in a row, the dedup should fold the history sent to the model to ≤ keepLast(2)
	for i, msgs := range m.received {
		if c := maxConsecutiveSameEcho(msgs); c > 2 {
			t.Fatalf("model call #%d: dedup should fold repetitive echo, found %d consecutive identical", i, c)
		}
	}
	t.Logf("dedup ok: model called %d times, echo actually ran %d times", m.callCount, atomic.LoadInt32(&echo.runs))
}

// Integration testing: Doom rejects repeated calls at the tool layer. Wrap echo and ctx with VisualToolWrapper and inject detectors,
// When the model outputs the same echo consecutively, execution is rejected starting from the third attempt (echo actually runs ≤ twice).
// At the same time, it was verified whether the detector could be transmitted to the tool layer via CTX—the uncertainty of previous static analysis was finally settled here.
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

	// Inject doom detector + stepCounter (simulate ReactAgentNode.buildRunContext)
	ctx := context.Background()
	stepCounter := int32(0)
	ctx = WithStepCounter(ctx, &stepCounter)
	ctx = WithDoomLoopDetector(ctx, NewDoomLoopDetector())

	_, err = agent.Generate(ctx, []*schema.Message{schema.UserMessage("test")})
	require.NoError(t, err)

	runs := atomic.LoadInt32(&echo.runs)
	if runs > 2 {
		t.Fatalf("doom should block echo from 3rd call: echo ran %d times (expect ≤2) — detector Maybe it hasn't reached the tool layer", runs)
	}
	t.Logf("doom ok: model called %d times, echo actually ran %d times (rest blocked by doom)", m.callCount, runs)
}
