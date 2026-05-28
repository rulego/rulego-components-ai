/*
 * Copyright 2026 The TPClaw Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAroundAspect 模拟的环绕切面，用于测试切面链的执行顺序和中断行为
type mockAroundAspect struct {
	name          string
	order         int
	calledBefore  bool
	calledAfter   bool
	shouldReturn  bool // 是否在此切面直接返回，中断链式调用
	returnContent string
}

func (a *mockAroundAspect) Order() int {
	return a.order
}

func (a *mockAroundAspect) New() aspect.Aspect {
	return a
}

func (a *mockAroundAspect) PointCut(ctx context.Context, point *aspect.AgentPoint) bool {
	return true
}

func (a *mockAroundAspect) Around(ctx context.Context, point *aspect.AgentPoint, input *aspect.AgentInput, next aspect.AgentExecutor) (*aspect.AgentOutput, error) {
	a.calledBefore = true

	if a.shouldReturn {
		// 中断链式调用，直接返回结果
		return &aspect.AgentOutput{
			Content:   a.returnContent,
			IsSuccess: true,
			SkippedAI: true,
		}, nil
	}

	// 继续执行下一个切面或核心逻辑
	out, err := next(ctx, input)
	a.calledAfter = true
	return out, err
}

// TestAspectIntegration_MultipleAroundAspects_Sync 测试同步模式下的多个环绕切面
func TestAspectIntegration_MultipleAroundAspects_Sync(t *testing.T) {
	// 创建执行器并清空默认切面
	executor := NewAgentAspectExecutor(NewTestLogger(t))
	// 通过反射或者重新创建一个只有我们需要的切面的管理器（为了简单，我们重新创建一个 Manager）
	manager := aspect.NewAspectManager()
	executor.manager = manager

	// 创建三个切面，order 分别为 1, 2, 3
	aspect1 := &mockAroundAspect{name: "Aspect1", order: 1}
	aspect2 := &mockAroundAspect{name: "Aspect2", order: 2}
	aspect3 := &mockAroundAspect{name: "Aspect3", order: 3}

	manager.RegisterAll(aspect1, aspect2, aspect3)

	opts := ExecuteOptions{
		ChainId:   "test_chain",
		AgentName: "test_agent",
		Msg:       types.NewMsg(0, "TEST", types.JSON, types.NewMetadata(), ""),
	}
	agentInput := &aspect.AgentInput{}
	messages := []*schema.Message{{Role: schema.User, Content: "Hello"}}

	// 核心业务逻辑
	coreExecuted := false
	coreExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		coreExecuted = true
		return &schema.Message{Role: schema.Assistant, Content: "Core Output"}, nil
	}

	output, err := executor.ExecuteSync(context.Background(), opts, agentInput, messages, coreExecutor)
	require.NoError(t, err)

	// 验证执行顺序和结果
	assert.True(t, coreExecuted, "Core logic should be executed")
	assert.Equal(t, "Core Output", output.Content)
	assert.True(t, aspect1.calledBefore && aspect1.calledAfter, "Aspect1 should be fully executed")
	assert.True(t, aspect2.calledBefore && aspect2.calledAfter, "Aspect2 should be fully executed")
	assert.True(t, aspect3.calledBefore && aspect3.calledAfter, "Aspect3 should be fully executed")
}

// TestAspectIntegration_MultipleAroundAspects_Stream 测试流式模式下的多个环绕切面
func TestAspectIntegration_MultipleAroundAspects_Stream(t *testing.T) {
	executor := NewAgentAspectExecutor(NewTestLogger(t))
	manager := aspect.NewAspectManager()
	executor.manager = manager

	aspect1 := &mockAroundAspect{name: "Aspect1", order: 1}
	aspect2 := &mockAroundAspect{name: "Aspect2", order: 2}
	aspect3 := &mockAroundAspect{name: "Aspect3", order: 3}

	manager.RegisterAll(aspect1, aspect2, aspect3)

	opts := ExecuteOptions{
		ChainId:   "test_chain",
		AgentName: "test_agent",
		Msg:       types.NewMsg(0, "TEST", types.JSON, types.NewMetadata(), ""),
	}
	agentInput := &aspect.AgentInput{}
	messages := []*schema.Message{{Role: schema.User, Content: "Hello"}}

	// 核心流式业务逻辑
	coreExecuted := false
	streamExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
		coreExecuted = true

		// 模拟一个简单的流式读取器
		reader, writer := schema.Pipe[*schema.Message](1)
		go func() {
			defer writer.Close()
			writer.Send(&schema.Message{Content: "Stream ", Role: schema.Assistant}, nil)
			writer.Send(&schema.Message{Content: "Output", Role: schema.Assistant}, nil)
		}()

		return reader, nil
	}

	var chunks []string
	onChunk := func(chunk string, isFirst bool) {
		chunks = append(chunks, chunk)
	}

	output, err := executor.ExecuteStream(context.Background(), opts, agentInput, messages, streamExecutor, onChunk)
	require.NoError(t, err)

	// 验证执行顺序和结果
	assert.True(t, coreExecuted, "Core stream logic should be executed")
	assert.Equal(t, "Stream Output", output.Content)
	assert.Equal(t, []string{"Stream ", "Output"}, chunks, "Chunks should be received correctly")
	assert.True(t, aspect1.calledBefore && aspect1.calledAfter, "Aspect1 should be fully executed")
	assert.True(t, aspect2.calledBefore && aspect2.calledAfter, "Aspect2 should be fully executed")
	assert.True(t, aspect3.calledBefore && aspect3.calledAfter, "Aspect3 should be fully executed")
}

// TestAspectIntegration_MultipleAroundAspects_Interrupt 测试环绕切面中断机制
func TestAspectIntegration_MultipleAroundAspects_Interrupt(t *testing.T) {
	executor := NewAgentAspectExecutor(NewTestLogger(t))
	manager := aspect.NewAspectManager()
	executor.manager = manager

	// 切面2 会中断调用链
	aspect1 := &mockAroundAspect{name: "Aspect1", order: 1}
	aspect2 := &mockAroundAspect{name: "Aspect2", order: 2, shouldReturn: true, returnContent: "Intercepted by Aspect2"}
	aspect3 := &mockAroundAspect{name: "Aspect3", order: 3}

	manager.RegisterAll(aspect1, aspect2, aspect3)

	opts := ExecuteOptions{
		ChainId:   "test_chain",
		AgentName: "test_agent",
		Msg:       types.NewMsg(0, "TEST", types.JSON, types.NewMetadata(), ""),
	}
	agentInput := &aspect.AgentInput{}
	messages := []*schema.Message{{Role: schema.User, Content: "Hello"}}

	// 同步执行测试
	t.Run("Sync Interrupt", func(t *testing.T) {
		coreExecuted := false
		coreExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
			coreExecuted = true
			return &schema.Message{Role: schema.Assistant, Content: "Core Output"}, nil
		}

		output, err := executor.ExecuteSync(context.Background(), opts, agentInput, messages, coreExecutor)
		require.NoError(t, err)

		// 核心逻辑不应该被执行
		assert.False(t, coreExecuted, "Core logic should NOT be executed")
		assert.Equal(t, "Intercepted by Aspect2", output.Content)

		// 验证切面执行状态
		assert.True(t, aspect1.calledBefore && aspect1.calledAfter, "Aspect1 should wrap Aspect2")
		assert.True(t, aspect2.calledBefore, "Aspect2 before logic should execute")
		assert.False(t, aspect2.calledAfter, "Aspect2 after logic shouldn't execute because it returned early")
		assert.False(t, aspect3.calledBefore, "Aspect3 should NOT be reached")
	})

	// 重置状态
	aspect1.calledBefore, aspect1.calledAfter = false, false
	aspect2.calledBefore, aspect2.calledAfter = false, false
	aspect3.calledBefore, aspect3.calledAfter = false, false

	// 流式执行测试
	t.Run("Stream Interrupt", func(t *testing.T) {
		coreExecuted := false
		streamExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
			coreExecuted = true
			reader, _ := schema.Pipe[*schema.Message](1)
			return reader, nil
		}

		onChunkCalled := false
		onChunk := func(chunk string, isFirst bool) {
			onChunkCalled = true
		}

		output, err := executor.ExecuteStream(context.Background(), opts, agentInput, messages, streamExecutor, onChunk)
		require.NoError(t, err)

		// 核心逻辑不应该被执行
		assert.False(t, coreExecuted, "Core stream logic should NOT be executed")
		assert.False(t, onChunkCalled, "onChunk should NOT be called")
		assert.Equal(t, "Intercepted by Aspect2", output.Content)

		// 验证切面执行状态
		assert.True(t, aspect1.calledBefore && aspect1.calledAfter, "Aspect1 should wrap Aspect2")
		assert.True(t, aspect2.calledBefore, "Aspect2 before logic should execute")
		assert.False(t, aspect2.calledAfter, "Aspect2 after logic shouldn't execute because it returned early")
		assert.False(t, aspect3.calledBefore, "Aspect3 should NOT be reached")
	})
}
