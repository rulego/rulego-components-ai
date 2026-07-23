package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	toolutil "github.com/rulego/rulego-components-ai/utils/tool"
	"github.com/rulego/rulego/api/types"
)

// RuleGoTool implements eino tool.InvokableTool
type RuleGoTool struct {
	Config config.Tool
}

var _ tool.InvokableTool = (*RuleGoTool)(nil)

// NewRuleGoTool creates a new RuleGoTool
func NewRuleGoTool(config config.Tool) *RuleGoTool {
	return &RuleGoTool{
		Config: config,
	}
}

func (t *RuleGoTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	// If there are parameters, parse them
	paramsOneOf, err := toolutil.ParseToolParameters(t.Config.Parameters)
	if err != nil {
		return nil, fmt.Errorf("invalid tool parameters for %s: %v", t.Config.Name, err)
	}

	return &schema.ToolInfo{
		Name:        t.Config.Name,
		Desc:        t.Config.Description,
		ParamsOneOf: paramsOneOf,
	}, nil
}

func (t *RuleGoTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// Retrieves RuleContext from context
	ruleCtxVal := ctx.Value(config.ShareRuleContextKey)
	if ruleCtxVal == nil {
		return "", fmt.Errorf("context 中未找到 RuleContext")
	}
	ruleCtx, ok := ruleCtxVal.(types.RuleContext)
	if !ok {
		return "", fmt.Errorf("无效的 RuleContext 类型")
	}

	switch t.Config.Type {
	case config.ToolTypeRuleChain, config.ToolTypeAgent:
		// The agent type is the semantic alias for rulechain and is used to call child agents
		return t.executeRuleChain(ctx, ruleCtx, argumentsInJSON)
	default:
		return "", fmt.Errorf("不支持的工具类型: %s", t.Config.Type)
	}
}

func (t *RuleGoTool) executeRuleChain(ctx context.Context, ruleCtx types.RuleContext, arguments string) (string, error) {
	// Directly use parameters to create messages without format conversion
	// The sub-agent parses parameters based on its own inputSchema configuration
	toolMsg := ruleCtx.NewMsg(config.MsgTypeToolCall, types.NewMetadata(), arguments)
	toolMsg.DataType = types.JSON

	// Get the timeout configuration, default 120 seconds
	// Timeout is measured in milliseconds
	timeout := time.Duration(t.Config.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	// Call the rule chain
	var result string
	var resultErr error
	var wg sync.WaitGroup
	var mu sync.Mutex

	wg.Add(1)
	ruleCtx.TellFlow(t.Config.TargetId, toolMsg, types.WithContext(ctx), types.WithOnEnd(func(nodeCtx types.RuleContext, onEndMsg types.RuleMsg, err error, relationType string) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			resultErr = err
		} else {
			result = onEndMsg.GetData()
		}
	}), types.WithOnAllNodeCompleted(func() {
		wg.Done()
	}))

	// Use timeout waits to prevent permanent blockages
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Completed normally
	case <-ctx.Done():
		// Context cancellation
		mu.Lock()
		resultErr = ctx.Err()
		mu.Unlock()
	case <-time.After(timeout):
		// Overtime
		mu.Lock()
		resultErr = fmt.Errorf("rulego tool execution timeout after %v", timeout)
		mu.Unlock()
	}

	if resultErr != nil {
		return "", resultErr
	}

	return result, nil
}
