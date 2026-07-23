//go:build integration

// Real LLM integration testing: configure with environment variables TEST_LLM_KEY / TEST_LLM_URL / TEST_LLM_MODEL,
// Skipping when TEST_LLM_KEY is not set to avoid hardcoded key failures and no-network/keyless environments.
// Runtime example:
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

// testLLMConfig reads the LLM configuration for testing from the environment variable; if not configured, it is skipped.
func testLLMConfig(t *testing.T) *config.LLMConfig {
	t.Helper()
	key := os.Getenv("TEST_LLM_KEY")
	if key == "" {
		t.Skip("TEST_LLM_KEY Not set, skip the real LLM integration test")
	}
	url := os.Getenv("TEST_LLM_URL")
	model := os.Getenv("TEST_LLM_MODEL")
	if url == "" || model == "" {
		t.Skip("TEST_LLM_URL / TEST_LLM_MODEL Not set, skip")
	}
	return &config.LLMConfig{
		Url:    url,
		Key:    key,
		Model:  model,
		Params: config.ModelParams{Temperature: 0.7, TopP: 0.9},
	}
}

// TestIntegration_LLM_Generate Verify that CreateChatModel (including retry wrappers) can realistically synchronize LLM generation.
func TestIntegration_LLM_Generate(t *testing.T) {
	cfg := testLLMConfig(t)
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := m.Generate(context.Background(),
		[]*schema.Message{schema.UserMessage("只回复两个字：你好")})
	if err != nil {
		t.Fatalf("Generate Failure: %v", err)
	}
	t.Logf("Generate Response: %q", resp.Content)
	if strings.TrimSpace(resp.Content) == "" {
		t.Error("The response content is empty")
	}
}

// TestIntegration_LLM_Stream Verify stream generation (off default mode, real-time).
func TestIntegration_LLM_Stream(t *testing.T) {
	cfg := testLLMConfig(t)
	m, err := CreateChatModel(*cfg, ModelOptions{WrapRetry: true, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	sr, err := m.Stream(context.Background(),
		[]*schema.Message{schema.UserMessage("从 1 数到 5，只输出数字和空格")})
	if err != nil {
		t.Fatalf("Stream Failure to build: %v", err)
	}
	var sb strings.Builder
	chunks := 0
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream Read failure: %v", err)
		}
		if msg.Content != "" {
			sb.WriteString(msg.Content)
			chunks++
		}
	}
	t.Logf("Stream Content: %q (chunks=%d)", sb.String(), chunks)
	if sb.Len() == 0 {
		t.Error("The content flows without merit")
	}
}

// TestIntegration_LLM_FullMode Verify streamRetryMode=full: The stream is fully buffered and replayed (received all at once).
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
		t.Fatalf("Stream Failure to build: %v", err)
	}
	var sb strings.Builder
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream Read failure: %v", err)
		}
		sb.WriteString(msg.Content)
	}
	t.Logf("FullMode Content: %q", sb.String())
	if sb.Len() == 0 {
		t.Error("The content flows without merit")
	}
}

// TestIntegration_LLM_FailoverStream Primary endpoint Stream failure → Backup endpoint Stream success (streaming failover).
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
		t.Fatalf("failover Stream Should succeed: %v", err)
	}
	var sb strings.Builder
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream Read failure: %v", err)
		}
		sb.WriteString(msg.Content)
	}
	t.Logf("Failover Stream Content: %q", sb.String())
	if sb.Len() == 0 {
		t.Error("The content flows without merit")
	}
}

// TestIntegration_LLM_FailoverFullMode Failover + Full mode combination:
// The primary endpoint (invalid) retries exhaustion → failover in full mode to the backup real endpoint stream successfully.
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
		t.Fatalf("failover full Stream Should succeed: %v", err)
	}
	var sb strings.Builder
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream Read failure: %v", err)
		}
		sb.WriteString(msg.Content)
	}
	t.Logf("Failover FullMode Stream Content: %q", sb.String())
	if sb.Len() == 0 {
		t.Error("The content flows without merit")
	}
}

// TestIntegration_LLM_Failover Failover verification: use invalid URLs for primary endpoints, real URLs for backup endpoints,
// Expect Generate to successfully switch to an alternate endpoint after the master endpoint fails.
func TestIntegration_LLM_Failover(t *testing.T) {
	cfg := testLLMConfig(t)
	realURL := cfg.Url
	// Backup = Real endpoint; The primary endpoint is intentionally written as an invalid domain name, triggering failover
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
		t.Fatalf("failover should succeed and got: %v", err)
	}
	t.Logf("Failover Response (from the standby endpoint): %q", resp.Content)
	if strings.TrimSpace(resp.Content) == "" {
		t.Error("The response content is empty")
	}
}
