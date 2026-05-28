// Package errors provides unified error handling for AI components.
package errors

// ErrorCode defines standardized error codes for AI components.
type ErrorCode int

const (
	// Success
	CodeSuccess ErrorCode = 0

	// Input validation errors (10000-10999)
	CodeInvalidInput      ErrorCode = 10001
	CodeMissingParameter  ErrorCode = 10002
	CodeInvalidFormat     ErrorCode = 10003
	CodeEmptyInput        ErrorCode = 10004
	CodeInvalidConfig     ErrorCode = 10005

	// LLM errors (20000-20999)
	CodeLLMCallFailed     ErrorCode = 20001
	CodeLLMTimeout        ErrorCode = 20002
	CodeLLMRateLimit      ErrorCode = 20003
	CodeLLMInvalidResponse ErrorCode = 20004
	CodeLLMContextTooLong ErrorCode = 20005
	CodeLLMNotAvailable   ErrorCode = 20006

	// Tool execution errors (30000-30999)
	CodeToolNotFound      ErrorCode = 30001
	CodeToolExecutionFail ErrorCode = 30002
	CodeToolTimeout       ErrorCode = 30003
	CodeToolInvalidInput  ErrorCode = 30004
	CodeToolNotRegistered ErrorCode = 30005

	// Storage errors (40000-40999)
	CodeStorageConnectFail ErrorCode = 40001
	CodeStorageQueryFail   ErrorCode = 40002
	CodeStorageWriteFail   ErrorCode = 40003
	CodeStorageNotFound    ErrorCode = 40004

	// System errors (50000-50999)
	CodeInternalError     ErrorCode = 50001
	CodeNotImplemented    ErrorCode = 50002
	CodeServiceUnavailable ErrorCode = 50003
	CodeUnknownError      ErrorCode = 50004
)

// String returns the string representation of the error code.
func (c ErrorCode) String() string {
	switch c {
	case CodeSuccess:
		return "Success"
	case CodeInvalidInput:
		return "InvalidInput"
	case CodeMissingParameter:
		return "MissingParameter"
	case CodeInvalidFormat:
		return "InvalidFormat"
	case CodeEmptyInput:
		return "EmptyInput"
	case CodeInvalidConfig:
		return "InvalidConfig"
	case CodeLLMCallFailed:
		return "LLMCallFailed"
	case CodeLLMTimeout:
		return "LLMTimeout"
	case CodeLLMRateLimit:
		return "LLMRateLimit"
	case CodeLLMInvalidResponse:
		return "LLMInvalidResponse"
	case CodeLLMContextTooLong:
		return "LLMContextTooLong"
	case CodeLLMNotAvailable:
		return "LLMNotAvailable"
	case CodeToolNotFound:
		return "ToolNotFound"
	case CodeToolExecutionFail:
		return "ToolExecutionFail"
	case CodeToolTimeout:
		return "ToolTimeout"
	case CodeToolInvalidInput:
		return "ToolInvalidInput"
	case CodeToolNotRegistered:
		return "ToolNotRegistered"
	case CodeStorageConnectFail:
		return "StorageConnectFail"
	case CodeStorageQueryFail:
		return "StorageQueryFail"
	case CodeStorageWriteFail:
		return "StorageWriteFail"
	case CodeStorageNotFound:
		return "StorageNotFound"
	case CodeInternalError:
		return "InternalError"
	case CodeNotImplemented:
		return "NotImplemented"
	case CodeServiceUnavailable:
		return "ServiceUnavailable"
	case CodeUnknownError:
		return "UnknownError"
	default:
		return "Unknown"
	}
}
