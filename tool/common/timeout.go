package common

import (
	"context"
	"sync"
	"time"
)

// globalTimeoutConfig 全局超时配置
var globalTimeoutConfig = DefaultTimeoutConfig()
var globalTimeoutMu sync.RWMutex

// TimeoutConfig holds timeout configuration for tool execution.
type TimeoutConfig struct {
	// DefaultTimeout default timeout for tool execution
	DefaultTimeout time.Duration `json:"defaultTimeout"`

	// MaxTimeout maximum allowed timeout
	MaxTimeout time.Duration `json:"maxTimeout"`

	// ReadTimeout timeout for read operations
	ReadTimeout time.Duration `json:"readTimeout"`

	// WriteTimeout timeout for write operations
	WriteTimeout time.Duration `json:"writeTimeout"`

	// SearchTimeout timeout for search operations
	SearchTimeout time.Duration `json:"searchTimeout"`

	// EditTimeout timeout for edit operations
	EditTimeout time.Duration `json:"editTimeout"`

	// BrowserTimeout timeout for browser automation operations
	// Browser tools need longer timeouts due to page loading, JavaScript execution, etc.
	BrowserTimeout time.Duration `json:"browserTimeout"`

	// HTTPTimeout timeout for HTTP requests
	HTTPTimeout time.Duration `json:"httpTimeout"`
}

// DefaultTimeoutConfig returns default timeout configuration.
func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		DefaultTimeout: 30 * time.Second,
		MaxTimeout:     8 * time.Minute,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		SearchTimeout:  60 * time.Second,
		EditTimeout:    30 * time.Second,
		BrowserTimeout: 5 * time.Minute,
		HTTPTimeout:    30 * time.Second,
	}
}

// GetTimeoutConfig 获取全局超时配置
func GetTimeoutConfig() TimeoutConfig {
	globalTimeoutMu.RLock()
	defer globalTimeoutMu.RUnlock()
	return globalTimeoutConfig
}

// SetTimeoutConfig 设置全局超时配置
func SetTimeoutConfig(cfg TimeoutConfig) {
	globalTimeoutMu.Lock()
	defer globalTimeoutMu.Unlock()
	globalTimeoutConfig = cfg
}

// SetTimeout 设置指定类别的超时时间
func SetTimeout(category string, timeout time.Duration) {
	globalTimeoutMu.Lock()
	defer globalTimeoutMu.Unlock()
	switch category {
	case "default":
		globalTimeoutConfig.DefaultTimeout = timeout
	case "read":
		globalTimeoutConfig.ReadTimeout = timeout
	case "write":
		globalTimeoutConfig.WriteTimeout = timeout
	case "search":
		globalTimeoutConfig.SearchTimeout = timeout
	case "edit":
		globalTimeoutConfig.EditTimeout = timeout
	case "browser":
		globalTimeoutConfig.BrowserTimeout = timeout
	case "http":
		globalTimeoutConfig.HTTPTimeout = timeout
	}
}

// GetTimeout returns the appropriate timeout for a tool category.
// If the specific timeout is not set (0), returns DefaultTimeout.
func (c TimeoutConfig) GetTimeout(category string) time.Duration {
	var timeout time.Duration
	switch category {
	case "read":
		timeout = c.ReadTimeout
	case "write":
		timeout = c.WriteTimeout
	case "search":
		timeout = c.SearchTimeout
	case "edit":
		timeout = c.EditTimeout
	case "browser":
		timeout = c.BrowserTimeout
	case "http":
		timeout = c.HTTPTimeout
	default:
		timeout = c.DefaultTimeout
	}

	if timeout <= 0 {
		return c.DefaultTimeout
	}
	return timeout
}

// GetGlobalTimeout 获取指定类别的全局超时时间
func GetGlobalTimeout(category string) time.Duration {
	return GetTimeoutConfig().GetTimeout(category)
}

// WithTimeout executes a function with timeout.
// Returns a timeout error if the function doesn't complete in time.
func WithTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) (string, error)) (string, error) {
	if timeout <= 0 {
		return fn(ctx)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultCh := make(chan struct {
		result string
		err    error
	}, 1)

	go func() {
		result, err := fn(ctx)
		resultCh <- struct {
			result string
			err    error
		}{result, err}
	}()

	select {
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return "", NewErrorf(ErrCodeTimeout, "operation timed out after %v", timeout)
		}
		return "", ctx.Err()
	case res := <-resultCh:
		return res.result, res.err
	}
}

// WithTimeoutSimple executes a function with timeout and returns formatted result.
func WithTimeoutSimple(ctx context.Context, timeout time.Duration, fn func(context.Context) (string, error)) string {
	result, err := WithTimeout(ctx, timeout, fn)
	if err != nil {
		if toolErr, ok := err.(*ToolError); ok {
			return toolErr.Error()
		}
		return NewError(ErrCodeTimeout, err.Error()).Error()
	}
	return result
}

// TimeoutError creates a timeout error.
func TimeoutError(timeout time.Duration) *ToolError {
	return NewErrorf(ErrCodeTimeout, "operation timed out after %v", timeout)
}

// IsTimeout checks if an error is a timeout error.
func IsTimeout(err error) bool {
	return IsCode(err, ErrCodeTimeout) || err == context.DeadlineExceeded
}

// GetContextTimeout returns the remaining time until the context deadline.
// Returns 0 if the context has no deadline or has already expired.
func GetContextTimeout(ctx context.Context) time.Duration {
	if ctx == nil {
		return 0
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
}

// WithContextOrTimeout executes a function with either the context's deadline
// or the specified timeout, whichever is shorter.
// This is useful for browser tools that may have long-running operations.
func WithContextOrTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) (string, error)) (string, error) {
	ctxTimeout := GetContextTimeout(ctx)
	if ctxTimeout > 0 && ctxTimeout < timeout {
		timeout = ctxTimeout
	}

	return WithTimeout(ctx, timeout, fn)
}

// NoTimeout is a sentinel value indicating no timeout should be applied.
const NoTimeout time.Duration = -1

// WithOptionalTimeout executes a function with optional timeout.
// If timeout is NoTimeout (-1), the function runs without timeout.
// If timeout is 0, uses DefaultTimeout from DefaultTimeoutConfig().
func WithOptionalTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) (string, error)) (string, error) {
	if timeout == NoTimeout {
		return fn(ctx)
	}
	if timeout == 0 {
		timeout = DefaultTimeoutConfig().DefaultTimeout
	}
	return WithTimeout(ctx, timeout, fn)
}
