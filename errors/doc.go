// Package errors provides unified error handling for AI components.
//
// This package defines:
//   - Standardized error codes (ErrorCode)
//   - Structured error type (AgentError)
//   - Helper functions for common errors
//   - Error classification utilities
//
// Usage:
//
//	import "github.com/rulego/rulego-components-ai/errors"
//
//	// Create a simple error
//	err := errors.InvalidInput("name cannot be empty")
//
//	// Create an error with cause
//	err := errors.LLMCallFailed(originalErr)
//
//	// Check error type
//	if errors.IsRetryable(err) {
//	    // retry logic
//	}
//
//	// Check error code
//	if errors.IsCode(err, errors.CodeLLMTimeout) {
//	    // handle timeout
//	}
package errors
