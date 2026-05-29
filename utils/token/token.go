// Package token provides token tracking and estimation utilities for AI components.
package token

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// TokenUsage tracks token consumption for a single operation.
type TokenUsage struct {
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens      int64 `json:"totalTokens"`
}

// Add adds another token usage to this one (thread-safe).
func (tu *TokenUsage) Add(other *TokenUsage) {
	if other == nil {
		return
	}
	atomic.AddInt64(&tu.PromptTokens, other.PromptTokens)
	atomic.AddInt64(&tu.CompletionTokens, other.CompletionTokens)
	atomic.AddInt64(&tu.TotalTokens, other.TotalTokens)
}

// Clone returns a copy of the token usage.
func (tu *TokenUsage) Clone() TokenUsage {
	return TokenUsage{
		PromptTokens:     atomic.LoadInt64(&tu.PromptTokens),
		CompletionTokens: atomic.LoadInt64(&tu.CompletionTokens),
		TotalTokens:      atomic.LoadInt64(&tu.TotalTokens),
	}
}

// TokenTracker tracks token usage across multiple operations.
type TokenTracker struct {
	usage TokenUsage
}

// NewTokenTracker creates a new token tracker.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{}
}

// Record records token usage (thread-safe).
func (t *TokenTracker) Record(prompt, completion int) {
	atomic.AddInt64(&t.usage.PromptTokens, int64(prompt))
	atomic.AddInt64(&t.usage.CompletionTokens, int64(completion))
	atomic.AddInt64(&t.usage.TotalTokens, int64(prompt+completion))
}

// RecordUsage records token usage from a TokenUsage struct.
func (t *TokenTracker) RecordUsage(usage *TokenUsage) {
	if usage == nil {
		return
	}
	t.Record(int(usage.PromptTokens), int(usage.CompletionTokens))
}

// GetUsage returns current token usage (thread-safe).
func (t *TokenTracker) GetUsage() TokenUsage {
	return t.usage.Clone()
}

// Reset resets the token counter.
func (t *TokenTracker) Reset() {
	atomic.StoreInt64(&t.usage.PromptTokens, 0)
	atomic.StoreInt64(&t.usage.CompletionTokens, 0)
	atomic.StoreInt64(&t.usage.TotalTokens, 0)
}

// ToolMetrics tracks metrics for a single tool.
type ToolMetrics struct {
	Name         string `json:"name"`
	CallCount    int64  `json:"callCount"`
	TotalTimeMs  int64  `json:"totalTimeMs"`
	AvgTimeMs    int64  `json:"avgTimeMs"`
	ErrorCount   int64  `json:"errorCount"`
	LastUsed     int64  `json:"lastUsed"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// MetricsCollector collects tool execution metrics.
type MetricsCollector struct {
	mu      sync.RWMutex
	metrics map[string]*ToolMetrics
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		metrics: make(map[string]*ToolMetrics),
	}
}

// Record records a tool execution (thread-safe).
func (mc *MetricsCollector) Record(toolName string, durationMs int64, inputTokens, outputTokens int, hasError bool) {
	mc.mu.Lock()
	m, exists := mc.metrics[toolName]
	if !exists {
		m = &ToolMetrics{Name: toolName}
		mc.metrics[toolName] = m
	}
	mc.mu.Unlock()

	atomic.AddInt64(&m.CallCount, 1)
	atomic.AddInt64(&m.TotalTimeMs, durationMs)
	atomic.AddInt64(&m.InputTokens, int64(inputTokens))
	atomic.AddInt64(&m.OutputTokens, int64(outputTokens))

	if hasError {
		atomic.AddInt64(&m.ErrorCount, 1)
	}

	// Update average (approximate)
	callCount := atomic.LoadInt64(&m.CallCount)
	totalTime := atomic.LoadInt64(&m.TotalTimeMs)
	atomic.StoreInt64(&m.AvgTimeMs, totalTime/callCount)
	atomic.StoreInt64(&m.LastUsed, time.Now().Unix())
}

// GetMetrics returns all collected metrics.
func (mc *MetricsCollector) GetMetrics() map[string]ToolMetrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	result := make(map[string]ToolMetrics, len(mc.metrics))
	for k, v := range mc.metrics {
		result[k] = ToolMetrics{
			Name:         v.Name,
			CallCount:    atomic.LoadInt64(&v.CallCount),
			TotalTimeMs:  atomic.LoadInt64(&v.TotalTimeMs),
			AvgTimeMs:    atomic.LoadInt64(&v.AvgTimeMs),
			ErrorCount:   atomic.LoadInt64(&v.ErrorCount),
			LastUsed:     atomic.LoadInt64(&v.LastUsed),
			InputTokens:  atomic.LoadInt64(&v.InputTokens),
			OutputTokens: atomic.LoadInt64(&v.OutputTokens),
		}
	}
	return result
}

// GetToolMetrics returns metrics for a specific tool.
func (mc *MetricsCollector) GetToolMetrics(toolName string) *ToolMetrics {
	mc.mu.RLock()
	m, ok := mc.metrics[toolName]
	mc.mu.RUnlock()
	if ok {
		return &ToolMetrics{
			Name:         m.Name,
			CallCount:    atomic.LoadInt64(&m.CallCount),
			TotalTimeMs:  atomic.LoadInt64(&m.TotalTimeMs),
			AvgTimeMs:    atomic.LoadInt64(&m.AvgTimeMs),
			ErrorCount:   atomic.LoadInt64(&m.ErrorCount),
			LastUsed:     atomic.LoadInt64(&m.LastUsed),
			InputTokens:  atomic.LoadInt64(&m.InputTokens),
			OutputTokens: atomic.LoadInt64(&m.OutputTokens),
		}
	}
	return nil
}

// Reset clears all metrics.
func (mc *MetricsCollector) Reset() {
	mc.mu.Lock()
	mc.metrics = make(map[string]*ToolMetrics)
	mc.mu.Unlock()
}

// ToJSON returns metrics as JSON string.
func (mc *MetricsCollector) ToJSON() string {
	b, _ := json.MarshalIndent(mc.GetMetrics(), "", "  ")
	return string(b)
}

// EstimateTokens provides a rough token count estimation.
// English: ~4 chars per token, Chinese: ~2 chars per token.
// This is a rough estimate; actual tokenization depends on the model.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}

	// Count Chinese characters
	chineseCount := 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			chineseCount++
		}
	}

	// Non-Chinese characters
	otherCount := len(text) - chineseCount

	// Chinese: ~2 chars per token, English: ~4 chars per token
	return (chineseCount / 2) + (otherCount / 4) + 1
}

// EstimateMessagesTokens estimates tokens for a list of messages.
func EstimateMessagesTokens(messages []string) int {
	total := 0
	for _, msg := range messages {
		total += EstimateTokens(msg)
	}
	// Add overhead for message formatting
	return total + len(messages)*4
}