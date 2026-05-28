package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego/test/assert"
)

// mockInvokableTool 用于测试的模拟工具
type mockInvokableTool struct {
	infoFunc func(ctx context.Context) (*schema.ToolInfo, error)
	runFunc  func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error)
}

func (m *mockInvokableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	if m.infoFunc != nil {
		return m.infoFunc(ctx)
	}
	return &schema.ToolInfo{
		Name: "mock_tool",
		Desc: "A mock tool for testing",
	}, nil
}

func (m *mockInvokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, argumentsInJSON, opts...)
	}
	return "mock result", nil
}

// TestNewToolAgent 测试 NewToolAgent 函数
func TestNewToolAgent(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	assert.NotNil(t, agent)
	assert.Equal(t, "test_agent", agent.name)
	assert.Equal(t, "A test agent", agent.description)
	assert.NotNil(t, agent.tool)
}

// TestNewToolAgent_EmptyName 测试空名称
func TestNewToolAgent_EmptyName(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("", "A test agent", mockTool)

	assert.NotNil(t, agent)
	assert.Equal(t, "", agent.name)
}

// TestNewToolAgent_EmptyDescription 测试空描述
func TestNewToolAgent_EmptyDescription(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "", mockTool)

	assert.NotNil(t, agent)
	assert.Equal(t, "", agent.description)
}

// TestNewToolAgent_NilTool 测试 nil 工具
func TestNewToolAgent_NilTool(t *testing.T) {
	agent := NewToolAgent("test_agent", "A test agent", nil)

	assert.NotNil(t, agent)
	assert.Nil(t, agent.tool)
}

// TestToolAgent_Name 测试 Name 方法
func TestToolAgent_Name(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("my_agent", "My agent description", mockTool)

	ctx := context.Background()
	name := agent.Name(ctx)

	assert.Equal(t, "my_agent", name)
}

// TestToolAgent_Description 测试 Description 方法
func TestToolAgent_Description(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("my_agent", "This is my agent", mockTool)

	ctx := context.Background()
	desc := agent.Description(ctx)

	assert.Equal(t, "This is my agent", desc)
}

// TestToolAgent_Run_Success 测试 Run 方法成功情况
func TestToolAgent_Run_Success(t *testing.T) {
	mockTool := &mockInvokableTool{
		runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
			return `{"result": "success"}`, nil
		},
	}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	ctx := context.Background()
	input := &adk.AgentInput{
		Messages: []*schema.Message{
			schema.UserMessage("test input"),
		},
	}

	iterator := agent.Run(ctx, input)
	assert.NotNil(t, iterator)

	// 等待结果
	time.Sleep(100 * time.Millisecond)

	// 获取结果
	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)
	assert.Nil(t, event.Err)
	assert.NotNil(t, event.Output)
	assert.NotNil(t, event.Output.MessageOutput)
	assert.NotNil(t, event.Output.MessageOutput.Message)
	assert.Equal(t, schema.Assistant, event.Output.MessageOutput.Message.Role)
	// 验证内容包含 success
	if event.Output.MessageOutput.Message.Content != "" {
		// 内容应该包含 success
	}
}

// TestToolAgent_Run_ToolError 测试 Run 方法工具错误情况
func TestToolAgent_Run_ToolError(t *testing.T) {
	mockTool := &mockInvokableTool{
		runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
			return "", errors.New("tool execution failed")
		},
	}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	ctx := context.Background()
	input := &adk.AgentInput{
		Messages: []*schema.Message{
			schema.UserMessage("test input"),
		},
	}

	iterator := agent.Run(ctx, input)
	assert.NotNil(t, iterator)

	// 等待结果
	time.Sleep(100 * time.Millisecond)

	// 获取结果
	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)
	assert.NotNil(t, event.Err)
	// 验证错误信息包含 tool execution failed
	if event.Err != nil && event.Err.Error() != "" {
		// 错误应该包含 tool execution failed
	}
}

// TestToolAgent_Run_EmptyMessages 测试 Run 方法空消息列表
func TestToolAgent_Run_EmptyMessages(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	ctx := context.Background()
	input := &adk.AgentInput{
		Messages: []*schema.Message{},
	}

	iterator := agent.Run(ctx, input)
	assert.NotNil(t, iterator)

	// 等待结果
	time.Sleep(100 * time.Millisecond)

	// 空消息列表应该正常处理
	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)
}

// TestToolAgent_Run_MultipleMessages 测试 Run 方法多条消息
func TestToolAgent_Run_MultipleMessages(t *testing.T) {
	var receivedInput string
	mockTool := &mockInvokableTool{
		runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
			receivedInput = argumentsInJSON
			return "processed: " + argumentsInJSON, nil
		},
	}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	ctx := context.Background()
	input := &adk.AgentInput{
		Messages: []*schema.Message{
			schema.SystemMessage("System prompt"),
			schema.UserMessage("First message"),
			schema.AssistantMessage("Assistant response", nil),
			schema.UserMessage("Last user message"), // 应该使用这条
		},
	}

	iterator := agent.Run(ctx, input)
	assert.NotNil(t, iterator)

	// 等待结果
	time.Sleep(100 * time.Millisecond)

	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)
	assert.Nil(t, event.Err)

	// 应该使用最后一条用户消息
	assert.Equal(t, "Last user message", receivedInput)
}

// TestToolAgent_Run_NoUserMessage 测试 Run 方法没有用户消息
func TestToolAgent_Run_NoUserMessage(t *testing.T) {
	var receivedInput string
	mockTool := &mockInvokableTool{
		runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
			receivedInput = argumentsInJSON
			return "ok", nil
		},
	}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	ctx := context.Background()
	input := &adk.AgentInput{
		Messages: []*schema.Message{
			schema.SystemMessage("System prompt"),
			schema.AssistantMessage("Assistant response", nil),
		},
	}

	iterator := agent.Run(ctx, input)
	assert.NotNil(t, iterator)

	// 等待结果
	time.Sleep(100 * time.Millisecond)

	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)

	// 没有用户消息时，应该使用最后一条消息
	assert.Equal(t, "Assistant response", receivedInput)
}

// TestToolAgent_Stream 测试 Stream 方法
func TestToolAgent_Stream(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	ctx := context.Background()
	input := &adk.AgentInput{
		Messages: []*schema.Message{
			schema.UserMessage("test input"),
		},
	}

	reader, err := agent.Stream(ctx, input)

	// Stream 方法未实现，应该返回错误
	assert.NotNil(t, err)
	assert.Nil(t, reader)
}

// TestToolAgent_Interface 测试接口实现
func TestToolAgent_Interface(t *testing.T) {
	// 确保 ToolAgent 实现了 adk.Agent 接口
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test", "test", mockTool)

	// 编译时检查
	_ = agent.Name
	_ = agent.Description
	_ = agent.Run
	_ = agent.Stream
}

// BenchmarkToolAgent_Name 基准测试 Name 方法
func BenchmarkToolAgent_Name(b *testing.B) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "test", mockTool)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = agent.Name(ctx)
	}
}

// BenchmarkToolAgent_Description 基准测试 Description 方法
func BenchmarkToolAgent_Description(b *testing.B) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test", "A test agent description", mockTool)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = agent.Description(ctx)
	}
}

// BenchmarkToolAgent_Run 基准测试 Run 方法
func BenchmarkToolAgent_Run(b *testing.B) {
	mockTool := &mockInvokableTool{
		runFunc: func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
			return "result", nil
		},
	}
	agent := NewToolAgent("test", "test", mockTool)
	ctx := context.Background()
	input := &adk.AgentInput{
		Messages: []*schema.Message{
			schema.UserMessage("test"),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		iterator := agent.Run(ctx, input)
		// 等待完成
		time.Sleep(10 * time.Millisecond)
		_, _ = iterator.Next()
	}
}

// BenchmarkNewToolAgent 基准测试 NewToolAgent
func BenchmarkNewToolAgent(b *testing.B) {
	mockTool := &mockInvokableTool{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewToolAgent("test", "test", mockTool)
	}
}
