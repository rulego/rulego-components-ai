package errors

import (
	"errors"
	"testing"
)

func TestAgentError(t *testing.T) {
	tests := []struct {
		name     string
		err      *AgentError
		wantCode ErrorCode
		wantMsg  string
		wantRetry bool
	}{
		{
			name:     "simple error",
			err:      New(CodeInvalidInput, "input is empty"),
			wantCode: CodeInvalidInput,
			wantMsg:  "input is empty",
			wantRetry: false,
		},
		{
			name:     "error with cause",
			err:      Wrap(CodeLLMCallFailed, "LLM failed", errors.New("connection refused")),
			wantCode: CodeLLMCallFailed,
			wantMsg:  "LLM failed",
			wantRetry: false,
		},
		{
			name:     "retryable error",
			err:      LLMTimeout(),
			wantCode: CodeLLMTimeout,
			wantMsg:  "LLM request timeout",
			wantRetry: true,
		},
		{
			name:     "rate limit error",
			err:      LLMRateLimit(),
			wantCode: CodeLLMRateLimit,
			wantMsg:  "LLM rate limit exceeded",
			wantRetry: true,
		},
		{
			name:     "tool not found",
			err:      ToolNotFound("bash"),
			wantCode: CodeToolNotFound,
			wantMsg:  "tool not found: bash",
			wantRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check code
			if got := GetCode(tt.err); got != tt.wantCode {
				t.Errorf("GetCode() = %v, want %v", got, tt.wantCode)
			}

			// Check retryable
			if got := IsRetryable(tt.err); got != tt.wantRetry {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.wantRetry)
			}

			// Check error message contains expected text
			if tt.wantMsg != "" && !contains(tt.err.Error(), tt.wantMsg) {
				t.Errorf("Error() = %v, want to contain %v", tt.err.Error(), tt.wantMsg)
			}

			// Check IsCode
			if !IsCode(tt.err, tt.wantCode) {
				t.Errorf("IsCode() = false for code %v", tt.wantCode)
			}
		})
	}
}

func TestAgentErrorWithCause(t *testing.T) {
	cause := errors.New("original error")
	err := Wrap(CodeInternalError, "something went wrong", cause)

	// Test Unwrap
	unwrapped := err.Unwrap()
	if unwrapped != cause {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}

	// Test Error() includes cause
	errStr := err.Error()
	if !contains(errStr, "original error") {
		t.Errorf("Error() should contain cause, got: %v", errStr)
	}
}

func TestAgentErrorWithDetails(t *testing.T) {
	err := New(CodeInvalidInput, "invalid input").
		WithDetails(map[string]string{"field": "name", "value": ""})

	if err.Details == nil {
		t.Error("WithDetails() should set Details")
	}
}

func TestIsLLMError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantLLM bool
	}{
		{
			name:    "LLM error",
			err:     LLMTimeout(),
			wantLLM: true,
		},
		{
			name:    "tool error",
			err:     ToolNotFound("test"),
			wantLLM: false,
		},
		{
			name:    "generic error",
			err:     errors.New("generic error"),
			wantLLM: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLLMError(tt.err); got != tt.wantLLM {
				t.Errorf("IsLLMError() = %v, want %v", got, tt.wantLLM)
			}
		})
	}
}

func TestFromString(t *testing.T) {
	tests := []struct {
		name       string
		input      error
		wantRetry  bool
	}{
		{
			name:      "timeout error",
			input:     errors.New("request timeout exceeded"),
			wantRetry: true,
		},
		{
			name:      "rate limit error",
			input:     errors.New("rate limit exceeded"),
			wantRetry: true,
		},
		{
			name:      "connection error",
			input:     errors.New("connection refused"),
			wantRetry: true,
		},
		{
			name:      "generic error",
			input:     errors.New("something went wrong"),
			wantRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := FromString(tt.input)
			if err == nil {
				t.Fatal("FromString() returned nil")
			}
			if got := IsRetryable(err); got != tt.wantRetry {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.wantRetry)
			}
		})
	}
}

func TestErrorCodeString(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want string
	}{
		{CodeSuccess, "Success"},
		{CodeInvalidInput, "InvalidInput"},
		{CodeLLMTimeout, "LLMTimeout"},
		{CodeToolNotFound, "ToolNotFound"},
		{ErrorCode(99999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.code.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(s) > len(substr) && containsMiddle(s, substr))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
