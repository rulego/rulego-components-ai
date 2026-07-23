package agent

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego/api/types"
	"github.com/stretchr/testify/assert"
)

// TestExecuteStream_MidStreamError Reproduction: Model stream returns "Error in input stream" midway,
// ExecuteStream should pass errors to the upper layer (output.Error + return err), no longer silently swallowing as a success.
func TestExecuteStream_MidStreamError(t *testing.T) {
	executor := NewAgentAspectExecutor(NewTestLogger(t))
	executor.manager = aspect.NewAspectManager()

	opts := ExecuteOptions{
		ChainId:   "test_chain",
		AgentName: "test_agent",
		Msg:       types.NewMsg(0, "TEST", types.JSON, types.NewMetadata(), ""),
	}
	agentInput := &aspect.AgentInput{}
	messages := []*schema.Message{{Role: schema.User, Content: "Hello"}}

	// Simulating model stream: After 2 normal chunks, the third Recv returns "Error in input stream"
	streamExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
		reader, writer := schema.Pipe[*schema.Message](1)
		go func() {
			defer writer.Close()
			writer.Send(&schema.Message{Content: "chunk1 ", Role: schema.Assistant}, nil)
			writer.Send(&schema.Message{Content: "chunk2", Role: schema.Assistant}, nil)
			writer.Send(nil, errors.New("Error in input stream"))
		}()
		return reader, nil
	}

	var chunks []string
	onChunk := func(content, reasoning string, isFirst bool) {
		chunks = append(chunks, content)
	}

	output, err := executor.ExecuteStream(context.Background(), opts, agentInput, messages, streamExecutor, onChunk)

	// After modification: Errors are no longer swallowed, ExecuteStream returns error
	assert.Error(t, err, "mid-stream 错误应透传，不应被吞")
	assert.Contains(t, err.Error(), "Error in input stream")
	// output still contains the received content + Error field
	if assert.NotNil(t, output) {
		assert.Contains(t, output.Content, "chunk1")
		assert.Error(t, output.Error, "output.Error 应记录截断错误")
	}
	// Issued chunks are unaffected
	assert.Equal(t, []string{"chunk1 ", "chunk2"}, chunks)
}

// TestExecuteStream_NormalEOF Normal io.EOF should not return error (Regression protection: don't treat normal termination as an error)
func TestExecuteStream_NormalEOF(t *testing.T) {
	executor := NewAgentAspectExecutor(NewTestLogger(t))
	executor.manager = aspect.NewAspectManager()

	opts := ExecuteOptions{
		ChainId:   "test_chain",
		AgentName: "test_agent",
		Msg:       types.NewMsg(0, "TEST", types.JSON, types.NewMetadata(), ""),
	}
	agentInput := &aspect.AgentInput{}
	messages := []*schema.Message{{Role: schema.User, Content: "Hello"}}

	streamExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
		reader, writer := schema.Pipe[*schema.Message](1)
		go func() {
			defer writer.Close()
			writer.Send(&schema.Message{Content: "hello", Role: schema.Assistant}, nil)
			writer.Send(nil, io.EOF)
		}()
		return reader, nil
	}

	output, err := executor.ExecuteStream(context.Background(), opts, agentInput, messages, streamExecutor, func(string, string, bool) {})
	assert.NoError(t, err, "正常 EOF 不应返回 error")
	if assert.NotNil(t, output) {
		assert.Nil(t, output.Error, "正常结束 output.Error 应为 nil")
		assert.Equal(t, "hello", output.Content)
	}
}
