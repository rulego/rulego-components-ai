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

// RuleGoTool 实现 eino tool.InvokableTool
type RuleGoTool struct {
	Config config.Tool
}

var _ tool.InvokableTool = (*RuleGoTool)(nil)

// NewRuleGoTool 创建一个新的 RuleGoTool
func NewRuleGoTool(config config.Tool) *RuleGoTool {
	return &RuleGoTool{
		Config: config,
	}
}

func (t *RuleGoTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	// 如果存在参数，解析它们
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
	// 从 context 获取 RuleContext
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
		// agent 类型是 rulechain 的语义别名，用于调用子智能体
		return t.executeRuleChain(ctx, ruleCtx, argumentsInJSON)
	default:
		return "", fmt.Errorf("不支持的工具类型: %s", t.Config.Type)
	}
}

func (t *RuleGoTool) executeRuleChain(ctx context.Context, ruleCtx types.RuleContext, arguments string) (string, error) {
	// 直接使用参数创建消息，不做格式转换
	// 子智能体会根据自己的 inputSchema 配置来解析参数
	toolMsg := ruleCtx.NewMsg(config.MsgTypeToolCall, types.NewMetadata(), arguments)
	toolMsg.DataType = types.JSON

	// 获取超时配置，默认120秒
	// Timeout 单位是毫秒
	timeout := time.Duration(t.Config.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	// 调用规则链
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

	// 使用带超时的等待，防止永久阻塞
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 正常完成
	case <-ctx.Done():
		// 上下文取消
		mu.Lock()
		resultErr = ctx.Err()
		mu.Unlock()
	case <-time.After(timeout):
		// 超时
		mu.Lock()
		resultErr = fmt.Errorf("rulego tool execution timeout after %v", timeout)
		mu.Unlock()
	}

	if resultErr != nil {
		return "", resultErr
	}

	return result, nil
}
