/*
 * Copyright 2023 The RuleGo Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package session

import (
	"encoding/json"
	"strings"
	"time"
)

// Session defaults
const (
	// DefaultMaxMessages max messages per session
	DefaultMaxMessages = 200

	// DefaultMaxTokenCount max token count
	DefaultMaxTokenCount = 128000

	// DefaultKeepRecentCount for compaction
	DefaultKeepRecentCount = 10

	// DefaultTargetTokens for compaction (fallback when percent not set)
	// A fixed threshold used when the model context size is unknown
	DefaultTargetTokens = 76800

	// DefaultTargetTokensPercent triggers the percentage threshold for compression
	// For example, 70 means compression is triggered when token usage reaches 70% of the model context
	DefaultTargetTokensPercent = 70

	// DefaultMinMessagesToCompact minimum messages before compaction
	DefaultMinMessagesToCompact = 20

	// MaxToolResultSize: The maximum number of characters the tool result can directly save
	MaxToolResultSize = 2000

	// MaxToolCallArgumentSize is the maximum number of characters the parameter field can call
	// Used to truncate large fields such as content and new_content
	MaxToolCallArgumentSize = 500
)

// Time durations
var (
	// DefaultSessionTTL session time-to-live (30 days)
	DefaultSessionTTL = 30 * 24 * time.Hour

	// DefaultSessionIdleTimeout session idle timeout (1 hour)
	DefaultSessionIdleTimeout = 1 * time.Hour
)

// ProcessToolResult handles the tool result
// If the threshold is not exceeded, it is stored directly; if exceeded, it will be truncated
func ProcessToolResult(result string) string {
	if len(result) <= MaxToolResultSize {
		return result
	}
	return result[:MaxToolResultSize] + "\n...[已截断]"
}

// largeArgumentFields: The name of the large parameter field to be truncated
var largeArgumentFields = []string{
	"content",     // write tool
	"new_content", // edit tool
	"search",      // edit tool (possibly very large)
	"replace",     // edit tool (possibly very large)
	"code",        // code tools
	"text",        // Generic text fields
	"body",        // HTTP body
	"data",        // General data field
}

// ProcessToolCallArguments handles the parameters called by the tool
// Truncate large fields to keep JSON formatting valid
// If parsing fails, returns an empty object {}
func ProcessToolCallArguments(arguments string) string {
	if arguments == "" || arguments == "null" {
		return "{}"
	}

	// Try parsing JSON
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		// Parsing fails, returns an empty object (guaranteed to be valid JSON)
		return "{}"
	}

	// If the parameter is short, return it directly
	if len(arguments) <= MaxToolCallArgumentSize {
		return arguments
	}

	// Truncate large fields
	modified := false
	for _, field := range largeArgumentFields {
		if val, ok := params[field]; ok {
			if strVal, ok := val.(string); ok && len(strVal) > MaxToolCallArgumentSize {
				params[field] = strVal[:MaxToolCallArgumentSize] + "...[已截断]"
				modified = true
			}
		}
	}

	if !modified {
		// No modifications were made, but the original parameters were too long, so the version was cut off
		// This situation may be that other fields are large and the whole is cut off
		if len(arguments) > MaxToolCallArgumentSize*2 {
			// Reserialization, if still too long, returns to the simplified version
			result, err := json.Marshal(params)
			if err != nil || len(result) > MaxToolCallArgumentSize*2 {
				// Returns a simplified version that only includes the operation type and path
				simplified := make(map[string]interface{})
				if op, ok := params["operation"]; ok {
					simplified["operation"] = op
				}
				if path, ok := params["path"]; ok {
					simplified["path"] = path
				}
				simplified["_truncated"] = true
				result, _ = json.Marshal(simplified)
				return string(result)
			}
			return string(result)
		}
		return arguments
	}

	// Reserialization
	result, err := json.Marshal(params)
	if err != nil {
		return "{}"
	}
	return string(result)
}

// truncateString is a string truncation auxiliary function
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[已截断]"
}

// isLargeField checks whether a field needs to be truncated
func isLargeField(field string) bool {
	field = strings.ToLower(field)
	for _, f := range largeArgumentFields {
		if strings.Contains(field, f) {
			return true
		}
	}
	return false
}
