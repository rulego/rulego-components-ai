package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rulego/rulego/test/assert"
)

// ============================================================================
// Environmental variables are related
// ============================================================================

// getEnvOrSkip retrieves the environment variable; if it does not exist, the test is skipped
func getEnvOrSkip(t *testing.T, key string) string {
	value := os.Getenv(key)
	if value == "" {
		t.Skipf("Skipping test: %s environment variable not set", key)
	}
	return value
}

// getEnvOrFatal gets the environment variable; if it doesn't exist, use Fatal
func getEnvOrFatal(t *testing.T, key string) string {
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("Required environment variable %s not set", key)
	}
	return value
}

// ============================================================================
// String processing
// ============================================================================

// truncateMiddle Prune the middle part of the string
func truncateMiddle(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	half := maxLen / 2
	return s[:half] + "..." + s[len(s)-half:]
}

// maskSensitive hides sensitive information
func maskSensitive(s string, visibleChars int) string {
	if len(s) <= visibleChars*2 {
		return strings.Repeat("*", len(s))
	}
	return s[:visibleChars] + strings.Repeat("*", len(s)-visibleChars*2) + s[len(s)-visibleChars:]
}

// ============================================================================
// Test control
// ============================================================================

// skipIfNoAPIKey skips the test if the API Key is not configured
func skipIfNoAPIKey(t *testing.T, apiKey string) {
	if apiKey == "" {
		t.Skip("Skipping test: LLM_API_KEY environment variable not set")
	}
}

// skipIfAPIError If the error is due to temporary issues such as API balance, rate limit, or unavailability, the test is skipped
func skipIfAPIError(t *testing.T, err error) {
	if err == nil {
		return
	}
	errStr := err.Error()
	if strings.Contains(errStr, "余额") || strings.Contains(errStr, "balance") ||
		strings.Contains(errStr, "insufficient") || strings.Contains(errStr, "quota") ||
		strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "503") || strings.Contains(errStr, "Service Unavailable") {
		t.Skipf("Skipping test: API unavailable/quota exceeded: %v", err)
	}
}

// skipIfShort: If it is a short test mode, skip it
func skipIfShort(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
}

// skipIfCI Skip if in a CI environment
func skipIfCI(t *testing.T) {
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("Skipping test in CI environment")
	}
}

// skipUnlessEnv Skip unless specified environment variable settings
func skipUnlessEnv(t *testing.T, key string) {
	if os.Getenv(key) == "" {
		t.Skipf("Skipping test: %s not set", key)
	}
}

// ============================================================================
// Configuration acquisition
// ============================================================================

// getTestConfig Retrieves test configuration (read from environment variables)
// Note: Please set the environment variable LLM_API_KEY to run the test
// Example:
//
//	export LLM_BASE_URL="https://open.bigmodel.cn/api/paas/v4"
//	export LLM_MODEL="GLM-5"
//	export LLM_API_KEY="your-api-key"
func getTestConfig() (baseURL, apiKey, model string) {
	baseURL = getEnvOrDefault("LLM_BASE_URL", "https://open.bigmodel.cn/api/paas/v4")
	apiKey = os.Getenv("LLM_API_KEY") // It must be set through environment variables; default values are not provided
	model = getEnvOrDefault("LLM_MODEL", "GLM-5")
	return
}

// getTestConfigWithTimeout gets the test configuration and timeout timeout
func getTestConfigWithTimeout() (baseURL, apiKey, model string, timeout time.Duration) {
	baseURL, apiKey, model = getTestConfig()
	timeoutStr := getEnvOrDefault("LLM_TIMEOUT", "120s")
	timeout, _ = time.ParseDuration(timeoutStr)
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return
}

// ============================================================================
// JSON processing
// ============================================================================

// mustMarshalJSON must be successfully serialized to JSON, or it will panic
func mustMarshalJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return string(data)
}

// mustMarshalJSONIndent must be successfully serialized into a formatted JSON, or it will panic
func mustMarshalJSONIndent(v interface{}) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return string(data)
}

// isValidJSON checks whether the string is a valid JSON
func isValidJSON(s string) bool {
	var js interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}

// jsonContains checks whether a JSON string contains the specified key value
func jsonContains(jsonStr, key, value string) bool {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return false
	}
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v) == value
	}
	return false
}

// ============================================================================
// Asserting support
// ============================================================================

// assertContains asserts that the string contains substrings
func assertContains(t *testing.T, s, substr string, msgAndArgs ...interface{}) {
	t.Helper()
	if !strings.Contains(s, substr) {
		msg := fmt.Sprintf("Expected %q to contain %q", s, substr)
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf("%s - %v", msg, msgAndArgs)
		}
		t.Error(msg)
	}
}

// assertNotContains assertion strings do not contain substrings
func assertNotContains(t *testing.T, s, substr string, msgAndArgs ...interface{}) {
	t.Helper()
	if strings.Contains(s, substr) {
		msg := fmt.Sprintf("Expected %q to NOT contain %q", s, substr)
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf("%s - %v", msg, msgAndArgs)
		}
		t.Error(msg)
	}
}

// assertOneOf assert value is one option
func assertOneOf[T comparable](t *testing.T, actual T, expected []T, msgAndArgs ...interface{}) {
	t.Helper()
	for _, e := range expected {
		if actual == e {
			return
		}
	}
	msg := fmt.Sprintf("Expected %v to be one of %v", actual, expected)
	if len(msgAndArgs) > 0 {
		msg = fmt.Sprintf("%s - %v", msg, msgAndArgs)
	}
	t.Error(msg)
}

// assertLen asserts length
func assertLen[T any](t *testing.T, slice []T, expected int, msgAndArgs ...interface{}) {
	t.Helper()
	if len(slice) != expected {
		msg := fmt.Sprintf("Expected length %d, got %d", expected, len(slice))
		if len(msgAndArgs) > 0 {
			msg = fmt.Sprintf("%s - %v", msg, msgAndArgs)
		}
		t.Error(msg)
	}
}

// ============================================================================
// Concurrent testing
// ============================================================================

// waitForChannel waits for the channel return value or timeout
func waitForChannel[T any](ch <-chan T, timeout time.Duration) (T, bool) {
	var zero T
	select {
	case v := <-ch:
		return v, true
	case <-time.After(timeout):
		return zero, false
	}
}

// waitGroupWithTimeout: Waits for the channel to close or time out
func waitGroupWithTimeout(done <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// ============================================================================
// Test data generation
// ============================================================================

// generateTestString generates a string for testing
func generateTestString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[i%len(charset)]
	}
	return string(result)
}

// generateTestJSON generates JSON for testing
func generateTestJSON(fields map[string]interface{}) string {
	return mustMarshalJSON(fields)
}

// ============================================================================
// Log assistance
// ============================================================================

// logTestName records the test name
func logTestName(t *testing.T) {
	t.Helper()
	t.Logf("=== Running test: %s ===", t.Name())
}

// logTestResult records the test results
func logTestResult(t *testing.T, passed bool) {
	t.Helper()
	status := "PASSED"
	if !passed {
		status = "FAILED"
	}
	t.Logf("=== Test %s: %s ===", status, t.Name())
}

// logWithDuration records time-consuming operations
func logWithDuration(t *testing.T, operation string, fn func()) {
	t.Helper()
	start := time.Now()
	t.Logf("[%s] Starting...", operation)
	fn()
	t.Logf("[%s] Completed in %v", operation, time.Since(start))
}

// ============================================================================
// Test cases
// ============================================================================

// TestGetEnvOrDefault Test getEnvOrDefault
func TestGetEnvOrDefault(t *testing.T) {
	// Test environment variables that don't exist
	result := getEnvOrDefault("NONEXISTENT_VAR_12345", "default_value")
	assert.Equal(t, "default_value", result)
}

// TestTruncateString Tests truncateString
func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string",
			input:    "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "exact length",
			input:    "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "long string",
			input:    "hello world this is a long string",
			maxLen:   10,
			expected: "hello worl...", // First 10 characters + "..."
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
		{
			name:     "zero maxLen",
			input:    "hello",
			maxLen:   0,
			expected: "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestTruncateMiddle tests truncateMiddle
func TestTruncateMiddle(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string",
			input:    "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "long string",
			input:    "hello world this is long",
			maxLen:   10,
			expected: "hello... long", // half=5, first 5 + "..." + last 5 (including spaces)
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateMiddle(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMaskSensitive tests maskSensitive
func TestMaskSensitive(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		visibleChars int
		expected     string
	}{
		{
			name:         "normal key",
			input:        "sk-1234567890abcdef",
			visibleChars: 4,
			expected:     "sk-1***********cdef", // First 4 + (18 - 8 = 10 *) + last 4
		},
		{
			name:         "short string",
			input:        "abc",
			visibleChars: 2,
			expected:     "***",
		},
		{
			name:         "exact visible",
			input:        "abcd",
			visibleChars: 2,
			expected:     "****",
		},
		{
			name:         "empty string",
			input:        "",
			visibleChars: 4,
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskSensitive(tt.input, tt.visibleChars)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestmustMarshalJSON TestMustMarshalJSON
func TestMustMarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name:     "simple object",
			input:    map[string]string{"key": "value"},
			expected: `{"key":"value"}`,
		},
		{
			name:     "array",
			input:    []string{"a", "b", "c"},
			expected: `["a","b","c"]`,
		},
		{
			name:     "number",
			input:    42,
			expected: "42",
		},
		{
			name:     "nil",
			input:    nil,
			expected: "null",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mustMarshalJSON(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsValidJSON Test isValidJSON
func TestIsValidJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid object",
			input:    `{"key": "value"}`,
			expected: true,
		},
		{
			name:     "valid array",
			input:    `[1, 2, 3]`,
			expected: true,
		},
		{
			name:     "valid number",
			input:    `42`,
			expected: true,
		},
		{
			name:     "invalid json",
			input:    `{invalid}`,
			expected: false,
		},
		{
			name:     "empty string",
			input:    ``,
			expected: false,
		},
		{
			name:     "partial json",
			input:    `{"key":`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidJSON(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestJSONContains tests jsonContains
func TestJSONContains(t *testing.T) {
	tests := []struct {
		name     string
		jsonStr  string
		key      string
		value    string
		expected bool
	}{
		{
			name:     "key exists with value",
			jsonStr:  `{"name": "test", "count": 42}`,
			key:      "name",
			value:    "test",
			expected: true,
		},
		{
			name:     "key exists wrong value",
			jsonStr:  `{"name": "test"}`,
			key:      "name",
			value:    "wrong",
			expected: false,
		},
		{
			name:     "key not exists",
			jsonStr:  `{"name": "test"}`,
			key:      "missing",
			value:    "test",
			expected: false,
		},
		{
			name:     "invalid json",
			jsonStr:  `{invalid}`,
			key:      "name",
			value:    "test",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := jsonContains(tt.jsonStr, tt.key, tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGenerateTestString Test generateTestString
func TestGenerateTestString(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{name: "zero length", length: 0},
		{name: "short string", length: 10},
		{name: "medium string", length: 100},
		{name: "long string", length: 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateTestString(tt.length)
			assert.Equal(t, tt.length, len(result))
		})
	}
}

// TestGenerateTestJSON Test generateTestJSON
func TestGenerateTestJSON(t *testing.T) {
	fields := map[string]interface{}{
		"name":  "test",
		"count": 42,
	}

	result := generateTestJSON(fields)
	assert.True(t, isValidJSON(result))
	assert.True(t, jsonContains(result, "name", "test"))
}

// TestWaitForChannel Tests waitForChannel
func TestWaitForChannel(t *testing.T) {
	t.Run("value received", func(t *testing.T) {
		ch := make(chan string, 1)
		ch <- "test value"

		result, ok := waitForChannel(ch, 100*time.Millisecond)
		assert.True(t, ok)
		assert.Equal(t, "test value", result)
	})

	t.Run("timeout", func(t *testing.T) {
		ch := make(chan string)

		result, ok := waitForChannel(ch, 50*time.Millisecond)
		assert.False(t, ok)
		assert.Equal(t, "", result)
	})
}

// TestWaitGroupWithTimeout Test: waitGroupWithTimeout
func TestWaitGroupWithTimeout(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			time.Sleep(10 * time.Millisecond)
			close(done)
		}()

		result := waitGroupWithTimeout(done, 100*time.Millisecond)
		assert.True(t, result)
	})

	t.Run("timeout", func(t *testing.T) {
		done := make(chan struct{})
		// Do not close the channel

		result := waitGroupWithTimeout(done, 50*time.Millisecond)
		assert.False(t, result)
	})
}

// TestAssertContains tests assertContains
func TestAssertContains(t *testing.T) {
	// This test verifies that assertContains does not cause errors for valid cases
	assertContains(t, "hello world", "world")
}

// TestAssertNotContains tests assertNotContains
func TestAssertNotContains(t *testing.T) {
	assertNotContains(t, "hello world", "xyz")
}

// TestAssertOneOf Test assertOneOf
func TestAssertOneOf(t *testing.T) {
	assertOneOf(t, "b", []string{"a", "b", "c"})
	assertOneOf(t, 2, []int{1, 2, 3})
}

// TestAssertLen Test assertLen
func TestAssertLen(t *testing.T) {
	assertLen(t, []int{1, 2, 3}, 3)
	assertLen(t, []string{}, 0)
}

// ============================================================================
// Benchmark Test
// ============================================================================

// BenchmarktruncateString Benchmark TruncateString
func BenchmarkTruncateString(b *testing.B) {
	longStr := "This is a very long string that needs to be truncated for display purposes"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = truncateString(longStr, 20)
	}
}

// BenchmarkMaskSensitive benchmark maskSensitive
func BenchmarkMaskSensitive(b *testing.B) {
	key := "sk-1234567890abcdefghijklmnopqrstuvwxyz"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = maskSensitive(key, 4)
	}
}

// BenchmarkmustMarshalJSON Benchmark MustMarshalJSON
func BenchmarkMustMarshalJSON(b *testing.B) {
	data := map[string]interface{}{
		"name":  "test",
		"count": 42,
		"items": []string{"a", "b", "c"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mustMarshalJSON(data)
	}
}

// BenchmarkIsValidJSON Benchmark isValidJSON
func BenchmarkIsValidJSON(b *testing.B) {
	jsonStr := `{"name": "test", "count": 42, "items": ["a", "b", "c"]}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isValidJSON(jsonStr)
	}
}

// BenchmarkGenerateTestString Benchmark test generateTestString
func BenchmarkGenerateTestString(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generateTestString(100)
	}
}
