package common

import "fmt"

// Result is a unified response structure for tool operations.
// Note: For LLM consumption, use the plain text format via String() method
// instead of JSON to save tokens and improve comprehension.
type Result struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// NewResult creates a new Result.
func NewResult(success bool, message string, data ...any) *Result {
	r := &Result{
		Success: success,
		Message: message,
	}
	if len(data) > 0 {
		r.Data = data[0]
	}
	return r
}

// SuccessResult creates a successful result.
func SuccessResult(message string, data ...any) *Result {
	return NewResult(true, message, data...)
}

// ErrorResult creates an error result from a ToolError.
func ErrorResult(err *ToolError) *Result {
	return NewResult(false, err.Error())
}

// ErrorResultFromCode creates an error result from error code and detail.
func ErrorResultFromCode(code ErrorCode, detail string) *Result {
	return ErrorResult(NewError(code, detail))
}

// ErrorResultFromString creates an error result from a plain string message.
// Deprecated: Use ErrorResult with ToolError for better error handling.
func ErrorResultFromString(message string) *Result {
	return NewResult(false, message)
}

// String returns a simple text format for LLM consumption.
func (r *Result) String() string {
	if r.Success {
		return fmt.Sprintf("Success: %s", r.Message)
	}
	return r.Message // Error message already has "Error:" prefix from ToolError
}

// ToJSON converts the result to a JSON string.
func (r *Result) ToJSON() string {
	b, _ := MarshalJSON(r)
	return string(b)
}

// ToJSONCompact converts the result to a compact JSON string.
func (r *Result) ToJSONCompact() string {
	b, _ := MarshalJSONCompact(r)
	return string(b)
}

// WithData adds data to the result and returns it.
func (r *Result) WithData(data any) *Result {
	r.Data = data
	return r
}

// Success returns a simple success message string for LLM consumption.
func Success(format string, args ...interface{}) string {
	return fmt.Sprintf("Success: "+format, args...)
}

// SuccessWithData returns a success message with data info.
func SuccessWithData(message string, data map[string]any) string {
	if path, ok := data["path"].(string); ok {
		return fmt.Sprintf("Success: %s (%s)", message, path)
	}
	return fmt.Sprintf("Success: %s", message)
}

// Fail returns a simple error message string from a ToolError.
func Fail(err *ToolError) string {
	return err.Error()
}

// FailFromCode returns an error message from error code and detail.
func FailFromCode(code ErrorCode, detail string) string {
	return NewError(code, detail).Error()
}
