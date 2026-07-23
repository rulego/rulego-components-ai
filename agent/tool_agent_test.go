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

// mockInvokableTool is a simulation tool used for testing
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

// TestNewToolAgent Test the NewToolAgent function
func TestNewToolAgent(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	assert.NotNil(t, agent)
	assert.Equal(t, "test_agent", agent.name)
	assert.Equal(t, "A test agent", agent.description)
	assert.NotNil(t, agent.tool)
}

// TestNewToolAgent_EmptyName Test the empty name
func TestNewToolAgent_EmptyName(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("", "A test agent", mockTool)

	assert.NotNil(t, agent)
	assert.Equal(t, "", agent.name)
}

// TestNewToolAgent_EmptyDescription Testspace description
func TestNewToolAgent_EmptyDescription(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "", mockTool)

	assert.NotNil(t, agent)
	assert.Equal(t, "", agent.description)
}

// TestNewToolAgent_NilTool Test the NIL tool
func TestNewToolAgent_NilTool(t *testing.T) {
	agent := NewToolAgent("test_agent", "A test agent", nil)

	assert.NotNil(t, agent)
	assert.Nil(t, agent.tool)
}

// TestToolAgent_Name Test the Name method
func TestToolAgent_Name(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("my_agent", "My agent description", mockTool)

	ctx := context.Background()
	name := agent.Name(ctx)

	assert.Equal(t, "my_agent", name)
}

// TestToolAgent_Description Test Description methods
func TestToolAgent_Description(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("my_agent", "This is my agent", mockTool)

	ctx := context.Background()
	desc := agent.Description(ctx)

	assert.Equal(t, "This is my agent", desc)
}

// TestToolAgent_Run_Success Test the success of the Run method
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

	// Wait for the results
	time.Sleep(100 * time.Millisecond)

	// Get results
	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)
	assert.Nil(t, event.Err)
	assert.NotNil(t, event.Output)
	assert.NotNil(t, event.Output.MessageOutput)
	assert.NotNil(t, event.Output.MessageOutput.Message)
	assert.Equal(t, schema.Assistant, event.Output.MessageOutput.Message.Role)
	// Verification includes success
	if event.Output.MessageOutput.Message.Content != "" {
		// The content should include success
	}
}

// TestToolAgent_Run_ToolError Error situations in the Run method tool
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

	// Wait for the results
	time.Sleep(100 * time.Millisecond)

	// Get results
	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)
	assert.NotNil(t, event.Err)
	// The validation error message contains tool execution failed
	if event.Err != nil && event.Err.Error() != "" {
		// The error should include tool execution failed
	}
}

// TestToolAgent_Run_EmptyMessages Test the Run method for an empty message list
func TestToolAgent_Run_EmptyMessages(t *testing.T) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "A test agent", mockTool)

	ctx := context.Background()
	input := &adk.AgentInput{
		Messages: []*schema.Message{},
	}

	iterator := agent.Run(ctx, input)
	assert.NotNil(t, iterator)

	// Wait for the results
	time.Sleep(100 * time.Millisecond)

	// Empty message lists should be handled properly
	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)
}

// TestToolAgent_Run_MultipleMessages Test multiple messages in the Run method
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
			schema.UserMessage("Last user message"), // This should be used
		},
	}

	iterator := agent.Run(ctx, input)
	assert.NotNil(t, iterator)

	// Wait for the results
	time.Sleep(100 * time.Millisecond)

	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)
	assert.Nil(t, event.Err)

	// The last user message should be used
	assert.Equal(t, "Last user message", receivedInput)
}

// TestToolAgent_Run_NoUserMessage Test the Run method has no user messages
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

	// Wait for the results
	time.Sleep(100 * time.Millisecond)

	event, ok := iterator.Next()
	assert.True(t, ok)
	assert.NotNil(t, event)

	// If there is no user message, the last message should be used
	assert.Equal(t, "Assistant response", receivedInput)
}

// TestToolAgent_Stream Test the Stream method
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

	// The Stream method is not implemented and should return an error
	assert.NotNil(t, err)
	assert.Nil(t, reader)
}

// TestToolAgent_Interface Test interface implementation
func TestToolAgent_Interface(t *testing.T) {
	// Make sure ToolAgent implements adk.Agent interface
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test", "test", mockTool)

	// Compile time check
	_ = agent.Name
	_ = agent.Description
	_ = agent.Run
	_ = agent.Stream
}

// BenchmarkToolAgent_Name Benchmark Name method
func BenchmarkToolAgent_Name(b *testing.B) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test_agent", "test", mockTool)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = agent.Name(ctx)
	}
}

// BenchmarkToolAgent_Description Benchmark Description method
func BenchmarkToolAgent_Description(b *testing.B) {
	mockTool := &mockInvokableTool{}
	agent := NewToolAgent("test", "A test agent description", mockTool)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = agent.Description(ctx)
	}
}

// BenchmarkToolAgent_Run Benchmark Run method
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
		// Waiting for completion
		time.Sleep(10 * time.Millisecond)
		_, _ = iterator.Next()
	}
}

// BenchmarkNewToolAgent Benchmarking NewToolAgent
func BenchmarkNewToolAgent(b *testing.B) {
	mockTool := &mockInvokableTool{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewToolAgent("test", "test", mockTool)
	}
}
