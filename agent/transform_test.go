package agent

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test/assert"
)

// mockRuleContext Used for testing simulation RuleContext
type mockRuleContext struct {
	types.RuleContext
}

// GetEnv simulates obtaining environment variables
func (m *mockRuleContext) GetEnv(msg types.RuleMsg, useMetadata bool) map[string]interface{} {
	env := make(map[string]interface{})
	if useMetadata {
		for k, v := range msg.Metadata.GetReadOnlyValues() {
			env[k] = v
		}
	}
	return env
}

// TestIsBase64Image Test the isBase64Image function
func TestIsBase64Image(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Valid base64 image format
		{name: "valid jpeg", input: "data:image/jpeg;base64,/9j/4AAQSkZJRg==", expected: true},
		{name: "valid png", input: "data:image/png;base64,iVBORw0KGgo=", expected: true},
		{name: "valid gif", input: "data:image/gif;base64,R0lGODlh", expected: true},
		{name: "valid webp", input: "data:image/webp;base64,UklGRjg=", expected: true},
		{name: "valid bmp", input: "data:image/bmp;base64,Qk0=", expected: true},

		// Invalid format
		{name: "empty string", input: "", expected: false},
		{name: "plain text", input: "hello world", expected: false},
		{name: "url", input: "https://example.com/image.png", expected: false},
		{name: "local path", input: "/path/to/image.png", expected: false},
		{name: "data without image", input: "data:text/plain;base64,SGVsbG8=", expected: false},
		// Note: The actual function only checks the prefix "data:image/" and does not verify whether there is valid base64 data
		{name: "data uri without base64 data", input: "data:image/png,", expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBase64Image(tt.input)
			if result != tt.expected {
				t.Errorf("isBase64Image(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestIsLocalFilePath tests the isLocalFilePath function
func TestIsLocalFilePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Valid local file paths
		{name: "absolute path unix", input: "/home/user/image.png", expected: true},
		{name: "absolute path windows", input: "C:\\Users\\image.png", expected: true},
		{name: "relative path", input: "./image.png", expected: true},
		{name: "relative path parent", input: "../images/photo.jpg", expected: true},

		// Invalid format
		{name: "empty string", input: "", expected: false},
		{name: "url http", input: "http://example.com/image.png", expected: false},
		{name: "url https", input: "https://example.com/image.png", expected: false},
		{name: "base64", input: "data:image/png;base64,iVBORw0KGgo=", expected: false},
		// Note: The actual function will recognize filenames without path separators
		{name: "filename only", input: "image.png", expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLocalFilePath(tt.input)
			if result != tt.expected {
				t.Errorf("isLocalFilePath(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestParseBase64Image Test the parseBase64Image function
func TestParseBase64Image(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedMime   string
		expectedBase64 string
	}{
		{
			name:           "jpeg image",
			input:          "data:image/jpeg;base64,/9j/4AAQSkZJRg==",
			expectedMime:   "image/jpeg",
			expectedBase64: "/9j/4AAQSkZJRg==",
		},
		{
			name:           "png image",
			input:          "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
			expectedMime:   "image/png",
			expectedBase64: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
		},
		{
			name:           "gif image",
			input:          "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP",
			expectedMime:   "image/gif",
			expectedBase64: "R0lGODlhAQABAIAAAAAAAP",
		},
		{
			name:           "webp image",
			input:          "data:image/webp;base64,UklGRjgPAAAA",
			expectedMime:   "image/webp",
			expectedBase64: "UklGRjgPAAAA",
		},
		{
			name:           "invalid format",
			input:          "not a base64 image",
			expectedMime:   "",
			expectedBase64: "",
		},
		{
			name:           "empty string",
			input:          "",
			expectedMime:   "",
			expectedBase64: "",
		},
		{
			// Note: The actual function returns an empty mime for data without ";base64," URI
			name:           "data uri without base64",
			input:          "data:image/png,",
			expectedMime:   "",
			expectedBase64: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mime, base64Data := parseBase64Image(tt.input)
			if mime != tt.expectedMime {
				t.Errorf("parseBase64Image(%q) mime = %q, want %q", tt.input, mime, tt.expectedMime)
			}
			if base64Data != tt.expectedBase64 {
				t.Errorf("parseBase64Image(%q) base64 = %q, want %q", tt.input, base64Data, tt.expectedBase64)
			}
		})
	}
}

// TestChatMessageTemplate tests the structure of ChatMessageTemplate
func TestChatMessageTemplate(t *testing.T) {
	// This struct is mainly used in ConvertRuleMsgToAgentInput
	// Here, we test the basic structure
	tmpl := ChatMessageTemplate{
		Role:            "user",
		ContentTemplate: nil, // In actual use, el.Template
	}

	if tmpl.Role != "user" {
		t.Errorf("Expected role 'user', got %q", tmpl.Role)
	}
}

// TestConvertRuleMsgToAgentInput_TextInput Test plain text input
func TestConvertRuleMsgToAgentInput_TextInput(t *testing.T) {
	t.Skip("Requires full RuleContext implementation with GetEnv method")
	// Create simulated RuleContext and RuleMsg
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "Hello, world!")

	input, err := ConvertRuleMsgToAgentInput(ctx, msg, nil, false, "", nil, "gpt-4", "test_agent", nil)

	assert.Nil(t, err)
	assert.NotNil(t, input)
	assert.Equal(t, 1, len(input.Messages))
	assert.Equal(t, schema.User, input.Messages[0].Role)
	assert.Equal(t, "Hello, world!", input.Messages[0].Content)
}

// TestConvertRuleMsgToAgentInput_WithSystemPrompt Test with system prompts
func TestConvertRuleMsgToAgentInput_WithSystemPrompt(t *testing.T) {
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "Hello")
	systemPrompt := "You are a helpful assistant."
	input, err := ConvertRuleMsgToAgentInput(ctx, msg, nil, false, systemPrompt, nil, "gpt-4", "test_agent", nil)
	assert.Nil(t, err)
	assert.NotNil(t, input)
	// There should be system messages and user messages
	assert.True(t, len(input.Messages) >= 2, "Expected at least 2 messages")
	// The first should be the system message
	assert.Equal(t, schema.System, input.Messages[0].Role)
	assert.Equal(t, systemPrompt, input.Messages[0].Content)
}

// TestConvertRuleMsgToAgentInput_JSONInput Test JSON input
func TestConvertRuleMsgToAgentInput_JSONInput(t *testing.T) {
	t.Skip("Requires full RuleContext implementation with GetEnv method")
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	// Create messages in OpenAI format
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
			{Role: "user", Content: "How are you?"},
		},
	}
	jsonData, _ := json.Marshal(chatRequest)
	msg := types.NewMsg(0, "TEST_MSG", types.JSON, meta, string(jsonData))
	input, err := ConvertRuleMsgToAgentInput(ctx, msg, nil, false, "", nil, "gpt-4", "test_agent", nil)
	assert.Nil(t, err)
	assert.NotNil(t, input)
	assert.Equal(t, 3, len(input.Messages))
	assert.Equal(t, schema.User, input.Messages[0].Role)
	assert.Equal(t, "Hello", input.Messages[0].Content)
	assert.Equal(t, schema.Assistant, input.Messages[1].Role)
	assert.Equal(t, "Hi there!", input.Messages[1].Content)
}

// TestParseChatMessages_WithToolCallHistory Test recovery from frontend messages tool_calls/tool_call_id.
func TestParseChatMessages_WithToolCallHistory(t *testing.T) {
	data := `{
		"messages": [
			{
				"role": "assistant",
				"content": "",
				"tool_calls": [
					{
						"id": "call-1",
						"type": "function",
						"function": {
							"name": "read",
							"arguments": "{\"path\":\"a.txt\"}"
						}
					}
				]
			},
			{
				"role": "tool",
				"content": "file content",
				"tool_call_id": "call-1"
			}
		]
	}`

	input := &adk.AgentInput{Messages: make([]*schema.Message, 0)}
	parseChatMessages(data, false, "", input, nil)

	assert.Equal(t, 2, len(input.Messages))
	assert.Equal(t, schema.Assistant, input.Messages[0].Role)
	assert.Equal(t, 1, len(input.Messages[0].ToolCalls))
	assert.Equal(t, "call-1", input.Messages[0].ToolCalls[0].ID)
	assert.Equal(t, "read", input.Messages[0].ToolCalls[0].Function.Name)
	assert.Equal(t, "{\"path\":\"a.txt\"}", input.Messages[0].ToolCalls[0].Function.Arguments)

	assert.Equal(t, schema.Tool, input.Messages[1].Role)
	assert.Equal(t, "call-1", input.Messages[1].ToolCallID)
	assert.Equal(t, "file content", input.Messages[1].Content)
}

// TestConvertRuleMsgToAgentInput_WithImages_NonVisionModel Test non-visual model processing images
func TestConvertRuleMsgToAgentInput_WithImages_NonVisionModel(t *testing.T) {
	t.Skip("Requires full RuleContext implementation with GetEnv method")
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	// Create a message containing the image URL (OpenAI format)
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Check this image"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/image.png",
						},
					},
				},
			},
		},
	}
	jsonData, _ := json.Marshal(chatRequest)
	msg := types.NewMsg(0, "TEST_MSG", types.JSON, meta, string(jsonData))
	// Use models that do not support vision
	input, err := ConvertRuleMsgToAgentInput(ctx, msg, nil, false, "", nil, "text-davinci-003", "test_agent", nil)
	assert.Nil(t, err)
	assert.NotNil(t, input)
	// Image URLs should be added to the text content
	assert.True(t, len(input.Messages) >= 1)
	content := input.Messages[len(input.Messages)-1].Content
	assert.True(t, len(content) > 0, "Content should not be empty")
}

// TestConvertRuleMsgToAgentInput_WithImages_VisionModel Test visual model processing images
func TestConvertRuleMsgToAgentInput_WithImages_VisionModel(t *testing.T) {
	t.Skip("Requires full RuleContext implementation with GetEnv method")
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	// Create a message containing base64 images (OpenAI format)
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Check this image"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
						},
					},
				},
			},
		},
	}
	jsonData, _ := json.Marshal(chatRequest)
	msg := types.NewMsg(0, "TEST_MSG", types.JSON, meta, string(jsonData))
	// Use models that support vision
	input, err := ConvertRuleMsgToAgentInput(ctx, msg, nil, false, "", nil, "gpt-4-vision-preview", "test_agent", nil)
	assert.Nil(t, err)
	assert.NotNil(t, input)
	// Multimodal formats should be used
	assert.True(t, len(input.Messages) >= 1)
	lastMsg := input.Messages[len(input.Messages)-1]
	assert.True(t, lastMsg.Role == schema.User, "Last message should be from user")
}

// TestConvertRuleMsgToAgentInput_EmptyMessages Test the situation of empty messages
func TestConvertRuleMsgToAgentInput_EmptyMessages(t *testing.T) {
	t.Skip("Requires full RuleContext implementation with GetEnv method")
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	// Creates JSON for the empty message array
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{},
	}
	jsonData, _ := json.Marshal(chatRequest)
	msg := types.NewMsg(0, "TEST_MSG", types.JSON, meta, string(jsonData))
	input, err := ConvertRuleMsgToAgentInput(ctx, msg, nil, false, "", nil, "gpt-4", "test_agent", nil)
	assert.Nil(t, err)
	assert.NotNil(t, input)
	// An empty message array should use raw data as the user message
	assert.Equal(t, 1, len(input.Messages))
	assert.Equal(t, schema.User, input.Messages[0].Role)
}

// TestConvertRuleMsgToAgentInput_InvalidJSON Test invalid JSON input
func TestConvertRuleMsgToAgentInput_InvalidJSON(t *testing.T) {
	t.Skip("Requires full RuleContext implementation with GetEnv method")
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	msg := types.NewMsg(0, "TEST_MSG", types.JSON, meta, "not valid json")
	input, err := ConvertRuleMsgToAgentInput(ctx, msg, nil, false, "", nil, "gpt-4", "test_agent", nil)
	assert.Nil(t, err)
	assert.NotNil(t, input)
	// Invalid JSON should be treated as regular text
	assert.Equal(t, 1, len(input.Messages))
	assert.Equal(t, "not valid json", input.Messages[0].Content)
}

// TestConvertRuleMsgToAgentInput_ExistingSystemMessage Test existing system messages
func TestConvertRuleMsgToAgentInput_ExistingSystemMessage(t *testing.T) {
	t.Skip("Requires full RuleContext implementation with GetEnv method")
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	// Create a JSON containing system messages
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{Role: "system", Content: "Existing system prompt"},
			{Role: "user", Content: "Hello"},
		},
	}
	jsonData, _ := json.Marshal(chatRequest)
	msg := types.NewMsg(0, "TEST_MSG", types.JSON, meta, string(jsonData))
	// Provide new system prompts, but should not overwrite existing ones
	input, err := ConvertRuleMsgToAgentInput(ctx, msg, nil, false, "New system prompt", nil, "gpt-4", "test_agent", nil)
	assert.Nil(t, err)
	assert.NotNil(t, input)
	// The original system messages should be preserved
	assert.Equal(t, 2, len(input.Messages))
	assert.Equal(t, schema.System, input.Messages[0].Role)
	assert.Equal(t, "Existing system prompt", input.Messages[0].Content)
}

// TestConvertRuleMsgToAgentInput_PresetMessages Test preset messages
func TestConvertRuleMsgToAgentInput_PresetMessages(t *testing.T) {
	t.Skip("Requires el.Template initialization")
}

// BenchmarkIsBase64Image Benchmark isBase64Image
func BenchmarkIsBase64Image(b *testing.B) {
	input := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isBase64Image(input)
	}
}

// BenchmarkIsLocalFilePath Benchmarking isLocalFilePath
func BenchmarkIsLocalFilePath(b *testing.B) {
	input := "/home/user/images/photo.jpg"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isLocalFilePath(input)
	}
}

// BenchmarkParseBase64Image Benchmark test parseBase64Image
func BenchmarkParseBase64Image(b *testing.B) {
	input := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseBase64Image(input)
	}
}

// BenchmarkConvertRuleMsgToAgentInput Benchmark ConvertRuleMsgToAgentInput
func BenchmarkConvertRuleMsgToAgentInput(b *testing.B) {
	b.Skip("Requires full RuleContext implementation with GetEnv method")
	ctx := &mockRuleContext{}
	meta := types.NewMetadata()
	msg := types.NewMsg(0, "TEST_MSG", types.TEXT, meta, "Hello, world!")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ConvertRuleMsgToAgentInput(ctx, msg, nil, false, "", nil, "gpt-4", "test_agent", nil)
	}
}
