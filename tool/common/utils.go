package common

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// TruncateString truncates a string to the given maximum length.
// Truncate with valid UTF-8 boundaries to avoid corrupting multibyte characters.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find the last valid rune boundary that does not exceed maxLen bytes
	for maxLen > 0 && !utf8.RuneStart(s[maxLen]) {
		maxLen--
	}
	return s[:maxLen] + "..."
}

// FormatTimestamp formats the current time as RFC3339.
func FormatTimestamp() string {
	return time.Now().Format(time.RFC3339)
}

// FormatTime formats time in a human-readable format.
func FormatTime() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// ContainsIgnoreCase checks if a string slice contains a specific string (case-insensitive).
func ContainsIgnoreCase(slice []string, item string) bool {
	itemLower := strings.ToLower(item)
	for _, s := range slice {
		if strings.ToLower(s) == itemLower {
			return true
		}
	}
	return false
}

// MarshalJSON marshals data to JSON with indentation.
func MarshalJSON(data interface{}) ([]byte, error) {
	return json.MarshalIndent(data, "", "  ")
}

// MarshalJSONCompact marshals data to compact JSON.
func MarshalJSONCompact(data interface{}) ([]byte, error) {
	return json.Marshal(data)
}

// MustToJSON marshals data to JSON string, panics on error.
func MustToJSON(data interface{}) string {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}
