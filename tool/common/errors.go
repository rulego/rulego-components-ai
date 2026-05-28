// Package common provides shared utilities and constants for AI tools.
package common

import "fmt"

// ErrorCode represents a tool error code for programmatic handling.
type ErrorCode string

const (
	// General errors
	ErrCodeInvalidParams  ErrorCode = "INVALID_PARAMS"
	ErrCodeParseFailed    ErrorCode = "PARSE_FAILED"
	ErrCodeTimeout        ErrorCode = "TIMEOUT"
	ErrCodeCanceled       ErrorCode = "CANCELED"
	ErrCodeNotImplemented ErrorCode = "NOT_IMPLEMENTED"

	// File/Path errors
	ErrCodeFileNotFound      ErrorCode = "FILE_NOT_FOUND"
	ErrCodeFileExists        ErrorCode = "FILE_EXISTS"
	ErrCodePermissionDenied  ErrorCode = "PERMISSION_DENIED"
	ErrCodePathEmpty         ErrorCode = "PATH_EMPTY"
	ErrCodePathInvalid       ErrorCode = "PATH_INVALID"
	ErrCodePathEscape        ErrorCode = "PATH_ESCAPE"
	ErrCodePathIsDirectory   ErrorCode = "PATH_IS_DIRECTORY"
	ErrCodeDirCreateFailed   ErrorCode = "DIR_CREATE_FAILED"
	ErrCodeDirNotEmpty       ErrorCode = "DIR_NOT_EMPTY"

	// Read errors
	ErrCodeReadFailed     ErrorCode = "READ_FAILED"
	ErrCodeContentEmpty   ErrorCode = "CONTENT_EMPTY"
	ErrCodeFileTooLarge   ErrorCode = "FILE_TOO_LARGE"
	ErrCodeLineOutOfRange ErrorCode = "LINE_OUT_OF_RANGE"
	ErrCodeEncodingError  ErrorCode = "ENCODING_ERROR"

	// Write errors
	ErrCodeWriteFailed     ErrorCode = "WRITE_FAILED"
	ErrCodeContentRequired ErrorCode = "CONTENT_REQUIRED"

	// Edit errors
	ErrCodeSearchEmpty        ErrorCode = "SEARCH_EMPTY"
	ErrCodeSearchNotFound     ErrorCode = "SEARCH_NOT_FOUND"
	ErrCodeInsertPosEmpty     ErrorCode = "INSERT_POS_EMPTY"
	ErrCodeInsertPosNotFound  ErrorCode = "INSERT_POS_NOT_FOUND"
	ErrCodeDeleteLinesEmpty   ErrorCode = "DELETE_LINES_EMPTY"
	ErrCodeDeleteLinesInvalid ErrorCode = "DELETE_LINES_INVALID"
	ErrCodeRegexInvalid       ErrorCode = "REGEX_INVALID"
	ErrCodeRegexTooLong       ErrorCode = "REGEX_TOO_LONG"
	ErrCodeBackupNotFound     ErrorCode = "BACKUP_NOT_FOUND"
	ErrCodeBackupFailed       ErrorCode = "BACKUP_FAILED"
	ErrCodeRestoreFailed      ErrorCode = "RESTORE_FAILED"
	ErrCodeVersionInvalid     ErrorCode = "VERSION_INVALID"

	// Search errors
	ErrCodeQueryEmpty    ErrorCode = "QUERY_EMPTY"
	ErrCodePatternInvalid ErrorCode = "PATTERN_INVALID"
	ErrCodeSearchFailed  ErrorCode = "SEARCH_FAILED"

	// Operation errors
	ErrCodeOperationNotSupported ErrorCode = "OPERATION_NOT_SUPPORTED"
)

// ErrorMessages maps error codes to human-readable messages (English for LLM efficiency).
var ErrorMessages = map[ErrorCode]string{
	ErrCodeInvalidParams:  "Invalid parameters",
	ErrCodeParseFailed:    "Failed to parse parameters",
	ErrCodeTimeout:        "Operation timeout",
	ErrCodeCanceled:       "Operation canceled",
	ErrCodeNotImplemented: "Feature not implemented",

	// File/Path
	ErrCodeFileNotFound:     "File not found",
	ErrCodeFileExists:       "File already exists",
	ErrCodePermissionDenied: "Permission denied",
	ErrCodePathEmpty:        "File path cannot be empty",
	ErrCodePathInvalid:      "Invalid file path",
	ErrCodePathEscape:       "Path escapes allowed directory",
	ErrCodePathIsDirectory:  "Path is a directory, not a file",
	ErrCodeDirCreateFailed:  "Failed to create directory",
	ErrCodeDirNotEmpty:      "Directory is not empty",

	// Read
	ErrCodeReadFailed:     "Failed to read file",
	ErrCodeContentEmpty:   "Content cannot be empty",
	ErrCodeFileTooLarge:   "File size exceeds limit",
	ErrCodeLineOutOfRange: "Line number out of range",
	ErrCodeEncodingError:  "File encoding error",

	// Write
	ErrCodeWriteFailed:     "Failed to write file",
	ErrCodeContentRequired: "Content is required",

	// Edit
	ErrCodeSearchEmpty:        "Search content cannot be empty",
	ErrCodeSearchNotFound:     "No matches found",
	ErrCodeInsertPosEmpty:     "Must specify insert_after or insert_before",
	ErrCodeInsertPosNotFound:  "Insert position not found",
	ErrCodeDeleteLinesEmpty:   "Delete line numbers cannot be empty",
	ErrCodeDeleteLinesInvalid: "No valid line numbers to delete",
	ErrCodeRegexInvalid:       "Invalid regex pattern",
	ErrCodeRegexTooLong:       "Regex pattern too long (max 1000 chars)",
	ErrCodeBackupNotFound:     "Backup not found",
	ErrCodeBackupFailed:       "Failed to create backup",
	ErrCodeRestoreFailed:      "Failed to restore backup",
	ErrCodeVersionInvalid:     "Invalid version number",

	// Search
	ErrCodeQueryEmpty:     "Search query cannot be empty",
	ErrCodePatternInvalid: "Invalid file pattern",
	ErrCodeSearchFailed:   "Search failed",

	// Operation
	ErrCodeOperationNotSupported: "Operation not supported",
}

// ToolError represents a tool error with code, message and optional detail.
type ToolError struct {
	Code    ErrorCode
	Message string
	Detail  string
}

// Error returns a simple text message for LLM consumption.
func (e *ToolError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("Error: %s - %s", e.Message, e.Detail)
	}
	return fmt.Sprintf("Error: %s", e.Message)
}

// NewError creates a new ToolError with the given code and detail.
func NewError(code ErrorCode, detail string) *ToolError {
	msg, ok := ErrorMessages[code]
	if !ok {
		msg = "Unknown error"
	}
	return &ToolError{
		Code:    code,
		Message: msg,
		Detail:  detail,
	}
}

// NewErrorf creates a new ToolError with formatted detail.
func NewErrorf(code ErrorCode, format string, args ...interface{}) *ToolError {
	return NewError(code, fmt.Sprintf(format, args...))
}

// IsCode checks if an error matches the given error code.
func IsCode(err error, code ErrorCode) bool {
	if toolErr, ok := err.(*ToolError); ok {
		return toolErr.Code == code
	}
	return false
}

// GetCode extracts the error code from an error, returns empty string if not a ToolError.
func GetCode(err error) ErrorCode {
	if toolErr, ok := err.(*ToolError); ok {
		return toolErr.Code
	}
	return ""
}

// ============================================================================
// Convenience constructors for common errors
// ============================================================================

// General errors
func ErrInvalidParams(detail string) *ToolError  { return NewError(ErrCodeInvalidParams, detail) }
func ErrParseFailed(detail string) *ToolError    { return NewError(ErrCodeParseFailed, detail) }
func ErrTimeout(detail string) *ToolError        { return NewError(ErrCodeTimeout, detail) }
func ErrNotImplemented() *ToolError              { return NewError(ErrCodeNotImplemented, "") }

// Path/File errors
func ErrPathEmpty() *ToolError                    { return NewError(ErrCodePathEmpty, "") }
func ErrPathInvalid(path string) *ToolError       { return NewErrorf(ErrCodePathInvalid, "%s", path) }
func ErrPathEscape(path string) *ToolError        { return NewErrorf(ErrCodePathEscape, "%s", path) }
func ErrPathIsDirectory(path string) *ToolError { return NewErrorf(ErrCodePathIsDirectory, "%s is a directory, not a file. Specify a file path instead", path) }
func ErrFileNotFound(path string) *ToolError      { return NewErrorf(ErrCodeFileNotFound, "%s", path) }
func ErrFileExists(path string) *ToolError        { return NewErrorf(ErrCodeFileExists, "%s (use overwrite or append mode)", path) }
func ErrPermissionDenied(path string) *ToolError  { return NewErrorf(ErrCodePermissionDenied, "%s", path) }
func ErrDirCreateFailed(path string) *ToolError   { return NewErrorf(ErrCodeDirCreateFailed, "%s", path) }

// Content errors
func ErrContentEmpty() *ToolError                          { return NewError(ErrCodeContentEmpty, "") }
func ErrContentRequired() *ToolError                       { return NewError(ErrCodeContentRequired, "") }
func ErrFileTooLarge(size, limit int64) *ToolError         { return NewErrorf(ErrCodeFileTooLarge, "%d bytes (limit: %d)", size, limit) }
func ErrLineOutOfRange(line, total int) *ToolError         { return NewErrorf(ErrCodeLineOutOfRange, "line %d exceeds file length (%d lines)", line, total) }
func ErrLineNumberInvalid() *ToolError                     { return NewError(ErrCodeLineOutOfRange, "line number must be >= 1") }

// Edit errors
func ErrSearchEmpty() *ToolError                  { return NewError(ErrCodeSearchEmpty, "") }
func ErrSearchNotFound(search string) *ToolError  { return NewErrorf(ErrCodeSearchNotFound, "%s", search) }
func ErrInsertPosEmpty() *ToolError               { return NewError(ErrCodeInsertPosEmpty, "") }
func ErrInsertPosNotFound() *ToolError            { return NewError(ErrCodeInsertPosNotFound, "") }
func ErrDeleteLinesEmpty() *ToolError             { return NewError(ErrCodeDeleteLinesEmpty, "") }
func ErrDeleteLinesInvalid() *ToolError           { return NewError(ErrCodeDeleteLinesInvalid, "") }
func ErrRegexInvalid(detail string) *ToolError    { return NewErrorf(ErrCodeRegexInvalid, "%s", detail) }
func ErrRegexTooLong() *ToolError                 { return NewError(ErrCodeRegexTooLong, "") }
func ErrBackupFailed(path string) *ToolError      { return NewErrorf(ErrCodeBackupFailed, "%s", path) }
func ErrBackupNotFound(path string) *ToolError    { return NewErrorf(ErrCodeBackupNotFound, "%s", path) }
func ErrRestoreFailed(detail string) *ToolError   { return NewErrorf(ErrCodeRestoreFailed, "%s", detail) }
func ErrVersionInvalid() *ToolError               { return NewError(ErrCodeVersionInvalid, "version must be > 0") }

// Search errors
func ErrQueryEmpty() *ToolError  { return NewError(ErrCodeQueryEmpty, "") }

// Operation errors
func ErrOperationNotSupported(op string) *ToolError { return NewErrorf(ErrCodeOperationNotSupported, "%s", op) }
