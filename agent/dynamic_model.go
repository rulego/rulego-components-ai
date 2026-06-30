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
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

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

// contextKeySessionExtraFields 是 context 中 session_extra_fields 的 key
type contextKeySessionExtraFields struct{}

// SessionExtraFieldsFromContext 从 context 中获取会话级扩展参数覆盖（思考强度等）
func SessionExtraFieldsFromContext(ctx context.Context) map[string]any {
	if v := ctx.Value(contextKeySessionExtraFields{}); v != nil {
		if m, ok := v.(map[string]any); ok {
			return m
		}
	}
	return nil
}

// ContextWithSessionExtraFields 将会话级扩展参数注入到 context
func ContextWithSessionExtraFields(ctx context.Context, fields map[string]any) context.Context {
	if len(fields) == 0 {
		return ctx
	}
	return context.WithValue(ctx, contextKeySessionExtraFields{}, fields)
}

// maxCachedModels 动态模型缓存的最大数量
const maxCachedModels = 32

// DynamicModelWrapper 动态模型包装器
// 支持根据 context 中的 session_model 动态切换模型
type DynamicModelWrapper struct {
	baseModel    model.ToolCallingChatModel
	llmConfig    config.LLMConfig
	modelCache   *sync.Map // modelID -> model.ToolCallingChatModel（指针，WithTools 派生实例共享）
	cacheCount   *int32    // 缓存中的模型数量（指针，WithTools 派生实例共享）
	logger       types.Logger
	modelOptions ModelOptions
	tools        []*schema.ToolInfo // 绑定的工具列表（重建模型时需重新绑定，否则工具调用失效）
}

// NewDynamicModelWrapper 创建动态模型包装器
func NewDynamicModelWrapper(baseModel model.ToolCallingChatModel, llmConfig config.LLMConfig, opts ModelOptions) *DynamicModelWrapper {
	return &DynamicModelWrapper{
		baseModel:    baseModel,
		llmConfig:    llmConfig,
		modelCache:   &sync.Map{},
		cacheCount:   new(int32),
		logger:       opts.Logger,
		modelOptions: opts,
	}
}

// getModelForContext 根据 context 获取合适的模型
// 触发重建的条件：切了模型（session_model 与默认不同）或有会话级扩展参数覆盖（思考强度等）
func (w *DynamicModelWrapper) getModelForContext(ctx context.Context) model.ToolCallingChatModel {
	sessionModel := SessionModelFromContext(ctx)
	sessionExtra := SessionExtraFieldsFromContext(ctx)
	modelChanged := sessionModel != "" && sessionModel != w.llmConfig.Model

	// 既没切模型、也没扩展参数覆盖 → 用基础模型
	if !modelChanged && len(sessionExtra) == 0 {
		return w.baseModel
	}

	// 缓存键 = 模型名 + extraFields 哈希；同模型同 extra 命中缓存，避免每条消息重建
	cacheKey := w.llmConfig.Model
	if modelChanged {
		cacheKey = sessionModel
	}
	if eh := extraFieldsHash(sessionExtra); eh != "" {
		cacheKey += "|" + eh
	}
	if cached, ok := w.modelCache.Load(cacheKey); ok {
		if m, ok := cached.(model.ToolCallingChatModel); ok {
			return m
		}
	}

	// 创建新模型配置
	newConfig := w.llmConfig
	if modelChanged {
		newConfig.Model = sessionModel
	}
	// 会话级扩展参数覆盖（思考强度等）：session override 优先合并到模型默认 ExtraFields
	if len(sessionExtra) > 0 {
		merged := make(map[string]any, len(newConfig.Params.ExtraFields)+len(sessionExtra))
		for k, v := range newConfig.Params.ExtraFields {
			merged[k] = v
		}
		for k, v := range sessionExtra {
			merged[k] = v
		}
		newConfig.Params.ExtraFields = merged
	}

	// 创建新模型
	newModel, err := CreateChatModel(newConfig, w.modelOptions)
	if err != nil {
		if w.logger != nil {
			w.logger.Warnf("[DynamicModelWrapper] Failed to create model %s: %v, using default model", newConfig.Model, err)
		}
		return w.baseModel
	}
	// 重建的模型默认未绑工具，需重新绑定（否则 session 级切换模型/思考后工具调用失效）
	if len(w.tools) > 0 {
		if tm, err := newModel.WithTools(w.tools); err == nil {
			newModel = tm
		} else if w.logger != nil {
			w.logger.Warnf("[DynamicModelWrapper] WithTools on rebuilt model failed: %v", err)
		}
	}

	// 缓存（达到上限时不再缓存，但仍返回新模型）
	if atomic.LoadInt32(w.cacheCount) < maxCachedModels {
		w.modelCache.Store(cacheKey, newModel)
		atomic.AddInt32(w.cacheCount, 1)
	}

	return newModel
}

// Generate 生成方法
func (w *DynamicModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	sessionModel := SessionModelFromContext(ctx)
	// 覆盖 opts 里可能存在的 WithModel（eino compose 会注入节点默认 model），
	// 否则会盖掉 getModelForContext 按 session_model 重建的模型
	if sessionModel != "" {
		opts = append(opts, model.WithModel(sessionModel))
	}
	m := w.getModelForContext(ctx)
	return m.Generate(ctx, input, opts...)
}

// Stream 流式生成方法
func (w *DynamicModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sessionModel := SessionModelFromContext(ctx)
	if sessionModel != "" {
		opts = append(opts, model.WithModel(sessionModel))
	}
	m := w.getModelForContext(ctx)
	return m.Stream(ctx, input, opts...)
}

// WithTools 设置工具并返回新模型，新实例共享同一个 modelCache
func (w *DynamicModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newModel, err := w.baseModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	// modelCache/cacheCount 为指针，派生实例与原实例共享同一份缓存与计数
	return &DynamicModelWrapper{
		baseModel:    newModel,
		llmConfig:    w.llmConfig,
		modelCache:   w.modelCache,
		cacheCount:   w.cacheCount,
		logger:       w.logger,
		modelOptions: w.modelOptions,
		tools:        tools, // 记住工具，重建模型（切模型/extra 覆盖）时重新绑定
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

// InjectSessionExtraFieldsToContext 从 metadata 中读取 session_extra_fields（JSON 字符串）并注入 context
func InjectSessionExtraFieldsToContext(ctx context.Context, metadata map[string]string) context.Context {
	if metadata == nil {
		return ctx
	}
	if raw, ok := metadata[aspect.MetaSessionExtraFields]; ok && raw != "" {
		var fields map[string]any
		if err := json.Unmarshal([]byte(raw), &fields); err == nil && len(fields) > 0 {
			return ContextWithSessionExtraFields(ctx, fields)
		}
	}
	return ctx
}

// extraFieldsHash 对扩展参数 map 计算稳定哈希（key 排序后拼接，避免 map 遍历顺序不定）。
// 用于动态模型缓存键，使"同模型 + 同 extra"命中缓存、不必每条消息重建模型。
func extraFieldsHash(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New32a()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{'='})
		fmt.Fprintf(h, "%v", m[k])
		h.Write([]byte{';'})
	}
	return strconv.FormatUint(uint64(h.Sum32()), 36)
}
