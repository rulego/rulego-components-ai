/*
 * Copyright 2026 The RuleGo Authors.
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

package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Multimodal image recognition testing
// ============================================================================
//
// How to use:
//
//	Method 1: Set environment variables
//	  export LLM_API_KEY="your-api-key"
//	  export LLM_BASE_URL="https://coding.dashscope.aliyuncs.com/v1"
//	  export LLM_MODEL="qwen3.5-plus"
//	  go test -v -run TestMultimodal ./...
//
//	Method 2: Run directly (using built-in default configuration)
//	  go test -v -run TestMultimodal ./...
//
// ============================================================================

// Alibaba Cloud Tongyi Qianwen multimodal test configuration
const (
	// Default configuration (Alibaba Cloud DashScope API)
	defaultQwenBaseURL = "https://coding.dashscope.aliyuncs.com/v1"
	defaultQwenModel   = "qwen3.5-plus"
)

// getMultimodalTestConfig to obtain the multimodal test configuration
// Prioritize using LLM_VISION_* environment variables, revert to LLM_* environment variables, and finally use the default configuration
func getMultimodalTestConfig() config.LLMConfig {
	apiKey := os.Getenv("LLM_VISION_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("LLM_API_KEY")
	}

	baseURL := os.Getenv("LLM_VISION_BASE_URL")
	if baseURL == "" {
		baseURL = os.Getenv("LLM_BASE_URL")
		if baseURL == "" {
			baseURL = defaultQwenBaseURL
		}
	}

	model := os.Getenv("LLM_VISION_MODEL")
	if model == "" {
		model = os.Getenv("LLM_MODEL")
		if model == "" {
			model = defaultQwenModel
		}
	}

	return config.LLMConfig{
		Url:   baseURL,
		Key:   apiKey,
		Model: model,
		Params: config.ModelParams{
			Temperature: 0.7,
			MaxTokens:   2048,
		},
		MaxRetries: 3,
	}
}

// skipIfNoAPIKeyForMultimodal If there is no API Key or the model does not support vision, the test is skipped
func skipIfNoAPIKeyForMultimodal(t *testing.T, cfg config.LLMConfig) {
	if cfg.Key == "" {
		t.Skip("Skipping test: LLM_API_KEY environment variable not set. " +
			"Please set LLM_API_KEY to run multimodal vision tests.")
	}
	if !config.SupportsVision(cfg.Model) {
		t.Skipf("Skipping multimodal test: model %s does not support vision. "+
			"Set LLM_VISION_MODEL to a vision model (e.g. glm-4.6v).", cfg.Model)
	}
}

// ============================================================================
// Test 1: Model creation and visual ability assessment
// ============================================================================

// TestMultimodal_CreateChatModel Test the creation of chat models that support visual abilities
func TestMultimodal_CreateChatModel(t *testing.T) {
	cfg := getMultimodalTestConfig()
	skipIfNoAPIKeyForMultimodal(t, cfg)

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})

	require.NoError(t, err, "Failed to create chat model")
	require.NotNil(t, chatModel, "Chat model should not be nil")

	t.Logf("✅ Chat model created successfully for model: %s", cfg.Model)
}

// TestMultimodal_VisionCapabilityDetection Test model visual ability assessment
// Note: Due to the configuration using strings.Contains performs pattern matching, map traversal order is uncertain,
// Some model names may be pre-matched by shorter patterns (e.g., gpt-4o may be matched by GPT-4)
// Here, only the key models that will not be pre-matched are tested
//
// Configure with a conservative default policy: unknown models support all capabilities by default (including vision)
func TestMultimodal_VisionCapabilityDetection(t *testing.T) {
	tests := []struct {
		modelName      string
		description    string
		checkSupported bool // Check whether vision is supported; false means the check does not support it
	}{
		// Alibaba Tongyi Series (Key Model)
		{"qwen3.5-plus", "Qwen3.5 Plus supports vision", true},
		{"qwen-vl-max", "Qwen VL Max supports vision", true},
		{"qwen2-vl", "Qwen2 VL supports vision", true},

		// Claude Series (not affected by other modes)
		{"claude-3-opus", "Claude 3 supports vision", true},
		{"claude-4-sonnet", "Claude 4 supports vision", true},

		// Zhipu GLM Series (Note: glm-4-vision may be matched first by the GLM-4 mode)
		// Use the more specific name glm-4.6v for testing
		{"glm-4.6v", "GLM-4.6V supports vision", true},

		// Unknown model (conservative default supports vision)
		{"unknown-model-xyz", "Unknown models default to supporting vision", true},
	}

	for _, tt := range tests {
		t.Run(tt.modelName, func(t *testing.T) {
			result := config.SupportsVision(tt.modelName)
			if tt.checkSupported {
				assert.True(t, result, tt.description)
			} else {
				assert.False(t, result, tt.description)
			}
			t.Logf("  %s: SupportsVision=%v", tt.modelName, result)
		})
	}
}

// ============================================================================
// Test 2: URL image recognition
// ============================================================================

// TestMultimodal_ImageRecognitionWithURL Testing uses URL images for recognition
func TestMultimodal_ImageRecognitionWithURL(t *testing.T) {
	cfg := getMultimodalTestConfig()
	skipIfNoAPIKeyForMultimodal(t, cfg)

	// Validation models support visual capabilities
	require.True(t, config.SupportsVision(cfg.Model),
		"Model %s should support vision capability", cfg.Model)

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Using sample images from Alibaba Cloud's official documentation
	// Note: Alibaba Cloud's Qwen VL model has specific requirements for image URLs
	imageURL := "https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg"

	// Build multimodal messaging
	messages := []*schema.Message{
		schema.SystemMessage("你是一个图像识别助手。请简洁地回答问题。"),
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{
						MessagePartCommon: schema.MessagePartCommon{
							URL: &imageURL,
						},
						Detail: "auto",
					},
				},
				{
					Type: schema.ChatMessagePartTypeText,
					Text: "请描述这张图片的内容，用一句话回答。",
				},
			},
		},
	}

	response, err := chatModel.Generate(ctx, messages)
	require.NoError(t, err, "Failed to generate response with image URL")
	require.NotNil(t, response)
	require.NotEmpty(t, response.Content, "Response content should not be empty")

	t.Logf("✅ Image URL recognition successful")
	t.Logf("   Response: %s", truncateString(response.Content, 300))
}

// TestMultimodal_ImageRecognitionWithMultipleURLs Testing multiple URL image recognition
func TestMultimodal_ImageRecognitionWithMultipleURLs(t *testing.T) {
	cfg := getMultimodalTestConfig()
	skipIfNoAPIKeyForMultimodal(t, cfg)

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Using official Alibaba Cloud test images
	imageURLs := []string{
		"https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg",
	}

	var imageParts []schema.MessageInputPart
	for _, url := range imageURLs {
		imageParts = append(imageParts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{
					URL: &url,
				},
				Detail: "auto",
			},
		})
	}

	// Add text questions
	imageParts = append(imageParts, schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeText,
		Text: "请描述这张图片，用一句话概括。",
	})

	messages := []*schema.Message{
		schema.SystemMessage("你是一个图像识别助手。"),
		{
			Role:                  schema.User,
			UserInputMultiContent: imageParts,
		},
	}

	response, err := chatModel.Generate(ctx, messages)
	require.NoError(t, err)
	require.NotEmpty(t, response.Content)

	t.Logf("✅ Multiple image URLs recognition successful")
	t.Logf("   Response: %s", truncateString(response.Content, 200))
}

// ============================================================================
// Test 3: Base64 image recognition
// ============================================================================

// TestMultimodal_ImageRecognitionWithBase64 Test using images encoded in Base64 for recognition
func TestMultimodal_ImageRecognitionWithBase64(t *testing.T) {
	cfg := getMultimodalTestConfig()
	skipIfNoAPIKeyForMultimodal(t, cfg)

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Create a simple test image (1x1 red pixel PNG)
	// This is the smallest valid PNG file in Base64 encoding
	smallRedPng := createTestPNGBase64()

	// Build multimodal messaging
	messages := []*schema.Message{
		schema.SystemMessage("你是一个图像识别助手。"),
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{
						MessagePartCommon: schema.MessagePartCommon{
							Base64Data: &smallRedPng,
							MIMEType:   "image/png",
						},
						Detail: "auto",
					},
				},
				{
					Type: schema.ChatMessagePartTypeText,
					Text: "请描述这张图片的颜色。",
				},
			},
		},
	}

	response, err := chatModel.Generate(ctx, messages)
	if err != nil {
		if strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "Service Unavailable") {
			t.Skipf("API returned 503 Service Unavailable, skipping: %v", err)
		}
	}
	require.NoError(t, err, "Failed to generate response with Base64 image")
	require.NotNil(t, response)
	require.NotEmpty(t, response.Content)

	t.Logf("✅ Base64 image recognition successful")
	t.Logf("   Response: %s", truncateString(response.Content, 200))
}

// createTestPNGBase64 åå»ºä¸ä¸ªæµè¯ PNG å¾çç Base64 ç¼ç 
// ä½¿ç¨ Go æ ååºçæ 10x10 çº¢è² PNGï¼å¼å®¹å LLM API çå¾çè§£æè¦æ±
func createTestPNGBase64() string {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ============================================================================
// Test 4: Streaming image recognition
// ============================================================================

// TestMultimodal_StreamImageRecognition Test streaming image recognition
func TestMultimodal_StreamImageRecognition(t *testing.T) {
	cfg := getMultimodalTestConfig()
	skipIfNoAPIKeyForMultimodal(t, cfg)

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	imageURL := "https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg"

	messages := []*schema.Message{
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{
						MessagePartCommon: schema.MessagePartCommon{
							URL: &imageURL,
						},
						Detail: "auto",
					},
				},
				{
					Type: schema.ChatMessagePartTypeText,
					Text: "请用一段话描述这张图片。",
				},
			},
		},
	}

	stream, err := chatModel.Stream(ctx, messages)
	require.NoError(t, err, "Failed to create stream")
	require.NotNil(t, stream)

	var fullContent string
	chunkCount := 0

	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		chunkCount++
		fullContent += chunk.Content

		// Each chunk is printed only in debug mode
		if isDebugMode() && chunkCount <= 5 {
			t.Logf("  Chunk %d: %s", chunkCount, truncateString(chunk.Content, 50))
		}
	}

	require.NotEmpty(t, fullContent, "Stream content should not be empty")
	t.Logf("✅ Stream image recognition completed: %d chunks, %d chars",
		chunkCount, len(fullContent))
	t.Logf("   Full response: %s", truncateString(fullContent, 200))
}

// ============================================================================
// Test 5: Multimodal message transformation
// ============================================================================

// TestMultimodal_MessageConversionWithURL Test message conversion for URL images
func TestMultimodal_MessageConversionWithURL(t *testing.T) {
	// Testing multimodal message conversion in OpenAI format
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "这是什么？"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/test.png",
						},
					},
				},
			},
		},
	}

	// Verify message resolution
	msg := chatRequest.Messages[0]
	assert.True(t, msg.IsMultimodal(), "Message should be multimodal")

	images := msg.GetAllImages()
	assert.Len(t, images, 1)
	assert.Equal(t, "https://example.com/test.png", images[0])

	textContent := msg.GetContentAsString()
	assert.Equal(t, "这是什么？", textContent)

	t.Logf("✅ Message conversion test passed")
}

// TestMultimodal_MessageConversionWithBase64 Testing message conversion for Base64 images
func TestMultimodal_MessageConversionWithBase64(t *testing.T) {
	base64Img := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "分析这张图片"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": base64Img,
						},
					},
				},
			},
		},
	}

	msg := chatRequest.Messages[0]
	assert.True(t, msg.IsMultimodal())

	images := msg.GetAllImages()
	assert.Len(t, images, 1)
	assert.Equal(t, base64Img, images[0])

	t.Logf("✅ Base64 message conversion test passed")
}

// TestMultimodal_MultipleImagesInMessage Test for multiple images in a single message
func TestMultimodal_MultipleImagesInMessage(t *testing.T) {
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "比较这两张图片"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/image1.png",
						},
					},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/image2.png",
						},
					},
				},
			},
		},
	}

	msg := chatRequest.Messages[0]
	assert.True(t, msg.IsMultimodal())

	images := msg.GetAllImages()
	assert.Len(t, images, 2)

	t.Logf("✅ Multiple images message test passed: %d images", len(images))
}

// ============================================================================
// Test 6: Image Processing in Multi-Turn Dialogue
// ============================================================================

// TestMultimodal_MultiTurnWithImages Test image processing in multi-turn conversations
func TestMultimodal_MultiTurnWithImages(t *testing.T) {
	cfg := getMultimodalTestConfig()
	skipIfNoAPIKeyForMultimodal(t, cfg)

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:     NewTestLogger(t),
		WrapRetry:  true,
		MaxRetries: cfg.MaxRetries,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	imageURL := "https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg"

	// Round 1: Send pictures and ask questions
	messages := []*schema.Message{
		schema.SystemMessage("你是一个图像识别助手。"),
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{
						MessagePartCommon: schema.MessagePartCommon{
							URL: &imageURL,
						},
						Detail: "auto",
					},
				},
				{
					Type: schema.ChatMessagePartTypeText,
					Text: "请描述这张图片的主要内容。",
				},
			},
		},
	}

	response1, err := chatModel.Generate(ctx, messages)
	require.NoError(t, err, "First turn failed")
	t.Logf("✅ Turn 1 response: %s", truncateString(response1.Content, 150))

	// Round 2: Follow-up questions (without pictures)
	messages = append(messages, response1)
	messages = append(messages, schema.UserMessage("你提到的内容中，最主要的颜色是什么？"))

	response2, err := chatModel.Generate(ctx, messages)
	require.NoError(t, err, "Second turn failed")
	t.Logf("✅ Turn 2 response: %s", truncateString(response2.Content, 150))

	// The validation model can remember the image content from the first round
	assert.NotEmpty(t, response2.Content)
}

// ============================================================================
// Test 7: Error handling
// ============================================================================

// TestMultimodal_InvalidURL Error handling of invalid testing URLs
func TestMultimodal_InvalidURL(t *testing.T) {
	cfg := getMultimodalTestConfig()
	skipIfNoAPIKeyForMultimodal(t, cfg)

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		Logger:    NewTestLogger(t),
		WrapRetry: false, // Disable retries to speed up testing
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Using an invalid image URL
	invalidURL := "https://invalid-url-that-does-not-exist.com/fake-image.png"

	messages := []*schema.Message{
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{
						MessagePartCommon: schema.MessagePartCommon{
							URL: &invalidURL,
						},
						Detail: "auto",
					},
				},
				{
					Type: schema.ChatMessagePartTypeText,
					Text: "描述这张图片",
				},
			},
		},
	}

	// Note: Some models may ignore images that cannot be loaded and only respond to text
	// This test mainly verifies that there will be no panic or unexpected errors
	response, err := chatModel.Generate(ctx, messages)

	// Record results (failure is not mandatory, as different models handle them differently)
	if err != nil {
		t.Logf("⚠️ Expected error for invalid URL: %v", err)
	} else {
		t.Logf("✅ Model handled invalid URL gracefully: %s", truncateString(response.Content, 100))
	}
}

// TestMultimodal_EmptyImage Test empty image processing
func TestMultimodal_EmptyImage(t *testing.T) {
	// Test message construction for empty URLs
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "你好"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "", // Empty URL
						},
					},
				},
			},
		},
	}

	msg := chatRequest.Messages[0]
	images := msg.GetAllImages()

	// Empty URLs should not be included in the image list
	for _, img := range images {
		assert.NotEmpty(t, img, "Empty URLs should be filtered out")
	}

	t.Logf("✅ Empty image test passed: %d valid images", len(images))
}

// ============================================================================
// Test 8: Image format check
// ============================================================================

// TestMultimodal_ImageFormatDetection Test image format check
func TestMultimodal_ImageFormatDetection(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		isBase64 bool
		isLocal  bool
		isURL    bool
	}{
		{
			name:     "base64 png",
			input:    "data:image/png;base64,iVBORw0KGgo=",
			isBase64: true,
		},
		{
			name:     "base64 jpeg",
			input:    "data:image/jpeg;base64,/9j/4AAQSkZJRg==",
			isBase64: true,
		},
		{
			name:  "https url",
			input: "https://example.com/image.png",
			isURL: true,
		},
		{
			name:  "http url",
			input: "http://example.com/image.jpg",
			isURL: true,
		},
		{
			name:    "local path unix",
			input:   "/home/user/images/photo.png",
			isLocal: true,
		},
		{
			name:    "local path windows",
			input:   "C:\\Users\\Photos\\image.jpg",
			isLocal: true,
		},
		{
			name:    "relative path",
			input:   "./images/test.png",
			isLocal: true,
		},
		{
			name:  "plain text",
			input: "this is not an image",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isBase64 := isBase64Image(tt.input)
			isLocal := isLocalFilePath(tt.input)
			isURL := strings.HasPrefix(tt.input, "http://") || strings.HasPrefix(tt.input, "https://")

			assert.Equal(t, tt.isBase64, isBase64, "isBase64 check")
			assert.Equal(t, tt.isLocal, isLocal, "isLocal check")
			assert.Equal(t, tt.isURL, isURL, "isURL check")

			t.Logf("  Input: %s -> base64=%v, local=%v, url=%v",
				truncateString(tt.input, 30), isBase64, isLocal, isURL)
		})
	}
}

// ============================================================================
// Auxiliary function
// ============================================================================

// Note: The truncateString function is defined in aspect_integration.go, so it is used directly

// ============================================================================
// Benchmark Test
// ============================================================================

// BenchmarkMultimodal_VisionCapabilityDetection Benchmark visual ability assessment
func BenchmarkMultimodal_VisionCapabilityDetection(b *testing.B) {
	models := []string{
		"qwen3.5-plus",
		"gpt-4o",
		"glm-4v",
		"claude-3-opus",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, model := range models {
			_ = config.SupportsVision(model)
		}
	}
}

// BenchmarkMultimodal_MessageConversion Benchmark message transformation
func BenchmarkMultimodal_MessageConversion(b *testing.B) {
	chatRequest := config.MultiTurnChatRequest{
		Messages: []config.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "描述图片"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/test.png",
						},
					},
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = chatRequest.Messages[0].IsMultimodal()
		_ = chatRequest.Messages[0].GetAllImages()
		_ = chatRequest.Messages[0].GetContentAsString()
	}
}

// ============================================================================
// Example Tests
// ============================================================================

// Example_createChatModelWithVision Demonstrate how to create models that support vision
func TestExample_CreateChatModelWithVision(t *testing.T) {
	cfg := getMultimodalTestConfig()
	if cfg.Key == "" {
		t.Skip("LLM_API_KEY not set, skipping")
	}

	chatModel, err := CreateChatModel(cfg, ModelOptions{
		WrapRetry:  true,
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("Error: %v", err)
	}

	assert.NotNil(t, chatModel, "Model should be created")
	t.Logf("Model created: %v", chatModel != nil)
}

// Example_checkVisionSupport Demonstrates how to check the model's visual support
func Example_checkVisionSupport() {
	// Check whether the model supports vision
	modelName := "qwen3.5-plus"
	if config.SupportsVision(modelName) {
		fmt.Printf("Model %s supports vision\n", modelName)
	}
	// Output: Model qwen3.5-plus supports vision
}
