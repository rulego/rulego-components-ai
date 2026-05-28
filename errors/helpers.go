package errors

import (
	"strings"
)

// Common error constructors for convenience.

// Input errors
func InvalidInput(message string) *AgentError {
	return New(CodeInvalidInput, message)
}

func MissingParameter(param string) *AgentError {
	return Newf(CodeMissingParameter, "missing required parameter: %s", param)
}

func EmptyInput() *AgentError {
	return New(CodeEmptyInput, "input cannot be empty")
}

func InvalidConfig(message string) *AgentError {
	return New(CodeInvalidConfig, message)
}

// LLM errors
func LLMCallFailed(cause error) *AgentError {
	return Wrap(CodeLLMCallFailed, "LLM call failed", cause).
		WithRetryable(true)
}

func LLMTimeout() *AgentError {
	return New(CodeLLMTimeout, "LLM request timeout").
		WithRetryable(true)
}

func LLMRateLimit() *AgentError {
	return New(CodeLLMRateLimit, "LLM rate limit exceeded").
		WithRetryable(true)
}

func LLMInvalidResponse(cause error) *AgentError {
	return Wrap(CodeLLMInvalidResponse, "invalid LLM response", cause)
}

func LLMContextTooLong() *AgentError {
	return New(CodeLLMContextTooLong, "context length exceeds limit")
}

// Tool errors
func ToolNotFound(name string) *AgentError {
	return Newf(CodeToolNotFound, "tool not found: %s", name)
}

func ToolExecutionFail(toolName string, cause error) *AgentError {
	return Wrap(CodeToolExecutionFail, "tool execution failed: "+toolName, cause)
}

func ToolTimeout(toolName string) *AgentError {
	return Newf(CodeToolTimeout, "tool execution timeout: %s", toolName).
		WithRetryable(true)
}

func ToolInvalidInput(toolName, reason string) *AgentError {
	return Newf(CodeToolInvalidInput, "invalid input for tool %s: %s", toolName, reason)
}

// Storage errors
func StorageConnectFail(cause error) *AgentError {
	return Wrap(CodeStorageConnectFail, "failed to connect to storage", cause).
		WithRetryable(true)
}

func StorageQueryFail(cause error) *AgentError {
	return Wrap(CodeStorageQueryFail, "storage query failed", cause)
}

func StorageNotFound(resource string) *AgentError {
	return Newf(CodeStorageNotFound, "resource not found: %s", resource)
}

// System errors
func InternalError(cause error) *AgentError {
	return Wrap(CodeInternalError, "internal error", cause)
}

func NotImplemented(feature string) *AgentError {
	return Newf(CodeNotImplemented, "feature not implemented: %s", feature)
}

func ServiceUnavailable(service string) *AgentError {
	return Newf(CodeServiceUnavailable, "service unavailable: %s", service).
		WithRetryable(true)
}

// IsLLMError checks if the error is an LLM-related error.
func IsLLMError(err error) bool {
	code := GetCode(err)
	return code >= CodeLLMCallFailed && code <= CodeLLMNotAvailable
}

// IsToolError checks if the error is a tool-related error.
func IsToolError(err error) bool {
	code := GetCode(err)
	return code >= CodeToolNotFound && code <= CodeToolNotRegistered
}

// IsStorageError checks if the error is a storage-related error.
func IsStorageError(err error) bool {
	code := GetCode(err)
	return code >= CodeStorageConnectFail && code <= CodeStorageNotFound
}

// FromString creates an AgentError from error string patterns.
// This is useful for handling errors from external libraries.
func FromString(err error) *AgentError {
	if err == nil {
		return nil
	}

	errStr := err.Error()
	errLower := strings.ToLower(errStr)

	// Check for common patterns
	switch {
	case strings.Contains(errLower, "timeout"):
		return LLMTimeout().WithCause(err)
	case strings.Contains(errLower, "rate limit"):
		return LLMRateLimit().WithCause(err)
	case strings.Contains(errLower, "context too long") || strings.Contains(errLower, "token limit"):
		return LLMContextTooLong().WithCause(err)
	case strings.Contains(errLower, "connection refused") || strings.Contains(errLower, "dial tcp"):
		return StorageConnectFail(err)
	default:
		return InternalError(err)
	}
}
