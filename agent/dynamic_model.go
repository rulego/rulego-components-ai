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

// contextKeySessionModel is the key of session_model in context
type contextKeySessionModel struct{}

// SessionModelFromContext retrieves session_model from the context
func SessionModelFromContext(ctx context.Context) string {
	if v := ctx.Value(contextKeySessionModel{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ContextWithSessionModel injects session_model into the context
func ContextWithSessionModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, contextKeySessionModel{}, model)
}

// contextKeySessionExtraFields is the key to session_extra_fields in context
type contextKeySessionExtraFields struct{}

// SessionExtraFieldsFromContext Obtain session-level extended parameter coverage (such as thought intensity) from context
func SessionExtraFieldsFromContext(ctx context.Context) map[string]any {
	if v := ctx.Value(contextKeySessionExtraFields{}); v != nil {
		if m, ok := v.(map[string]any); ok {
			return m
		}
	}
	return nil
}

// ContextWithSessionExtraFields injects conversation-level extension parameters into context
func ContextWithSessionExtraFields(ctx context.Context, fields map[string]any) context.Context {
	if len(fields) == 0 {
		return ctx
	}
	return context.WithValue(ctx, contextKeySessionExtraFields{}, fields)
}

// maxCachedModels: The maximum number of dynamic model caches
const maxCachedModels = 32

// DynamicModelWrapper
// Supports dynamic model switching based on the session_model in context
type DynamicModelWrapper struct {
	baseModel    model.ToolCallingChatModel
	llmConfig    config.LLMConfig
	modelCache   *sync.Map // modelID -> model.ToolCallingChatModel (pointer, shared with WithTools derived instances)
	cacheCount   *int32    // Number of models in cache (pointer, shared with WithTools derived instances)
	logger       types.Logger
	modelOptions ModelOptions
	tools        []*schema.ToolInfo // List of bound tools (rebinding when rebuilding the model, otherwise the tool call will become invalid)
}

// NewDynamicModelWrapper creates a dynamic model wrapper
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

// getModelForContext Retrieves the appropriate model based on the context
// Conditions for triggering reconstruction: model cutoff (session_model different from default) or session-level extended parameter coverage (such as thinking intensity)
func (w *DynamicModelWrapper) getModelForContext(ctx context.Context) model.ToolCallingChatModel {
	sessionModel := SessionModelFromContext(ctx)
	sessionExtra := SessionExtraFieldsFromContext(ctx)
	modelChanged := sessionModel != "" && sessionModel != w.llmConfig.Model

	// No model cut, no parameter overlays→ using the base model
	if !modelChanged && len(sessionExtra) == 0 {
		return w.baseModel
	}

	// Cache key = model name + extraFields hash; Same model and extra hit cache to avoid reconstructing every message
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

	// Create a new model configuration
	newConfig := w.llmConfig
	if modelChanged {
		newConfig.Model = sessionModel
	}
	// Session-level extended parameter coverage (such as thought intensity): session override is prioritized and merged into the model's default ExtraFields
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

	// Create new models
	newModel, err := CreateChatModel(newConfig, w.modelOptions)
	if err != nil {
		if w.logger != nil {
			w.logger.Warnf("[DynamicModelWrapper] Failed to create model %s: %v, using default model", newConfig.Model, err)
		}
		return w.baseModel
	}
	// The reconstructed model is not bound to tools by default and must be rebounded (otherwise, after switching models at the session level or thinking about the tool, the call becomes invalid).
	if len(w.tools) > 0 {
		if tm, err := newModel.WithTools(w.tools); err == nil {
			newModel = tm
		} else if w.logger != nil {
			w.logger.Warnf("[DynamicModelWrapper] WithTools on rebuilt model failed: %v", err)
		}
	}

	// Caching (no longer cached when the limit is reached, but still returns to the new model)
	if atomic.LoadInt32(w.cacheCount) < maxCachedModels {
		w.modelCache.Store(cacheKey, newModel)
		atomic.AddInt32(w.cacheCount, 1)
	}

	return newModel
}

// Generate Generation Method
func (w *DynamicModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	sessionModel := SessionModelFromContext(ctx)
	// Override possible WithModel in opts (eino compose injects the node's default model),
	// Otherwise, the model reconstructed by getModelForContext with session_model will be overwhelmed
	if sessionModel != "" {
		opts = append(opts, model.WithModel(sessionModel))
	}
	m := w.getModelForContext(ctx)
	return m.Generate(ctx, input, opts...)
}

// Stream generation method
func (w *DynamicModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sessionModel := SessionModelFromContext(ctx)
	if sessionModel != "" {
		opts = append(opts, model.WithModel(sessionModel))
	}
	m := w.getModelForContext(ctx)
	return m.Stream(ctx, input, opts...)
}

// WithTools sets up the tool and returns a new model; the new instance shares the same modelCache
func (w *DynamicModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newModel, err := w.baseModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	// modelCache/cacheCount are pointers, and the derived instance shares the same cache and count with the original instance
	return &DynamicModelWrapper{
		baseModel:    newModel,
		llmConfig:    w.llmConfig,
		modelCache:   w.modelCache,
		cacheCount:   w.cacheCount,
		logger:       w.logger,
		modelOptions: w.modelOptions,
		tools:        tools, // Remember the tools and rebind when rebuilding the model (cutting the model/extra override).
	}, nil
}

// Ensure DynamicModelWrapper implements model.ToolCallingChatModel
var _ model.ToolCallingChatModel = (*DynamicModelWrapper)(nil)

// ============================================
// AgentAwareModelWrapper - Supports model switching at the agent level
// ============================================

// AgentAwareModelWrapper supports dynamic switching of the model's wrapper during agent execution
// Unlike DynamicModelWrapper, it supports wrapping models when creating a React Agent
type AgentAwareModelWrapper struct {
	*DynamicModelWrapper
}

// NewAgentAwareModelWrapper creates a model wrapper that supports Agents
func NewAgentAwareModelWrapper(baseModel model.ToolCallingChatModel, llmConfig config.LLMConfig, opts ModelOptions) *AgentAwareModelWrapper {
	return &AgentAwareModelWrapper{
		DynamicModelWrapper: NewDynamicModelWrapper(baseModel, llmConfig, opts),
	}
}

// WithTools setup tool
func (w *AgentAwareModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	// Delegate to DynamicModelWrapper
	newModel, err := w.DynamicModelWrapper.WithTools(tools)
	if err != nil {
		return nil, err
	}

	// If the returned DynamicModelWrapper is AgentAwareModelWrapper
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

// WrapModelWithDynamicSupport Wrap models to support dynamic switching
// If a logger is provided, it will output logs when switching models
func WrapModelWithDynamicSupport(baseModel model.ToolCallingChatModel, llmConfig config.LLMConfig, opts ModelOptions) model.ToolCallingChatModel {
	return NewAgentAwareModelWrapper(baseModel, llmConfig, opts)
}

// InjectSessionModelToContext injects session_model into the context as an auxiliary function
// You can call it before executing the Agent
func InjectSessionModelToContext(ctx context.Context, metadata map[string]string) context.Context {
	if metadata == nil {
		return ctx
	}

	// Reading session_model from metadata
	if sessionModel, ok := metadata[aspect.MetaSessionModel]; ok && sessionModel != "" {
		return ContextWithSessionModel(ctx, sessionModel)
	}

	return ctx
}

// InjectSessionExtraFieldsToContext reads the session_extra_fields (JSON string) from metadata and injects context into it
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

// extraFieldsHash calculates a stable hash for the extension parameter map (key sorted and concatenated to avoid uncertain map traversal order).
// Used for dynamic model caching keys, allowing "same model + same extra" to hit the cache, eliminating the need to rebuild the model for each message.
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
