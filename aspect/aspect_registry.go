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

// globalAspectRegistry Global Aspect Registry
// Supports dynamic application-level aspect registration shared by all Agent instances.
var globalAspectRegistry = &AspectRegistry{
	aspects: make(map[string]Aspect),
}

// AspectRegistry Aspect Registry
type AspectRegistry struct {
	mu      sync.RWMutex
	aspects map[string]Aspect // name -> Aspect
}

// RegisterAspect to register a face to the global registry
// If the facet with the same name already exists, it will be overwritten
//
// Parameters:
//   - name: Section name, used for identification and management
//   - a: Section instance
//
// Example:
//
//	aspect.RegisterAspect("logging", builtin.NewLoggingAspect(logger))
//	aspect.RegisterAspect("viz", builtin.NewVizAspect())
func RegisterAspect(name string, a Aspect) {
	globalAspectRegistry.mu.Lock()
	defer globalAspectRegistry.mu.Unlock()
	globalAspectRegistry.aspects[name] = a
}

// UnregisterAspect to log off the face from the global registry
//
// Parameters:
//   - name: The name of the face
func UnregisterAspect(name string) {
	globalAspectRegistry.mu.Lock()
	defer globalAspectRegistry.mu.Unlock()
	delete(globalAspectRegistry.aspects, name)
}

// GetGlobalAspects retrieves all registered aspects.
// Returns copies of the section list, in the order they were registered
//
// Back:
//   - []Aspect: All registered facet instances
func GetGlobalAspects() []Aspect {
	globalAspectRegistry.mu.RLock()
	defer globalAspectRegistry.mu.RUnlock()
	result := make([]Aspect, 0, len(globalAspectRegistry.aspects))
	for _, a := range globalAspectRegistry.aspects {
		result = append(result, a)
	}
	return result
}

// HasAspect checks whether the facet with the specified name has been registered
//
// Parameters:
//   - name: The name of the face
//
// Back:
//   - bool: Whether registered
func HasAspect(name string) bool {
	globalAspectRegistry.mu.RLock()
	defer globalAspectRegistry.mu.RUnlock()
	_, ok := globalAspectRegistry.aspects[name]
	return ok
}

// ClearAspects clears all registered aspects.
// Mainly used for testing scenarios
func ClearAspects() {
	globalAspectRegistry.mu.Lock()
	defer globalAspectRegistry.mu.Unlock()
	globalAspectRegistry.aspects = make(map[string]Aspect)
}
