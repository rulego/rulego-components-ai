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

package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client System One API 客户端，纯标准库实现，无第三方依赖。
// 接口契约见 https://docs.typesafe.ai/api.md；429/529 是过载信号，按指数退避重试。
type Client struct {
	httpClient *http.Client
}

// NewClient 创建客户端，timeout 为单次 HTTP 请求超时。
func NewClient(timeout time.Duration) *Client {
	return &Client{httpClient: &http.Client{Timeout: timeout}}
}

// Request System One 请求。state 支持字符串或结构化 JSON（对象/数组），
// questions 为问题 id 到问题文档的映射，答案按相同 id 返回。
type Request struct {
	Model     string                 `json:"model"`
	State     interface{}            `json:"state"`
	Questions map[string]interface{} `json:"questions"`
}

// Answer 三种答案的并集：choice 带 choice/confidence/probabilities，
// score 带 score/probabilities/confidence，noul 只带 0~1 概率、无置信度。
type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// PrimaryValue 答案主值：choice 为选项名，noul/score 为数值。
func (a *Answer) PrimaryValue() string {
	switch a.Type {
	case "choice":
		return a.Choice
	case "noul":
		if a.Noul != nil {
			return formatFloat(*a.Noul)
		}
	case "score":
		if a.Score != nil {
			return formatFloat(*a.Score)
		}
	}
	return ""
}

// Response System One 响应。
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Query 调用 System One 接口。maxRetries 为 429/529/网络错误的额外重试次数，
// 查询型接口无副作用，重试安全。
func (c *Client) Query(ctx context.Context, url, apiKey string, req *Request, maxRetries int) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(200<<(attempt-1)) * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %v", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("failed to call System One API: %v", err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %v", err)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529 {
			lastErr = fmt.Errorf("System One API overloaded (HTTP %d)", resp.StatusCode)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("System One API error (HTTP %d): %s", resp.StatusCode, truncate(string(data), 200))
		}

		var result Response
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %v", err)
		}
		return &result, nil
	}
	return nil, lastErr
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
