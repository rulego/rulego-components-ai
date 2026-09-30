// client.go 纯标准库的 OpenAI 兼容对话客户端(/chat/completions)。
//
// 不引入 eino/sonic,32 位平台可编译——这是 agent/lite 独立于 agent/ 存在的原因。
//
// 原生 delta 协议里 tool_calls 是一等增量(按 index 累积),不存在"流转
// Message 转换误判"一类问题;reasoning_content 直接透传。
package lite

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Message 对话消息(role/content/tool_calls/tool_call_id)。
// ToolCalls 仅 assistant 消息携带(发起工具调用);ToolCallID 仅 role=tool 携带(回传结果)。
// ContentParts 非空时 content 序列化为多模态 parts 数组(见 MarshalJSON),
// 反序列化同时接受 string 与数组两种形态。
type Message struct {
	Role         string        `json:"role"`
	Content      string        `json:"content"`
	ContentParts []ContentPart `json:"-"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
}

// MarshalJSON ContentParts 非空时 content 输出 parts 数组,否则输出字符串。
// 内部构造的 assistant/tool 消息只填 Content,继续走字符串形态。
func (m Message) MarshalJSON() ([]byte, error) {
	type messagePlain struct {
		Role       string     `json:"role"`
		Content    string     `json:"content"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}
	if len(m.ContentParts) == 0 {
		return json.Marshal(messagePlain{
			Role:       m.Role,
			Content:    m.Content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		})
	}
	type messageParts struct {
		Role       string        `json:"role"`
		Content    []ContentPart `json:"content"`
		ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
		ToolCallID string        `json:"tool_call_id,omitempty"`
	}
	return json.Marshal(messageParts{
		Role:       m.Role,
		Content:    m.ContentParts,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
	})
}

// UnmarshalJSON content 兼容 string 与 parts 数组两种形态(数组时填充 ContentParts)。
func (m *Message) UnmarshalJSON(data []byte) error {
	type messagePlain struct {
		Role       string     `json:"role"`
		Content    string     `json:"content"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}
	var p messagePlain
	if err := json.Unmarshal(data, &p); err == nil {
		*m = Message{
			Role:       p.Role,
			Content:    p.Content,
			ToolCalls:  p.ToolCalls,
			ToolCallID: p.ToolCallID,
		}
		return nil
	}
	type messageParts struct {
		Role       string        `json:"role"`
		Content    []ContentPart `json:"content"`
		ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
		ToolCallID string        `json:"tool_call_id,omitempty"`
	}
	var q messageParts
	if err := json.Unmarshal(data, &q); err != nil {
		return err
	}
	*m = Message{
		Role:         q.Role,
		ContentParts: q.Content,
		ToolCalls:    q.ToolCalls,
		ToolCallID:   q.ToolCallID,
	}
	return nil
}

// ContentPart 多模态 content 元素:文本或图片引用。
type ContentPart struct {
	Type string `json:"type"` // "text" | "image_url"
	Text string `json:"text,omitempty"`
	// ImageURL 仅 type=image_url 时非 nil;url 支持 http(s) 与 data:base64,纯透传。
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL 图片引用。
type ImageURL struct {
	URL string `json:"url"`
}

// TextPart 构造文本 part。
func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

// ImagePart 构造图片 part(url/base64 均可)。
func ImagePart(url string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: url}}
}

// ToolCall assistant 发起的一次工具调用。
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"` // 固定 "function"
	Function FuncCall `json:"function"`
}

// FuncCall 工具名与 JSON 参数串。
type FuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool 传给 LLM 的工具定义(JSON Schema 内联)。
type Tool struct {
	Type     string       `json:"type"` // 固定 "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction 工具名/描述/入参 schema。
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Params 采样参数(零值字段不发送)。字段名遵循 OpenAI 请求体协议
// (top_p/max_completion_tokens),与节点配置的同名字段(topP/maxTokens)
// 书写不同;maxTokens 映射到 max_completion_tokens 与 eino 版行为一致。
type Params struct {
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	MaxTokens   int     `json:"max_completion_tokens,omitempty"`
}

// ResponseFormat 输出格式约束(type=json_object 等)。
type ResponseFormat struct {
	Type string `json:"type"`
}

// Request 一次对话请求。
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Params
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
	// ExtraFields 供应商扩展参数(thinking.type/reasoning_effort 等),序列化时
	// 原样并入请求体顶层;与常规字段同名时常规字段优先。
	ExtraFields map[string]any `json:"-"`
}

// MarshalJSON 常规字段序列化后并入 ExtraFields 顶层条目。
func (r Request) MarshalJSON() ([]byte, error) {
	type requestFields Request
	if len(r.ExtraFields) == 0 {
		return json.Marshal(requestFields(r))
	}
	base, err := json.Marshal(requestFields(r))
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(base, &body); err != nil {
		return nil, err
	}
	for k, v := range r.ExtraFields {
		if _, taken := body[k]; !taken {
			body[k] = v
		}
	}
	return json.Marshal(body)
}

// Response 非流式响应(只取首 choice)。
type Response struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

// Usage token 统计。CachedTokens 来自 prompt_tokens_details(缓存命中的
// 输入部分),供应商不给时为 0。
type Usage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

// CachedTokens 缓存命中输入 token(无明细时 0)。
func (u Usage) CachedTokens() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CachedTokens
}

// StreamDelta 流式一帧的增量内容(三种互斥:正文/思考/工具调用增量)。
type StreamDelta struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCallDelta // 工具调用增量:按 Index 累积
	FinishReason     string          // 最后一帧非空
	Usage            *Usage          // 收到 usage 的帧非 nil
}

// ToolCallDelta 流式工具调用增量:ID/Name 通常在首帧,Arguments 逐帧追加。
// Index=-1 表示上游未提供(实现按出现顺序处理)。
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// 重试退避:500ms 起指数(500ms→1s→2s),重试次数内封顶 2s。
const (
	defaultMaxRetries  = 3
	retryBaseBackoff   = 500 * time.Millisecond
	retryBackoffCap    = 2 * time.Second
	retryResponseLimit = 8192 // 非 200 响应体读取上限(仅用于错误信息)
	maxResponseLimit   = 32 << 20
)

// Client OpenAI 兼容端点客户端。零值不可用,须 New。
type Client struct {
	baseURL    string
	apiKey     string
	http       *http.Client
	maxRetries int
}

// New 构造客户端。baseURL 形如 https://open.bigmodel.cn/api/paas/v4(不带 /chat/completions)。
// 默认自动重试 3 次(网络错误/超时/429/5xx/流建立中断),SetMaxRetries 可调。
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		// 传输层超时兜底,不用 Client.Timeout——它覆盖整个响应体读取,会掐断合法的长流式回合。
		// ResponseHeaderTimeout 须大于最慢的非流式首字节(深度思考模型可达数分钟)。
		http: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 5 * time.Minute,
			},
		},
		maxRetries: defaultMaxRetries,
	}
}

// SetMaxRetries 设置自动重试次数上限(不含首次),n<=0 保持默认。
func (c *Client) SetMaxRetries(n int) {
	if n > 0 {
		c.maxRetries = n
	}
}

// httpStatusError 携带状态码的错误(429/5xx 可重试,4xx 其余不重试)。
type httpStatusError struct {
	status int
	msg    string
}

func (e *httpStatusError) Error() string { return e.msg }

// retryable 判断错误是否可自动重试:网络错误/超时可;429/5xx 可;其余 4xx 确定性失败不可。
func retryable(err error) bool {
	if err == nil {
		return false
	}
	if se, ok := err.(*httpStatusError); ok {
		return se.status == http.StatusTooManyRequests || se.status >= 500
	}
	return true
}

// backoffFor 第 n 次重试(n 从 1 起)前的等待时长。
func backoffFor(n int) time.Duration {
	d := retryBaseBackoff << (n - 1)
	if d > retryBackoffCap {
		d = retryBackoffCap
	}
	return d
}

// sleepBackoff 等待退避;ctx 先到期则返回 ctx 错误。
func sleepBackoff(ctx context.Context, n int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(backoffFor(n)):
		return nil
	}
}

// Complete 非流式对话。网络错误/超时/429/5xx 自动重试(maxRetries 次)。
func (c *Client) Complete(ctx context.Context, req Request) (*Response, error) {
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		resp, err := c.completeOnce(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt >= c.maxRetries || ctx.Err() != nil || !retryable(err) {
			return nil, lastErr
		}
		if err := sleepBackoff(ctx, attempt+1); err != nil {
			return nil, lastErr
		}
	}
}

func (c *Client) completeOnce(ctx context.Context, body []byte) (*Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("无法连接模型服务: %w", err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseLimit))
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp.StatusCode, data)
	}
	if readErr != nil {
		return nil, fmt.Errorf("模型响应读取中断: %w", readErr)
	}
	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("模型响应解析失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("模型响应无 choices")
	}
	return &out, nil
}

// StreamHandler 流式回调。Content/Reasoning 逐帧到达;Done 在流结束(含 finish_reason
// 与 usage)时调用一次;出错通过返回 error 表达。
type StreamHandler func(delta StreamDelta) error

// Stream 流式对话。返回结束帧的 finish_reason 与 usage。
//
// 自动重试仅覆盖"流建立阶段"(连接失败/非 200/读到首个增量前中断):一旦有任何
// delta 交付给回调,后续中断不再重试——重放会把已输出的内容重复发给下游。
func (c *Client) Stream(ctx context.Context, req Request, onDelta StreamHandler) (finish string, usage Usage, err error) {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return "", usage, err
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		delivered := false
		finish, usage, err = c.streamOnce(ctx, body, StreamHandler(func(d StreamDelta) error {
			delivered = true
			return onDelta(d)
		}))
		if err == nil {
			return finish, usage, nil
		}
		lastErr = err
		if attempt >= c.maxRetries || ctx.Err() != nil || delivered || !retryable(err) {
			return "", usage, lastErr
		}
		if err := sleepBackoff(ctx, attempt+1); err != nil {
			return "", usage, lastErr
		}
	}
}

func (c *Client) streamOnce(ctx context.Context, body []byte, onDelta StreamHandler) (finish string, usage Usage, err error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", usage, err
	}
	c.setHeaders(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", usage, fmt.Errorf("无法连接模型服务: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, retryResponseLimit))
		return "", usage, statusError(resp.StatusCode, data)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var frame struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
				Usage        *Usage `json:"usage"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		if json.Unmarshal([]byte(payload), &frame) != nil {
			continue
		}
		if frame.Usage != nil {
			usage = *frame.Usage
		}
		if len(frame.Choices) == 0 {
			continue
		}
		ch := frame.Choices[0]
		if len(ch.Delta.ToolCalls) > 0 {
			tcs := make([]ToolCallDelta, 0, len(ch.Delta.ToolCalls))
			for _, tc := range ch.Delta.ToolCalls {
				tcs = append(tcs, ToolCallDelta{
					Index:     tc.Index,
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
			if err := onDelta(StreamDelta{ToolCalls: tcs}); err != nil {
				return "", usage, err
			}
		}
		if ch.Delta.Content != "" || ch.Delta.ReasoningContent != "" {
			if err := onDelta(StreamDelta{Content: ch.Delta.Content, ReasoningContent: ch.Delta.ReasoningContent}); err != nil {
				return "", usage, err
			}
		}
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		return finish, usage, fmt.Errorf("模型流读取中断: %w", err)
	}
	return finish, usage, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// statusError 把非 200 响应转成带服务端 message 的错误。
func statusError(status int, body []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return &httpStatusError{status: status, msg: fmt.Sprintf("模型服务返回 %d: %s", status, e.Error.Message)}
	}
	return &httpStatusError{status: status, msg: fmt.Sprintf("模型服务返回 %d: %s", status, truncate(string(body), 300))}
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
