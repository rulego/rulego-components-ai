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

// Package contextx provides generic context utilities.
package contextx

import "context"

// Key is a type-safe context key for storing values of type T.
// Usage:
//
//	var SessionKey = contextx.NewKey[*Session]("session")
//	session, ok := SessionKey.Get(ctx)
//	ctx = SessionKey.With(ctx, session)
type Key[T any] struct {
	name string
}

// NewKey creates a new type-safe context key.
func NewKey[T any](name string) *Key[T] {
	return &Key[T]{name: name}
}

// Name returns the key name for debugging purposes.
func (k *Key[T]) Name() string {
	return k.name
}

// Get retrieves the value from context. Returns zero value and false if not found.
func (k *Key[T]) Get(ctx context.Context) (T, bool) {
	var zero T
	v := ctx.Value(k)
	if v == nil {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// With returns a new context with the value stored.
func (k *Key[T]) With(ctx context.Context, v T) context.Context {
	return context.WithValue(ctx, k, v)
}

// MustGet retrieves the value from context, panics if not found.
func (k *Key[T]) MustGet(ctx context.Context) T {
	v, ok := k.Get(ctx)
	if !ok {
		panic("contextx: key not found: " + k.name)
	}
	return v
}