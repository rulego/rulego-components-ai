package errors

import (
	"encoding/json"
	"errors"
	"fmt"
)

// AgentError represents a structured error with code, message, and context.
type AgentError struct {
	Code      ErrorCode       `json:"code"`
	Message   string          `json:"message"`
	Details   json.RawMessage `json:"details,omitempty"`
	Cause     error           `json:"-"`
	Retryable bool            `json:"retryable"`
}

// Error implements the error interface.
func (e *AgentError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%d/%s] %s: %v", e.Code, e.Code.String(), e.Message, e.Cause)
	}
	return fmt.Sprintf("[%d/%s] %s", e.Code, e.Code.String(), e.Message)
}

// Unwrap returns the underlying cause of the error.
func (e *AgentError) Unwrap() error {
	return e.Cause
}

// Is implements errors.Is interface for comparison.
func (e *AgentError) Is(target error) bool {
	t, ok := target.(*AgentError)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// WithCause adds a cause to the error.
func (e *AgentError) WithCause(cause error) *AgentError {
	e.Cause = cause
	return e
}

// WithDetails adds details to the error.
func (e *AgentError) WithDetails(details any) *AgentError {
	if data, err := json.Marshal(details); err == nil {
		e.Details = data
	}
	return e
}

// WithRetryable sets whether the error is retryable.
func (e *AgentError) WithRetryable(retryable bool) *AgentError {
	e.Retryable = retryable
	return e
}

// New creates a new AgentError with the given code and message.
func New(code ErrorCode, message string) *AgentError {
	return &AgentError{
		Code:    code,
		Message: message,
	}
}

// Newf creates a new AgentError with formatted message.
func Newf(code ErrorCode, format string, args ...any) *AgentError {
	return &AgentError{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}

// Wrap wraps an existing error with code and message.
func Wrap(code ErrorCode, message string, cause error) *AgentError {
	return &AgentError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// IsRetryable checks if the error is retryable.
func IsRetryable(err error) bool {
	var ae *AgentError
	if errors.As(err, &ae) {
		return ae.Retryable
	}
	return false
}

// GetCode extracts the error code from an error.
func GetCode(err error) ErrorCode {
	var ae *AgentError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return CodeUnknownError
}

// IsCode checks if the error has a specific code.
func IsCode(err error, code ErrorCode) bool {
	var ae *AgentError
	if errors.As(err, &ae) {
		return ae.Code == code
	}
	return false
}
