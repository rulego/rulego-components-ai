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

package agent

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/aspect"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/api/types"
)

// contextKeySessionModel 是 context 中 session_model 的 key
type contextKeySessionModel struct{}

// SessionModelFromContext 从 context 中获取 session_model
func SessionModelFromContext(ctx context.Context) string {
	if v := ctx.Value(contextKeySessionModel{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ContextWithSessionModel 将 session_model 注入到 context
func ContextWithSessionModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, contextKeySessionModel{}, model)
}

// maxCachedModels 动态模型缓存的最大数量
const maxCachedModels = 32

// DynamicModelWrapper 动态模型包装器
// 支持根据 context 中的 session_model 动态切换模型
type DynamicModelWrapper struct {
	baseModel    model.ToolCallingChatModel
	llmConfig    config.LLMConfig
	modelCache   sync.Map // modelID -> model.ToolCallingChatModel
	cacheCount   int32    // 缓存中的模型数量
	logger       types.Logger
	modelOptions ModelOptions
}

// NewDynamicModelWrapper 创建动态模型包装器
func NewDynamicModelWrapper(baseModel model.ToolCallingChatModel, llmConfig config.LLMConfig, opts ModelOptions) *DynamicModelWrapper {
	return &DynamicModelWrapper{
		baseModel:    baseModel,
		llmConfig:    llmConfig,
		modelOptions: opts,
	}
}

// getModelForContext 根据 context 获取合适的模型
// 如果 context 中有 session_model 且与默认模型不同，则创建/缓存一个新的模型
func (w *DynamicModelWrapper) getModelForContext(ctx context.Context) model.ToolCallingChatModel {
	sessionModel := SessionModelFromContext(ctx)

	// 如果没有 session_model 或者与默认模型相同，使用基础模型
	if sessionModel == "" || sessionModel == w.llmConfig.Model {
		return w.baseModel
	}

	// 尝试从缓存获取
	if cached, ok := w.modelCache.Load(sessionModel); ok {
		if m, ok := cached.(model.ToolCallingChatModel); ok {
			return m
		}
	}

	// 创建新模型配置
	newConfig := w.llmConfig
	newConfig.Model = sessionModel

	// 创建新模型
	newModel, err := CreateChatModel(newConfig, w.modelOptions)
	if err != nil {
		if w.logger != nil {
			w.logger.Warnf("[DynamicModelWrapper] Failed to create model %s: %v, using default model", sessionModel, err)
		}
		return w.baseModel
	}

	// 缓存模型（达到上限时不再缓存，但仍然返回新模型）
	if w.cacheCount < maxCachedModels {
		w.modelCache.Store(sessionModel, newModel)
		w.cacheCount++
	}

	if w.logger != nil {
		w.logger.Debugf("[DynamicModelWrapper] Created and cached model: %s", sessionModel)
	}

	return newModel
}

// Generate 生成方法
func (w *DynamicModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m := w.getModelForContext(ctx)
	return m.Generate(ctx, input, opts...)
}

// Stream 流式生成方法
func (w *DynamicModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m := w.getModelForContext(ctx)
	return m.Stream(ctx, input, opts...)
}

// WithTools 设置工具并返回新模型，新实例共享同一个 modelCache
func (w *DynamicModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newModel, err := w.baseModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	// 返回包装后的模型，共享 modelCache
	return &DynamicModelWrapper{
		baseModel:    newModel,
		llmConfig:    w.llmConfig,
		modelCache:   w.modelCache,
		cacheCount:   w.cacheCount,
		logger:       w.logger,
		modelOptions: w.modelOptions,
	}, nil
}

// Ensure DynamicModelWrapper implements model.ToolCallingChatModel
var _ model.ToolCallingChatModel = (*DynamicModelWrapper)(nil)

// ============================================
// AgentAwareModelWrapper - 支持 Agent 级别的模型切换
// ============================================

// AgentAwareModelWrapper 支持在 Agent 执行时动态切换模型的包装器
// 与 DynamicModelWrapper 不同，它支持在创建 React Agent 时就包装模型
type AgentAwareModelWrapper struct {
	*DynamicModelWrapper
}

// NewAgentAwareModelWrapper 创建支持 Agent 的模型包装器
func NewAgentAwareModelWrapper(baseModel model.ToolCallingChatModel, llmConfig config.LLMConfig, opts ModelOptions) *AgentAwareModelWrapper {
	return &AgentAwareModelWrapper{
		DynamicModelWrapper: NewDynamicModelWrapper(baseModel, llmConfig, opts),
	}
}

// WithTools 设置工具
func (w *AgentAwareModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	// 委托给 DynamicModelWrapper
	newModel, err := w.DynamicModelWrapper.WithTools(tools)
	if err != nil {
		return nil, err
	}

	// 如果返回的是 DynamicModelWrapper，则包装为 AgentAwareModelWrapper
	if dm, ok := newModel.(*DynamicModelWrapper); ok {
		return &AgentAwareModelWrapper{
			DynamicModelWrapper: dm,
		}, nil
	}

	return newModel, nil
}

// Ensure AgentAwareModelWrapper implements model.ToolCallingChatModel
var _ model.ToolCallingChatModel = (*AgentAwareModelWrapper)(nil)

// ============================================
// Helper Functions
// ============================================

// WrapModelWithDynamicSupport 包装模型以支持动态切换
// 如果提供了 logger，会在切换模型时输出日志
func WrapModelWithDynamicSupport(baseModel model.ToolCallingChatModel, llmConfig config.LLMConfig, opts ModelOptions) model.ToolCallingChatModel {
	return NewAgentAwareModelWrapper(baseModel, llmConfig, opts)
}

// InjectSessionModelToContext 将 session_model 注入到 context 的辅助函数
// 可以在执行 Agent 前调用
func InjectSessionModelToContext(ctx context.Context, metadata map[string]string) context.Context {
	if metadata == nil {
		return ctx
	}

	// 从 metadata 中读取 session_model
	if sessionModel, ok := metadata[aspect.MetaSessionModel]; ok && sessionModel != "" {
		return ContextWithSessionModel(ctx, sessionModel)
	}

	return ctx
}
