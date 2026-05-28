// Package constants provides centralized constant definitions for the AI components.
// This package aims to eliminate hardcoded values and improve maintainability.
package constants

// LLM Provider URLs
const (
	// OpenAI API endpoints
	ProviderOpenAI = "https://api.openai.com/v1"

	// Gitee AI API endpoint (Chinese alternative)
	ProviderGiteeAI = "https://ai.gitee.com/v1"

	// Zhipu AI (GLM) API endpoint
	ProviderZhipuAI = "https://open.bigmodel.cn/api/paas/v4"

	// Ollama local API endpoint
	ProviderOllama = "http://localhost:11434/v1"

	// DeepSeek API endpoint
	ProviderDeepSeek = "https://api.deepseek.com/v1"

	// Moonshot AI (Kimi) API endpoint
	ProviderMoonshot = "https://api.moonshot.cn/v1"

	// Alibaba Qwen API endpoint
	ProviderQwen = "https://dashscope.aliyuncs.com/compatible-mode/v1"
)

// Embedding Service URLs
const (
	// OpenAI Embedding API endpoint
	EmbeddingOpenAI = "https://api.openai.com/v1/embeddings"

	// Gitee AI Embedding API endpoint
	EmbeddingGiteeAI = "https://ai.gitee.com/v1/embeddings"
)

// Vector Database Addresses
const (
	// Default Redis address
	DefaultRedisAddr = "127.0.0.1:6379"

	// Default Milvus address
	DefaultMilvusAddr = "localhost:19530"
)

// MCP Server Addresses
const (
	// Default MCP server address
	DefaultMCPServerAddr = "http://localhost:8080/mcp"
)
