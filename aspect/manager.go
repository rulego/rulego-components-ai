/*
 * Copyright 2023 The RuleGo Authors.
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

package aspect

import (
	"context"
	"log"
	"sort"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// AspectManager manages and executes aspects for AI agents.
// It provides a centralized way to register and invoke aspects at different
// execution points during agent processing.
//
// AspectManager 管理和执行 AI 智能体的切面。
// 它提供了一种集中的方式来注册和调用智能体处理期间不同执行点的切面。
type AspectManager struct {
	mu      sync.RWMutex
	aspects []Aspect

	// Categorized aspect lists for efficient execution
	// 分类缓存的切面列表，用于高效执行
	startAspects        []AgentStartAspect
	beforeAspects       []AgentBeforeAspect
	aroundAspects       []AgentAroundAspect
	afterAspects        []AgentAfterAspect
	completedAspects    []AgentCompletedAspect
	messageBeforeAspects []MessageBeforeAspect
	messageAfterAspects  []MessageAfterAspect
	streamChunkAspects   []StreamChunkAspect
	toolCallBeforeAspects []ToolCallBeforeAspect
	toolCallAfterAspects  []ToolCallAfterAspect
}

// NewAspectManager creates a new AspectManager instance.
//
// NewAspectManager 创建一个新的 AspectManager 实例。
func NewAspectManager() *AspectManager {
	return &AspectManager{
		aspects: make([]Aspect, 0),
	}
}

// Register registers a single aspect to the manager.
// The aspect will be categorized and sorted by order.
//
// Register 向管理器注册单个切面。
// 切面将被分类并按顺序排序。
func (m *AspectManager) Register(aspect Aspect) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aspects = append(m.aspects, aspect)
	m.categorizeAspects()
}

// RegisterAll registers multiple aspects to the manager at once.
// All aspects will be categorized and sorted by order.
//
// RegisterAll 一次性向管理器注册多个切面。
// 所有切面将被分类并按顺序排序。
func (m *AspectManager) RegisterAll(aspects ...Aspect) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aspects = append(m.aspects, aspects...)
	m.categorizeAspects()
}

// categorizeAspects categorizes and sorts all registered aspects by type and order.
// This method should be called after any modification to the aspects list.
//
// categorizeAspects 按类型和顺序对所有已注册的切面进行分类和排序。
// 此方法应在修改切面列表后调用。
func (m *AspectManager) categorizeAspects() {
	// Create a copy and sort by Order
	// 创建副本并按 Order 排序
	sortedAspects := make([]Aspect, len(m.aspects))
	copy(sortedAspects, m.aspects)
	sort.Slice(sortedAspects, func(i, j int) bool {
		return sortedAspects[i].Order() < sortedAspects[j].Order()
	})

	// Clear existing lists
	// 清空现有列表
	m.startAspects = m.startAspects[:0]
	m.beforeAspects = m.beforeAspects[:0]
	m.aroundAspects = m.aroundAspects[:0]
	m.afterAspects = m.afterAspects[:0]
	m.completedAspects = m.completedAspects[:0]
	m.messageBeforeAspects = m.messageBeforeAspects[:0]
	m.messageAfterAspects = m.messageAfterAspects[:0]
	m.streamChunkAspects = m.streamChunkAspects[:0]
	m.toolCallBeforeAspects = m.toolCallBeforeAspects[:0]
	m.toolCallAfterAspects = m.toolCallAfterAspects[:0]

	// Categorize aspects
	// 分类切面
	for _, aspect := range sortedAspects {
		if a, ok := aspect.(AgentStartAspect); ok {
			m.startAspects = append(m.startAspects, a)
		}
		if a, ok := aspect.(AgentBeforeAspect); ok {
			m.beforeAspects = append(m.beforeAspects, a)
		}
		if a, ok := aspect.(AgentAroundAspect); ok {
			m.aroundAspects = append(m.aroundAspects, a)
		}
		if a, ok := aspect.(AgentAfterAspect); ok {
			m.afterAspects = append(m.afterAspects, a)
		}
		if a, ok := aspect.(AgentCompletedAspect); ok {
			m.completedAspects = append(m.completedAspects, a)
		}
		if a, ok := aspect.(MessageBeforeAspect); ok {
			m.messageBeforeAspects = append(m.messageBeforeAspects, a)
		}
		if a, ok := aspect.(MessageAfterAspect); ok {
			m.messageAfterAspects = append(m.messageAfterAspects, a)
		}
		if a, ok := aspect.(StreamChunkAspect); ok {
			m.streamChunkAspects = append(m.streamChunkAspects, a)
		}
		if a, ok := aspect.(ToolCallBeforeAspect); ok {
			m.toolCallBeforeAspects = append(m.toolCallBeforeAspects, a)
		}
		if a, ok := aspect.(ToolCallAfterAspect); ok {
			m.toolCallAfterAspects = append(m.toolCallAfterAspects, a)
		}
	}
}

// ExecuteStart executes all registered AgentStartAspect instances.
// Returns the modified input and any error encountered.
//
// ExecuteStart 执行所有已注册的 AgentStartAspect 实例。
// 返回修改后的输入和遇到的任何错误。
func (m *AspectManager) ExecuteStart(ctx context.Context, point *AgentPoint, input *AgentInput) (*AgentInput, error) {
	m.mu.RLock()
	aspects := m.startAspects
	m.mu.RUnlock()

	var err error
	currentInput := input
	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			currentInput, err = aspect.OnStart(ctx, point, currentInput)
			if err != nil {
				return nil, err
			}
		}
	}
	return currentInput, nil
}

// ExecuteBefore executes all registered AgentBeforeAspect instances.
// Returns the modified input and any error encountered.
//
// ExecuteBefore 执行所有已注册的 AgentBeforeAspect 实例。
// 返回修改后的输入和遇到的任何错误。
func (m *AspectManager) ExecuteBefore(ctx context.Context, point *AgentPoint, input *AgentInput) (*AgentInput, error) {
	m.mu.RLock()
	aspects := m.beforeAspects
	m.mu.RUnlock()

	var err error
	currentInput := input
	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			currentInput, err = aspect.Before(ctx, point, currentInput)
			if err != nil {
				return nil, err
			}
		}
	}
	return currentInput, nil
}

// ExecuteAround executes all registered AgentAroundAspect instances in a chain.
// Each aspect can decide whether to call the next executor.
//
// ExecuteAround 以链式方式执行所有已注册的 AgentAroundAspect 实例。
// 每个切面可以决定是否调用下一个执行器。
func (m *AspectManager) ExecuteAround(ctx context.Context, point *AgentPoint, input *AgentInput, executor AgentExecutor) (*AgentOutput, error) {
	m.mu.RLock()
	aspects := m.aroundAspects
	m.mu.RUnlock()

	// Build the aspect chain
	// 构建切面链
	next := executor
	for i := len(aspects) - 1; i >= 0; i-- {
		aspect := aspects[i]
		currentNext := next
		next = func(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
			if aspect.PointCut(ctx, point) {
				return aspect.Around(ctx, point, input, currentNext)
			}
			return currentNext(ctx, input)
		}
	}

	return next(ctx, input)
}

// ExecuteAfter executes all registered AgentAfterAspect instances.
// Returns the modified output and any error encountered.
//
// ExecuteAfter 执行所有已注册的 AgentAfterAspect 实例。
// 返回修改后的输出和遇到的任何错误。
func (m *AspectManager) ExecuteAfter(ctx context.Context, point *AgentPoint, output *AgentOutput) (*AgentOutput, error) {
	m.mu.RLock()
	aspects := m.afterAspects
	m.mu.RUnlock()

	var err error
	currentOutput := output
	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			currentOutput, err = aspect.After(ctx, point, currentOutput)
			if err != nil {
				// After aspect errors are non-terminating, log and continue
				// After 切面错误是非终止的，记录并继续
				log.Printf("[AspectManager] After aspect error: %v", err)
				continue
			}
		}
	}
	return currentOutput, nil
}

// ExecuteCompleted executes all registered AgentCompletedAspect instances.
// This is called when agent processing completes (success or failure).
//
// ExecuteCompleted 执行所有已注册的 AgentCompletedAspect 实例。
// 当智能体处理完成时调用（成功或失败）。
func (m *AspectManager) ExecuteCompleted(ctx context.Context, point *AgentPoint, output *AgentOutput) {
	m.mu.RLock()
	aspects := m.completedAspects
	m.mu.RUnlock()

	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[AspectManager] panic in OnCompleted: %v", r)
					}
				}()
				aspect.OnCompleted(ctx, point, output)
			}()
		}
	}
}

// ExecuteMessageBefore executes all registered MessageBeforeAspect instances.
// Returns the modified messages and any error encountered.
//
// ExecuteMessageBefore 执行所有已注册的 MessageBeforeAspect 实例。
// 返回修改后的消息和遇到的任何错误。
func (m *AspectManager) ExecuteMessageBefore(ctx context.Context, point *AgentPoint, messages []*schema.Message) ([]*schema.Message, error) {
	m.mu.RLock()
	aspects := m.messageBeforeAspects
	m.mu.RUnlock()

	var err error
	currentMessages := messages
	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			currentMessages, err = aspect.BeforeLLM(ctx, point, currentMessages)
			if err != nil {
				return nil, err
			}
		}
	}
	return currentMessages, nil
}

// ExecuteMessageAfter executes all registered MessageAfterAspect instances.
// Returns the modified response message and any error encountered.
//
// ExecuteMessageAfter 执行所有已注册的 MessageAfterAspect 实例。
// 返回修改后的响应消息和遇到的任何错误。
func (m *AspectManager) ExecuteMessageAfter(ctx context.Context, point *AgentPoint, response *schema.Message) (*schema.Message, error) {
	m.mu.RLock()
	aspects := m.messageAfterAspects
	m.mu.RUnlock()

	var err error
	currentResponse := response
	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			currentResponse, err = aspect.AfterLLM(ctx, point, currentResponse)
			if err != nil {
				// MessageAfter aspect errors are non-terminating, log and continue
				// MessageAfter 切面错误是非终止的，记录并继续
				log.Printf("[AspectManager] MessageAfter aspect error: %v", err)
				continue
			}
		}
	}
	return currentResponse, nil
}

// ExecuteStreamChunk executes all registered StreamChunkAspect instances.
// Returns any error encountered during processing.
//
// ExecuteStreamChunk 执行所有已注册的 StreamChunkAspect 实例。
// 返回处理期间遇到的任何错误。
func (m *AspectManager) ExecuteStreamChunk(ctx context.Context, point *AgentPoint, chunk *StreamChunk) error {
	m.mu.RLock()
	aspects := m.streamChunkAspects
	m.mu.RUnlock()

	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			if err := aspect.OnChunk(ctx, point, chunk); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExecuteToolCallBefore executes all registered ToolCallBeforeAspect instances.
// Returns the modified tool call info and any error encountered.
//
// ExecuteToolCallBefore 执行所有已注册的 ToolCallBeforeAspect 实例。
// 返回修改后的工具调用信息和遇到的任何错误。
func (m *AspectManager) ExecuteToolCallBefore(ctx context.Context, point *AgentPoint, call *ToolCallInfo) (*ToolCallInfo, error) {
	m.mu.RLock()
	aspects := m.toolCallBeforeAspects
	m.mu.RUnlock()

	var err error
	currentCall := call
	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			currentCall, err = aspect.BeforeToolCall(ctx, point, currentCall)
			if err != nil {
				return nil, err
			}
		}
	}
	return currentCall, nil
}

// ExecuteToolCallAfter executes all registered ToolCallAfterAspect instances.
// Returns any error encountered during processing.
//
// ExecuteToolCallAfter 执行所有已注册的 ToolCallAfterAspect 实例。
// 返回处理期间遇到的任何错误。
func (m *AspectManager) ExecuteToolCallAfter(ctx context.Context, point *AgentPoint, call *ToolCallInfo, result *ToolCallResult) error {
	m.mu.RLock()
	aspects := m.toolCallAfterAspects
	m.mu.RUnlock()

	for _, aspect := range aspects {
		if aspect.PointCut(ctx, point) {
			if err := aspect.AfterToolCall(ctx, point, call, result); err != nil {
				// ToolCallAfter aspect errors are non-terminating, log and continue
				// ToolCallAfter 切面错误是非终止的，记录并继续
				continue
			}
		}
	}
	return nil
}

// Context key type for storing AspectManager in context
// 用于在上下文中存储 AspectManager 的上下文键类型
type aspectManagerKey struct{}

// WithAspectManager stores the AspectManager in the context for later retrieval.
//
// WithAspectManager 将 AspectManager 存储在上下文中以供后续检索。
func WithAspectManager(ctx context.Context, manager *AspectManager) context.Context {
	return context.WithValue(ctx, aspectManagerKey{}, manager)
}

// GetAspectManager retrieves the AspectManager from the context.
// Returns the manager and true if found, nil and false otherwise.
//
// GetAspectManager 从上下文中检索 AspectManager。
// 如果找到则返回管理器和 true，否则返回 nil 和 false。
func GetAspectManager(ctx context.Context) (*AspectManager, bool) {
	manager, ok := ctx.Value(aspectManagerKey{}).(*AspectManager)
	return manager, ok
}

// HasAspects returns true if any aspects are registered.
//
// HasAspects 返回是否注册了任何切面。
func (m *AspectManager) HasAspects() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.aspects) > 0
}
