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

// mockAroundAspect simulates an around aspect to test execution order and interruption behavior in the aspect chain.
type mockAroundAspect struct {
	name          string
	order         int
	calledBefore  bool
	calledAfter   bool
	shouldReturn  bool // Whether to return directly on this facet interrupts the chain call
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
		// Interrupt the chain call and return the result directly
		return &aspect.AgentOutput{
			Content:   a.returnContent,
			IsSuccess: true,
			SkippedAI: true,
		}, nil
	}

	// Continue executing the next aspect or core logic
	out, err := next(ctx, input)
	a.calledAfter = true
	return out, err
}

// TestAspectIntegration_MultipleAroundAspects_Sync tests multiple around aspects in synchronous mode.
func TestAspectIntegration_MultipleAroundAspects_Sync(t *testing.T) {
	// Create an executor and clear the default aspects.
	executor := NewAgentAspectExecutor(NewTestLogger(t))
	// Create a new Manager containing only the aspects needed by this test.
	manager := aspect.NewAspectManager()
	executor.manager = manager

	// Create three aspects with orders 1, 2, and 3.
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

	// Core business logic
	coreExecuted := false
	coreExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		coreExecuted = true
		return &schema.Message{Role: schema.Assistant, Content: "Core Output"}, nil
	}

	output, err := executor.ExecuteSync(context.Background(), opts, agentInput, messages, coreExecutor)
	require.NoError(t, err)

	// Verify the execution sequence and results
	assert.True(t, coreExecuted, "Core logic should be executed")
	assert.Equal(t, "Core Output", output.Content)
	assert.True(t, aspect1.calledBefore && aspect1.calledAfter, "Aspect1 should be fully executed")
	assert.True(t, aspect2.calledBefore && aspect2.calledAfter, "Aspect2 should be fully executed")
	assert.True(t, aspect3.calledBefore && aspect3.calledAfter, "Aspect3 should be fully executed")
}

// TestAspectIntegration_MultipleAroundAspects_Stream tests multiple around aspects in streaming mode.
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

	// Core streaming business logic
	coreExecuted := false
	streamExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
		coreExecuted = true

		// Simulates a simple stream reader
		reader, writer := schema.Pipe[*schema.Message](1)
		go func() {
			defer writer.Close()
			writer.Send(&schema.Message{Content: "Stream ", Role: schema.Assistant}, nil)
			writer.Send(&schema.Message{Content: "Output", Role: schema.Assistant}, nil)
		}()

		return reader, nil
	}

	var chunks []string
	onChunk := func(content, reasoning string, isFirst bool) {
		chunks = append(chunks, content)
	}

	output, err := executor.ExecuteStream(context.Background(), opts, agentInput, messages, streamExecutor, onChunk)
	require.NoError(t, err)

	// Verify the execution sequence and results
	assert.True(t, coreExecuted, "Core stream logic should be executed")
	assert.Equal(t, "Stream Output", output.Content)
	assert.Equal(t, []string{"Stream ", "Output"}, chunks, "Chunks should be received correctly")
	assert.True(t, aspect1.calledBefore && aspect1.calledAfter, "Aspect1 should be fully executed")
	assert.True(t, aspect2.calledBefore && aspect2.calledAfter, "Aspect2 should be fully executed")
	assert.True(t, aspect3.calledBefore && aspect3.calledAfter, "Aspect3 should be fully executed")
}

// TestAspectIntegration_MultipleAroundAspects_Interrupt Test the surround cut interrupt mechanism
func TestAspectIntegration_MultipleAroundAspects_Interrupt(t *testing.T) {
	executor := NewAgentAspectExecutor(NewTestLogger(t))
	manager := aspect.NewAspectManager()
	executor.manager = manager

	// Facet 2 interrupts the call chain
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

	// Tests are performed simultaneously
	t.Run("Sync Interrupt", func(t *testing.T) {
		coreExecuted := false
		coreExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
			coreExecuted = true
			return &schema.Message{Role: schema.Assistant, Content: "Core Output"}, nil
		}

		output, err := executor.ExecuteSync(context.Background(), opts, agentInput, messages, coreExecutor)
		require.NoError(t, err)

		// Core logic should not be executed
		assert.False(t, coreExecuted, "Core logic should NOT be executed")
		assert.Equal(t, "Intercepted by Aspect2", output.Content)

		// Verify the execution status of the face
		assert.True(t, aspect1.calledBefore && aspect1.calledAfter, "Aspect1 should wrap Aspect2")
		assert.True(t, aspect2.calledBefore, "Aspect2 before logic should execute")
		assert.False(t, aspect2.calledAfter, "Aspect2 after logic shouldn't execute because it returned early")
		assert.False(t, aspect3.calledBefore, "Aspect3 should NOT be reached")
	})

	// Reset state
	aspect1.calledBefore, aspect1.calledAfter = false, false
	aspect2.calledBefore, aspect2.calledAfter = false, false
	aspect3.calledBefore, aspect3.calledAfter = false, false

	// Stream execution of tests
	t.Run("Stream Interrupt", func(t *testing.T) {
		coreExecuted := false
		streamExecutor := func(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
			coreExecuted = true
			reader, _ := schema.Pipe[*schema.Message](1)
			return reader, nil
		}

		onChunkCalled := false
		onChunk := func(content, reasoning string, isFirst bool) {
			onChunkCalled = true
		}

		output, err := executor.ExecuteStream(context.Background(), opts, agentInput, messages, streamExecutor, onChunk)
		require.NoError(t, err)

		// Core logic should not be executed
		assert.False(t, coreExecuted, "Core stream logic should NOT be executed")
		assert.False(t, onChunkCalled, "onChunk should NOT be called")
		assert.Equal(t, "Intercepted by Aspect2", output.Content)

		// Verify the execution status of the face
		assert.True(t, aspect1.calledBefore && aspect1.calledAfter, "Aspect1 should wrap Aspect2")
		assert.True(t, aspect2.calledBefore, "Aspect2 before logic should execute")
		assert.False(t, aspect2.calledAfter, "Aspect2 after logic shouldn't execute because it returned early")
		assert.False(t, aspect3.calledBefore, "Aspect3 should NOT be reached")
	})
}
