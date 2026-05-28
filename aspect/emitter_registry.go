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
	"sync"
)

// globalEmitterRegistry 全局 Emitter 注册表
// 用于定时任务等场景下获取 emitter（当 context 中没有时）
var globalEmitterRegistry = &EmitterRegistry{
	emitters: make(map[string]EventEmitter),
}

// EmitterRegistry Emitter 注册表
type EmitterRegistry struct {
	mu       sync.RWMutex
	emitters map[string]EventEmitter // chainId -> emitter
}

// RegisterEmitter 注册 emitter 到全局注册表
func RegisterEmitter(chainId string, emitter EventEmitter) {
	globalEmitterRegistry.mu.Lock()
	defer globalEmitterRegistry.mu.Unlock()
	globalEmitterRegistry.emitters[chainId] = emitter
}

// UnregisterEmitter 从全局注册表注销 emitter
func UnregisterEmitter(chainId string) {
	globalEmitterRegistry.mu.Lock()
	defer globalEmitterRegistry.mu.Unlock()
	delete(globalEmitterRegistry.emitters, chainId)
}

// GetEmitterFromRegistry 从全局注册表获取 emitter
func GetEmitterFromRegistry(chainId string) (EventEmitter, bool) {
	globalEmitterRegistry.mu.RLock()
	defer globalEmitterRegistry.mu.RUnlock()
	emitter, ok := globalEmitterRegistry.emitters[chainId]
	return emitter, ok
}

// GetEmitterWithFallback 优先从 context 获取 emitter，如果没有则从全局注册表获取
// chainId 用于从全局注册表查找
func GetEmitterWithFallback(ctx context.Context, chainId string) (EventEmitter, bool) {
	// 首先尝试从 context 获取
	if emitter, ok := GetEmitter(ctx); ok {
		return emitter, true
	}
	// 然后从全局注册表获取
	return GetEmitterFromRegistry(chainId)
}
