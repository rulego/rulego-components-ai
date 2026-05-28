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

import "sync"

// globalAspectRegistry 全局切面注册表
// 用于应用层动态注册切面，所有 Agent 实例共享
var globalAspectRegistry = &AspectRegistry{
	aspects: make(map[string]Aspect),
}

// AspectRegistry 切面注册表
type AspectRegistry struct {
	mu      sync.RWMutex
	aspects map[string]Aspect // name -> Aspect
}

// RegisterAspect 注册切面到全局注册表
// 如果同名切面已存在，将被覆盖
//
// 参数:
//   - name: 切面名称，用于标识和管理
//   - a: 切面实例
//
// 示例:
//
//	aspect.RegisterAspect("logging", builtin.NewLoggingAspect(logger))
//	aspect.RegisterAspect("viz", builtin.NewVizAspect())
func RegisterAspect(name string, a Aspect) {
	globalAspectRegistry.mu.Lock()
	defer globalAspectRegistry.mu.Unlock()
	globalAspectRegistry.aspects[name] = a
}

// UnregisterAspect 从全局注册表注销切面
//
// 参数:
//   - name: 切面名称
func UnregisterAspect(name string) {
	globalAspectRegistry.mu.Lock()
	defer globalAspectRegistry.mu.Unlock()
	delete(globalAspectRegistry.aspects, name)
}

// GetGlobalAspects 获取所有已注册的切面
// 返回切面列表的副本，按注册顺序返回
//
// 返回:
//   - []Aspect: 所有已注册的切面实例
func GetGlobalAspects() []Aspect {
	globalAspectRegistry.mu.RLock()
	defer globalAspectRegistry.mu.RUnlock()
	result := make([]Aspect, 0, len(globalAspectRegistry.aspects))
	for _, a := range globalAspectRegistry.aspects {
		result = append(result, a)
	}
	return result
}

// HasAspect 检查指定名称的切面是否已注册
//
// 参数:
//   - name: 切面名称
//
// 返回:
//   - bool: 是否已注册
func HasAspect(name string) bool {
	globalAspectRegistry.mu.RLock()
	defer globalAspectRegistry.mu.RUnlock()
	_, ok := globalAspectRegistry.aspects[name]
	return ok
}

// ClearAspects 清空所有已注册的切面
// 主要用于测试场景
func ClearAspects() {
	globalAspectRegistry.mu.Lock()
	defer globalAspectRegistry.mu.Unlock()
	globalAspectRegistry.aspects = make(map[string]Aspect)
}
