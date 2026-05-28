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
// 环境变量相关
// ============================================================================

// getEnvOrSkip 获取环境变量，如果不存在则跳过测试
func getEnvOrSkip(t *testing.T, key string) string {
	value := os.Getenv(key)
	if value == "" {
		t.Skipf("Skipping test: %s environment variable not set", key)
	}
	return value
}

// getEnvOrFatal 获取环境变量，如果不存在则 Fatal
func getEnvOrFatal(t *testing.T, key string) string {
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("Required environment variable %s not set", key)
	}
	return value
}

// ============================================================================
// 字符串处理
// ============================================================================

// truncateMiddle 截断字符串中间部分
func truncateMiddle(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	half := maxLen / 2
	return s[:half] + "..." + s[len(s)-half:]
}

// maskSensitive 隐藏敏感信息
func maskSensitive(s string, visibleChars int) string {
	if len(s) <= visibleChars*2 {
		return strings.Repeat("*", len(s))
	}
	return s[:visibleChars] + strings.Repeat("*", len(s)-visibleChars*2) + s[len(s)-visibleChars:]
}

// ============================================================================
// 测试控制
// ============================================================================

// skipIfNoAPIKey 如果没有配置 API Key 则跳过测试
func skipIfNoAPIKey(t *testing.T, apiKey string) {
	if apiKey == "" {
		t.Skip("Skipping test: LLM_API_KEY environment variable not set")
	}
}

// skipIfAPIError 如果错误是 API 余额/限流/不可用等临时问题则跳过测试
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

// skipIfShort 如果是短测试模式则跳过
func skipIfShort(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
}

// skipIfCI 如果在 CI 环境中则跳过
func skipIfCI(t *testing.T) {
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("Skipping test in CI environment")
	}
}

// skipUnlessEnv 除非指定环境变量设置，否则跳过
func skipUnlessEnv(t *testing.T, key string) {
	if os.Getenv(key) == "" {
		t.Skipf("Skipping test: %s not set", key)
	}
}

// ============================================================================
// 配置获取
// ============================================================================

// getTestConfig 获取测试配置（从环境变量读取）
// 注意：请设置环境变量 LLM_API_KEY 来运行测试
// 示例：
//
//	export LLM_BASE_URL="https://open.bigmodel.cn/api/paas/v4"
//	export LLM_MODEL="GLM-5"
//	export LLM_API_KEY="your-api-key"
func getTestConfig() (baseURL, apiKey, model string) {
	baseURL = getEnvOrDefault("LLM_BASE_URL", "https://open.bigmodel.cn/api/paas/v4")
	apiKey = os.Getenv("LLM_API_KEY") // 必须通过环境变量设置，不提供默认值
	model = getEnvOrDefault("LLM_MODEL", "GLM-5")
	return
}

// getTestConfigWithTimeout 获取测试配置和超时时间
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
// JSON 处理
// ============================================================================

// mustMarshalJSON 必须成功序列化为 JSON，否则 panic
func mustMarshalJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return string(data)
}

// mustMarshalJSONIndent 必须成功序列化为格式化的 JSON，否则 panic
func mustMarshalJSONIndent(v interface{}) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return string(data)
}

// isValidJSON 检查字符串是否是有效的 JSON
func isValidJSON(s string) bool {
	var js interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}

// jsonContains 检查 JSON 字符串是否包含指定的键值
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
// 断言辅助
// ============================================================================

// assertContains 断言字符串包含子串
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

// assertNotContains 断言字符串不包含子串
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

// assertOneOf 断言值是选项之一
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

// assertLen 断言长度
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
// 并发测试
// ============================================================================

// waitForChannel 等待 channel 返回值或超时
func waitForChannel[T any](ch <-chan T, timeout time.Duration) (T, bool) {
	var zero T
	select {
	case v := <-ch:
		return v, true
	case <-time.After(timeout):
		return zero, false
	}
}

// waitGroupWithTimeout 等待 channel 关闭或超时
func waitGroupWithTimeout(done <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// ============================================================================
// 测试数据生成
// ============================================================================

// generateTestString 生成测试用字符串
func generateTestString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[i%len(charset)]
	}
	return string(result)
}

// generateTestJSON 生成测试用 JSON
func generateTestJSON(fields map[string]interface{}) string {
	return mustMarshalJSON(fields)
}

// ============================================================================
// 日志辅助
// ============================================================================

// logTestName 记录测试名称
func logTestName(t *testing.T) {
	t.Helper()
	t.Logf("=== Running test: %s ===", t.Name())
}

// logTestResult 记录测试结果
func logTestResult(t *testing.T, passed bool) {
	t.Helper()
	status := "PASSED"
	if !passed {
		status = "FAILED"
	}
	t.Logf("=== Test %s: %s ===", status, t.Name())
}

// logWithDuration 记录带耗时的操作
func logWithDuration(t *testing.T, operation string, fn func()) {
	t.Helper()
	start := time.Now()
	t.Logf("[%s] Starting...", operation)
	fn()
	t.Logf("[%s] Completed in %v", operation, time.Since(start))
}

// ============================================================================
// 测试用例
// ============================================================================

// TestGetEnvOrDefault 测试 getEnvOrDefault
func TestGetEnvOrDefault(t *testing.T) {
	// 测试不存在的环境变量
	result := getEnvOrDefault("NONEXISTENT_VAR_12345", "default_value")
	assert.Equal(t, "default_value", result)
}

// TestTruncateString 测试 truncateString
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
			expected: "hello worl...", // 前10个字符 + "..."
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

// TestTruncateMiddle 测试 truncateMiddle
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
			expected: "hello... long", // half=5, 前5个 + "..." + 后5个（包含空格）
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

// TestMaskSensitive 测试 maskSensitive
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
			expected:     "sk-1***********cdef", // 前4 + (18-8=10个*) + 后4
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

// TestMustMarshalJSON 测试 mustMarshalJSON
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

// TestIsValidJSON 测试 isValidJSON
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

// TestJSONContains 测试 jsonContains
func TestJSONContains(t *testing.T) {
	tests := []struct {
		name       string
		jsonStr    string
		key        string
		value      string
		expected   bool
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

// TestGenerateTestString 测试 generateTestString
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

// TestGenerateTestJSON 测试 generateTestJSON
func TestGenerateTestJSON(t *testing.T) {
	fields := map[string]interface{}{
		"name":  "test",
		"count": 42,
	}

	result := generateTestJSON(fields)
	assert.True(t, isValidJSON(result))
	assert.True(t, jsonContains(result, "name", "test"))
}

// TestWaitForChannel 测试 waitForChannel
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

// TestWaitGroupWithTimeout 测试 waitGroupWithTimeout
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
		// 不关闭 channel

		result := waitGroupWithTimeout(done, 50*time.Millisecond)
		assert.False(t, result)
	})
}

// TestAssertContains 测试 assertContains
func TestAssertContains(t *testing.T) {
	// 这个测试验证 assertContains 不会对有效情况报错
	assertContains(t, "hello world", "world")
}

// TestAssertNotContains 测试 assertNotContains
func TestAssertNotContains(t *testing.T) {
	assertNotContains(t, "hello world", "xyz")
}

// TestAssertOneOf 测试 assertOneOf
func TestAssertOneOf(t *testing.T) {
	assertOneOf(t, "b", []string{"a", "b", "c"})
	assertOneOf(t, 2, []int{1, 2, 3})
}

// TestAssertLen 测试 assertLen
func TestAssertLen(t *testing.T) {
	assertLen(t, []int{1, 2, 3}, 3)
	assertLen(t, []string{}, 0)
}

// ============================================================================
// 基准测试
// ============================================================================

// BenchmarkTruncateString 基准测试 truncateString
func BenchmarkTruncateString(b *testing.B) {
	longStr := "This is a very long string that needs to be truncated for display purposes"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = truncateString(longStr, 20)
	}
}

// BenchmarkMaskSensitive 基准测试 maskSensitive
func BenchmarkMaskSensitive(b *testing.B) {
	key := "sk-1234567890abcdefghijklmnopqrstuvwxyz"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = maskSensitive(key, 4)
	}
}

// BenchmarkMustMarshalJSON 基准测试 mustMarshalJSON
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

// BenchmarkIsValidJSON 基准测试 isValidJSON
func BenchmarkIsValidJSON(b *testing.B) {
	jsonStr := `{"name": "test", "count": 42, "items": ["a", "b", "c"]}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isValidJSON(jsonStr)
	}
}

// BenchmarkGenerateTestString 基准测试 generateTestString
func BenchmarkGenerateTestString(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generateTestString(100)
	}
}
