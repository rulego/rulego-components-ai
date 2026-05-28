package agent

import (
	"strings"
	"testing"

	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/test/assert"
)

// TestMaskAPIKey 测试 maskAPIKey 函数
func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal key",
			input:    "sk-1234567890abcdef",
			expected: "sk-1****cdef",
		},
		{
			name:     "short key",
			input:    "abc",
			expected: "****",
		},
		{
			name:     "exact 8 chars",
			input:    "12345678",
			expected: "****",
		},
		{
			name:     "9 chars key",
			input:    "123456789",
			expected: "1234****6789",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "****",
		},
		{
			name:     "long key",
			input:    "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890",
			expected: "sk-p****7890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskAPIKey(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMaskAPIKey_Format 测试 maskAPIKey 返回格式
func TestMaskAPIKey_Format(t *testing.T) {
	apiKey := "sk-1234567890abcdefghijklmnopqrstuvwxyz"
	result := maskAPIKey(apiKey)

	// 检查格式：前4位 + **** + 后4位
	// "sk-1234567890abcdefghijklmnopqrstuvwxyz" -> "sk-1****wxyz"
	assert.True(t, strings.HasPrefix(result, "sk-1"))
	assert.True(t, strings.HasSuffix(result, "wxyz"))
	if !strings.Contains(result, "****") {
		t.Errorf("Expected result to contain '****', got: %s", result)
	}

	// 验证长度
	parts := strings.Split(result, "****")
	assert.Equal(t, 2, len(parts))
	assert.Equal(t, 4, len(parts[0]))
	assert.Equal(t, 4, len(parts[1]))
}

// TestGenerateShortID 测试 generateShortID 函数
func TestGenerateShortID(t *testing.T) {
	id := generateShortID()

	// 检查长度
	assert.Equal(t, 6, len(id))

	// 检查只包含小写字母和数字
	for _, c := range id {
		assert.True(t, (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'))
	}
}

// TestGenerateShortID_Uniqueness 测试 generateShortID 唯一性
func TestGenerateShortID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)
	count := 1000

	for i := 0; i < count; i++ {
		id := generateShortID()
		ids[id] = true
	}

	// 由于是随机生成，可能有少量重复，但应该大部分唯一
	// 6位字符，36^6 = 2,176,782,336 种可能
	// 1000次调用应该几乎全部唯一
	uniquenessRate := float64(len(ids)) / float64(count)
	assert.True(t, uniquenessRate > 0.99, "Expected >99%% uniqueness, got %.2f%%", uniquenessRate*100)
}

// TestGenerateShortID_CharacterSet 测试 generateShortID 字符集
func TestGenerateShortID_CharacterSet(t *testing.T) {
	charset := "abcdefghijklmnopqrstuvwxyz0123456789"
	expectedChars := make(map[rune]bool)
	for _, c := range charset {
		expectedChars[c] = true
	}

	// 生成多个 ID 并验证字符
	foundChars := make(map[rune]bool)
	for i := 0; i < 1000; i++ {
		id := generateShortID()
		for _, c := range id {
			foundChars[c] = true
			assert.True(t, expectedChars[c], "Unexpected character: %c", c)
		}
	}
}

// TestApplyDefaultLLMParams 测试 applyDefaultLLMParams 方法
func TestApplyDefaultLLMParams(t *testing.T) {
	tests := []struct {
		name                    string
		initialParams           config.ModelParams
		expectedTemp            float32
		expectedTopP            float32
		expectedFreqPenalty     float32
		expectedPresencePenalty float32
	}{
		{
			name:                    "all zeros - apply defaults",
			initialParams:           config.ModelParams{},
			expectedTemp:            config.DefaultTemperature,
			expectedTopP:            config.DefaultTopP,
			expectedFreqPenalty:     config.DefaultFrequencyPenalty,
			expectedPresencePenalty: config.DefaultPresencePenalty,
		},
		{
			name: "custom values - keep them",
			initialParams: config.ModelParams{
				Temperature:      0.5,
				TopP:             0.8,
				FrequencyPenalty: 0.3,
				PresencePenalty:  0.4,
			},
			expectedTemp:            0.5,
			expectedTopP:            0.8,
			expectedFreqPenalty:     0.3,
			expectedPresencePenalty: 0.4,
		},
		{
			name: "partial custom - mix defaults and custom",
			initialParams: config.ModelParams{
				Temperature: 0.6,
				// TopP is 0, should use default
				FrequencyPenalty: 0.2,
				// PresencePenalty is 0, should use default
			},
			expectedTemp:            0.6,
			expectedTopP:            config.DefaultTopP,
			expectedFreqPenalty:     0.2,
			expectedPresencePenalty: config.DefaultPresencePenalty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &ReactAgentNode{
				Config: ReactAgentNodeConfig{
					ChatAgentConfig: ChatAgentConfig{
						LLMConfig: config.LLMConfig{
							Params: tt.initialParams,
						},
					},
				},
			}

			node.applyDefaultLLMParams()

			assert.Equal(t, tt.expectedTemp, node.Config.Params.Temperature)
			assert.Equal(t, tt.expectedTopP, node.Config.Params.TopP)
			assert.Equal(t, tt.expectedFreqPenalty, node.Config.Params.FrequencyPenalty)
			assert.Equal(t, tt.expectedPresencePenalty, node.Config.Params.PresencePenalty)
		})
	}
}

// TestReactAgentNode_New 测试 New 方法
func TestReactAgentNode_New(t *testing.T) {
	node := &ReactAgentNode{}
	newNode := node.New()

	assert.NotNil(t, newNode)

	// 验证类型
	reactNode, ok := newNode.(*ReactAgentNode)
	assert.True(t, ok)
	assert.NotNil(t, reactNode)

	// 验证默认值
	assert.Equal(t, float32(0.7), reactNode.Config.Params.Temperature)
	assert.Equal(t, float32(0.9), reactNode.Config.Params.TopP)
	assert.Equal(t, float32(0.5), reactNode.Config.Params.FrequencyPenalty)
	assert.Equal(t, float32(0.5), reactNode.Config.Params.PresencePenalty)
	assert.Equal(t, 150, reactNode.Config.MaxStep)
}
