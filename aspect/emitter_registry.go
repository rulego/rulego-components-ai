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

// globalEmitterRegistry Global Emitter registry
// Used to obtain emitters in scenarios such as scheduled tasks (when not present in context)
var globalEmitterRegistry = &EmitterRegistry{
	emitters: make(map[string]EventEmitter),
}

// EmitterRegistry Emitter registry
type EmitterRegistry struct {
	mu       sync.RWMutex
	emitters map[string]EventEmitter // chainId -> emitter
}

// RegisterEmitter Registers emitters into the global registry
func RegisterEmitter(chainId string, emitter EventEmitter) {
	globalEmitterRegistry.mu.Lock()
	defer globalEmitterRegistry.mu.Unlock()
	globalEmitterRegistry.emitters[chainId] = emitter
}

// UnregisterEmitter Deletes an emitter from the global registry
func UnregisterEmitter(chainId string) {
	globalEmitterRegistry.mu.Lock()
	defer globalEmitterRegistry.mu.Unlock()
	delete(globalEmitterRegistry.emitters, chainId)
}

// GetEmitterFromRegistry Retrieves emitters from the global registry
func GetEmitterFromRegistry(chainId string) (EventEmitter, bool) {
	globalEmitterRegistry.mu.RLock()
	defer globalEmitterRegistry.mu.RUnlock()
	emitter, ok := globalEmitterRegistry.emitters[chainId]
	return emitter, ok
}

// GetEmitterWithFallback first retrieves emitters from the context; if not, they get them from the global registry
// chainId is used to search from the global registry
func GetEmitterWithFallback(ctx context.Context, chainId string) (EventEmitter, bool) {
	// First, try to get it from the context
	if emitter, ok := GetEmitter(ctx); ok {
		return emitter, true
	}
	// Then retrieve it from the global registry
	return GetEmitterFromRegistry(chainId)
}
