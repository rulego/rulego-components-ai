//go:build integration

// 真实 LLM 集成测试：用环境变量 TEST_LLM_KEY / TEST_LLM_URL / TEST_LLM_MODEL 配置，
// 未设置 TEST_LLM_KEY 时跳过，避免硬编码 key 与无网络/无 key 环境失败。
// 运行示例：
//
//	TEST_LLM_KEY=xxx TEST_LLM_URL=https://... TEST_LLM_MODEL=glm-5.2 \
//	go test -tags integration ./agent/ -run TestIntegration -v -count=1
package agent

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
)

// testLLMConfig 从环境变量读取测试用 LLM 配置，未配置则跳过。
func testLLMConfig(t *testing.T) *config.LLMConfig {
	t.Helper()
	key := os.Getenv("TEST_LLM_KEY")
	if key == "" {
		t.Skip("TEST_LLM_KEY 未设置，跳过真实 LLM 集成测试")
	}
	url := os.Getenv("TEST_LLM_URL")
	model := os.Getenv("TEST_LLM_MODEL")
	if url == "" || model == "" {
		t.Skip("TEST_LLM_URL / TEST_LLM_MODEL 未设置，跳过")
	}
	return &config.LLMConfig{
		Url:    url,
		Key:    key,
		Model:  model,
		Params: config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
}

// TestIntegration_LLM_Generate 验证 CreateChatModel（含 retry 包装）能真实调通 LLM 同步生成。
func TestIntegration_LLM_Generate(t *testing.T) {
	cfg := testLLMConfig(t)
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := m.Generate(context.Background(),
		[]*schema.Message{schema.UserMessage("只回复两个字：你好")})
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	t.Logf("Generate 响应: %q", resp.Content)
	if strings.TrimSpace(resp.Content) == "" {
		t.Error("响应内容为空")
	}
}

// TestIntegration_LLM_Stream 验证流式生成（off 默认模式，实时）。
func TestIntegration_LLM_Stream(t *testing.T) {
	cfg := testLLMConfig(t)
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("从 1 数到 5，只输出数字和空格")})
	if err != nil {
		t.Fatalf("Stream 建立失败: %v", err)
	}
	var sb strings.Builder
	chunks := 0
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream 读取失败: %v", err)
		}
		if msg.Content != "" {
			sb.WriteString(msg.Content)
			chunks++
		}
	}
	t.Logf("Stream 内容: %q (chunks=%d)", sb.String(), chunks)
	if sb.Len() == 0 {
		t.Error("流内容为空")
	}
}

// TestIntegration_LLM_FullMode 验证 streamRetryMode=full：流被完整缓冲后重放（一次性收到）。
func TestIntegration_LLM_FullMode(t *testing.T) {
	cfg := testLLMConfig(t)
	cfg.StreamRetryMode = config.StreamRetryFull
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("从 1 数到 3，只输出数字和空格")})
	if err != nil {
		t.Fatalf("Stream 建立失败: %v", err)
	}
	var sb strings.Builder
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream 读取失败: %v", err)
		}
		sb.WriteString(msg.Content)
	}
	t.Logf("FullMode 流内容: %q", sb.String())
	if sb.Len() == 0 {
		t.Error("流内容为空")
	}
}

// TestIntegration_LLM_FailoverStream 主端点 Stream 失败 → 备用端点 Stream 成功（流式 failover）。
func TestIntegration_LLM_FailoverStream(t *testing.T) {
	cfg := testLLMConfig(t)
	realURL := cfg.Url
	cfg.Failover = []config.FailoverEndpoint{
		{Url: realURL, Key: cfg.Key, Model: cfg.Model},
	}
	cfg.Url = "https://invalid-failover-test.example.com/v1"

	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("从 1 数到 3，只输出数字和空格")})
	if err != nil {
		t.Fatalf("failover Stream 应成功: %v", err)
	}
	var sb strings.Builder
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream 读取失败: %v", err)
		}
		sb.WriteString(msg.Content)
	}
	t.Logf("Failover Stream 内容: %q", sb.String())
	if sb.Len() == 0 {
		t.Error("流内容为空")
	}
}

// TestIntegration_LLM_FailoverFullMode failover + full 模式组合：
// 主端点（无效）在 full 模式下重试耗尽 → failover 到备用真实端点流式成功。
func TestIntegration_LLM_FailoverFullMode(t *testing.T) {
	cfg := testLLMConfig(t)
	realURL := cfg.Url
	cfg.StreamRetryMode = config.StreamRetryFull
	cfg.Failover = []config.FailoverEndpoint{
		{Url: realURL, Key: cfg.Key, Model: cfg.Model},
	}
	cfg.Url = "https://invalid-failover-test.example.com/v1"

	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("从 1 数到 3，只输出数字和空格")})
	if err != nil {
		t.Fatalf("failover full Stream 应成功: %v", err)
	}
	var sb strings.Builder
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream 读取失败: %v", err)
		}
		sb.WriteString(msg.Content)
	}
	t.Logf("Failover FullMode Stream 内容: %q", sb.String())
	if sb.Len() == 0 {
		t.Error("流内容为空")
	}
}

// TestIntegration_LLM_Failover 验证故障转移：主端点用无效 URL，备用端点用真实 URL，
// 期望 Generate 在主端点失败后切换到备用端点成功。
func TestIntegration_LLM_Failover(t *testing.T) {
	cfg := testLLMConfig(t)
	realURL := cfg.Url
	// 备用 = 真实端点；主端点故意写成无效域名触发 failover
	cfg.Failover = []config.FailoverEndpoint{
		{Url: realURL, Key: cfg.Key, Model: cfg.Model},
	}
	cfg.Url = "https://invalid-failover-test.example.com/v1"

	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := m.Generate(context.Background(),
		[]*schema.Message{schema.UserMessage("只回复两个字：你好")})
	if err != nil {
		t.Fatalf("failover 后应成功，got: %v", err)
	}
	t.Logf("Failover 响应（来自备用端点）: %q", resp.Content)
	if strings.TrimSpace(resp.Content) == "" {
		t.Error("响应内容为空")
	}
}
