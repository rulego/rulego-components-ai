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

// TestExecuteStream_MidStreamError 复现：model stream 中途返回 "Error in input stream"，
// ExecuteStream 应把错误透传给上层（output.Error + return err），不再静默吞掉当成成功。
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

	// 模拟 model stream：2 个正常 chunk 后，第 3 个 Recv 返回 "Error in input stream"
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

	// 改后：错误不再被吞，ExecuteStream 返回 error
	assert.Error(t, err, "mid-stream 错误应透传，不应被吞")
	assert.Contains(t, err.Error(), "Error in input stream")
	// output 仍带已收的部分内容 + Error 字段
	if assert.NotNil(t, output) {
		assert.Contains(t, output.Content, "chunk1")
		assert.Error(t, output.Error, "output.Error 应记录截断错误")
	}
	// 已发出的 chunk 不受影响
	assert.Equal(t, []string{"chunk1 ", "chunk2"}, chunks)
}

// TestExecuteStream_NormalEOF 正常 io.EOF 不应返回 error（回归保护：别把正常结束也当错误）
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
