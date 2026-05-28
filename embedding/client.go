package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// EmbeddingClient 轻量 Embedding HTTP 客户端，兼容 OpenAI embedding API 格式。
// 支持私有化部署（HuggingFace TEI 等）和云端 API（Gitee AI 等）。
type EmbeddingClient struct {
	httpClient *http.Client
	URL        string // Embedding API 地址，如 http://localhost:8080/embed
	APIKey     string // API Key，私有部署可为空
	Model      string // 模型名，如 BgeSmallZh
}

// NewEmbeddingClient 创建 Embedding 客户端
func NewEmbeddingClient(url, apiKey, model string) *EmbeddingClient {
	return &EmbeddingClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		URL:        url,
		APIKey:     apiKey,
		Model:      model,
	}
}

// Embed 批量计算文本 embedding，返回向量列表。
// 请求格式：{"input": ["text1", "text2"], "model": "xxx"}
// 响应格式：{"data": [{"embedding": [0.1, 0.2, ...]}, ...]}
func (c *EmbeddingClient) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	requestBody := map[string]interface{}{
		"input": texts,
		"model": c.Model,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %v", err)
	}
	defer resp.Body.Close()

	responseData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding api error: status=%d, body=%s", resp.StatusCode, string(responseData))
	}

	return extractEmbeddings(responseData)
}

// extractEmbeddings 从 API 响应中提取 embedding 向量
func extractEmbeddings(data []byte) ([][]float64, error) {
	var response map[string]interface{}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	dataField, exists := response["data"]
	if !exists {
		return nil, fmt.Errorf("missing 'data' field in response")
	}

	dataArray, ok := dataField.([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid 'data' field format")
	}

	embeddings := make([][]float64, 0, len(dataArray))
	for i, item := range dataArray {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid item format at index %d", i)
		}

		embeddingField, exists := itemMap["embedding"]
		if !exists {
			return nil, fmt.Errorf("missing 'embedding' field at index %d", i)
		}

		embeddingArray, ok := embeddingField.([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid embedding format at index %d", i)
		}

		embedding := make([]float64, 0, len(embeddingArray))
		for _, val := range embeddingArray {
			floatVal, ok := val.(float64)
			if !ok {
				return nil, fmt.Errorf("non-numeric value in embedding at index %d", i)
			}
			embedding = append(embedding, floatVal)
		}
		embeddings = append(embeddings, embedding)
	}

	return embeddings, nil
}
