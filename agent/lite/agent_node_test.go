package lite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego-components-ai/config"
)

// fakeProvider 假工具提供者:echo 工具原样回参。
type fakeProvider struct{}

func (fakeProvider) ListToolDefinitions() ([]types.MCPToolDefinition, error) {
	return []types.MCPToolDefinition{{
		Name:        "echo",
		Description: "原样返回输入",
		InputSchema: []byte(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}}, nil
}

func (fakeProvider) CallTool(_ context.Context, name string, args map[string]interface{}) (string, error) {
	if name != "echo" {
		return "", fmt.Errorf("unknown tool %s", name)
	}
	b, _ := json.Marshal(map[string]any{"echoed": args["text"]})
	return string(b), nil
}

// sseLLM 伪 OpenAI 兼容 server:按脚本依次回 SSE 流,并记录每个请求体原文。
// 每个脚本是"SSE 帧列表"(不含 data: 前缀与 [DONE],自动补)。
type sseLLM struct {
	mu       sync.Mutex
	scripts  [][]string
	requests []string
}

func (s *sseLLM) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	idx := len(s.requests)
	s.requests = append(s.requests, string(body))
	var script []string
	if idx < len(s.scripts) {
		script = s.scripts[idx]
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	for _, frame := range script {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func contentFrame(text string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"content": text}}}})
	return string(b)
}

func tcFrame(index int, id, name, args string) string {
	fn := map[string]any{"arguments": args}
	if name != "" {
		fn["name"] = name
	}
	tc := map[string]any{"index": index, "function": fn}
	if id != "" {
		tc["id"] = id
	}
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"tool_calls": []any{tc}}}}})
	return string(b)
}

func doneFrame(reason string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{}, "finish_reason": reason}}})
	return string(b)
}

func usageFrame(prompt, completion int) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{}, "usage": map[string]any{
			"prompt_tokens": prompt, "completion_tokens": completion,
			"total_tokens": prompt + completion}})
	return string(b)
}

// buildAgentChain 构造 2 节点助手链并部署到独立池,返回引擎。
func buildAgentChain(t *testing.T, baseURL, extraConfig string, withProvider bool) types.RuleEngine {
	t.Helper()
	cfg := fmt.Sprintf(`{
		"url": %q, "key": "test-key", "model": "test-model",
		"maxStep": 5, "maxToolOutputLength": 1000,
		"systemPrompt": "你是测试助手", "skillsDir": ""%s
	}`, baseURL, extraConfig)
	def := fmt.Sprintf(`{
		"ruleChain": {"id": "t_agent_lite", "name": "t", "root": true},
		"metadata": {
			"nodes": [
				{"id": "n1", "type": %q, "name": "agent", "configuration": %s},
				{"id": "n2", "type": "end", "name": "end", "configuration": {}}
			],
			"connections": [
				{"fromId": "n1", "toId": "n2", "type": "Success"},
				{"fromId": "n1", "toId": "n2", "type": "Stream"}
			]
		}
	}`, NodeType, cfg)

	rc := rulego.NewConfig()
	if withProvider {
		rc.Udf = map[string]any{types.MCPToolProviderKey: fakeProvider{}}
	}
	pool := rulego.NewRuleGo()
	eng, err := pool.New("t_agent_lite", []byte(def), types.WithConfig(rc))
	if err != nil {
		t.Fatalf("部署链: %v", err)
	}
	return eng
}

// runStreamMsg 发送一条流式消息并收帧,full_content 到达或超时结束。
func runStreamMsg(t *testing.T, eng types.RuleEngine, data string) []types.RuleMsg {
	t.Helper()
	var frames []types.RuleMsg
	msg := types.NewMsg(0, "chat.completions", types.JSON, types.NewMetadata(), data)
	msg.Metadata.PutValue("stream", "true")
	wait := make(chan struct{})
	count := 0
	eng.OnMsg(msg, types.WithOnEnd(func(_ types.RuleContext, m types.RuleMsg, err error, _ string) {
		if err != nil {
			t.Errorf("链错误: %v", err)
		}
		frames = append(frames, m)
		count++
		if m.GetMetadata().GetValue("full_content") == "true" || count >= 20 {
			close(wait)
		}
	}))
	select {
	case <-wait:
	case <-time.After(30 * time.Second):
		t.Fatalf("等待链输出超时,已收 %d 帧", len(frames))
	}
	return frames
}

func TestAgentNodeFlows(t *testing.T) {
	srv := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()

	// 第一轮:正文 + 工具调用;第二轮:最终回答 + usage。
	srv.scripts = [][]string{
		{contentFrame("正在查询"), tcFrame(0, "c1", "echo", `{"text":"你好"}`), doneFrame("tool_calls")},
		{contentFrame("查询结果如下"), doneFrame("stop"), usageFrame(11, 7)},
	}

	eng := buildAgentChain(t, httpSrv.URL, "", true)
	t.Cleanup(func() { eng.Stop(context.Background()) })

	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"查一下"}]}`)

	var contents, reasoning, toolEvents []string
	var sawChunkMeta, sawToolMeta, sawDoneMeta bool
	var finalContent, promptTokens string
	for _, f := range frames {
		md := f.GetMetadata()
		switch {
		case md.GetValue("tool_call") == "true":
			sawToolMeta = true
			toolEvents = append(toolEvents, f.GetData())
		case md.GetValue("stream_completed") == "true":
			sawDoneMeta = true
		case md.GetValue("full_content") == "true":
			finalContent = f.GetData()
			promptTokens = md.GetValue("prompt_tokens")
		case md.GetValue("chunk") == "true":
			sawChunkMeta = true
			if md.GetValue("reasoning_content") == "" {
				contents = append(contents, f.GetData())
			} else {
				reasoning = append(reasoning, f.GetData())
			}
		}
	}
	if !sawChunkMeta || !sawToolMeta || !sawDoneMeta {
		t.Fatalf("帧元数据缺失: chunk=%v tool=%v done=%v", sawChunkMeta, sawToolMeta, sawDoneMeta)
	}
	if strings.Join(contents, "") != "正在查询查询结果如下" {
		t.Errorf("正文帧异常: %q", strings.Join(contents, ""))
	}
	if finalContent != "查询结果如下" {
		t.Errorf("最终全文异常: %q", finalContent)
	}
	if promptTokens != "11" {
		t.Errorf("usage 元数据异常: prompt_tokens=%q", promptTokens)
	}
	// 工具事件:START → RESULT。
	if len(toolEvents) != 2 ||
		!strings.Contains(toolEvents[0], "TOOL_CALL_START") ||
		!strings.Contains(toolEvents[0], "你好") ||
		!strings.Contains(toolEvents[1], "TOOL_CALL_RESULT") ||
		!strings.Contains(toolEvents[1], "echoed") {
		t.Errorf("工具事件异常: %v", toolEvents)
	}

	// 第二轮请求应含 system prompt、role:tool 回传与工具定义。
	srv.mu.Lock()
	last := srv.requests[len(srv.requests)-1]
	srv.mu.Unlock()
	var lastReq struct {
		Messages []Message `json:"messages"`
		Tools    []Tool    `json:"tools"`
	}
	if err := json.Unmarshal([]byte(last), &lastReq); err != nil {
		t.Fatal(err)
	}
	hasSystem, hasToolMsg := false, false
	for _, m := range lastReq.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "测试助手") {
			hasSystem = true
		}
		if m.Role == "tool" {
			hasToolMsg = true
		}
	}
	if !hasSystem || !hasToolMsg || len(lastReq.Tools) == 0 {
		t.Errorf("第二轮请求缺 system/tool 消息或工具定义: %s", last)
	}
}

// 宿主不注入 provider(UDF 缺失)时节点退化为纯对话,不应报错。
func TestAgentNodePureConversation(t *testing.T) {
	srv := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()
	srv.scripts = [][]string{
		{contentFrame("你好,我是纯对话助手"), doneFrame("stop")},
	}

	eng := buildAgentChain(t, httpSrv.URL, "", false)
	t.Cleanup(func() { eng.Stop(context.Background()) })

	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"hi"}]}`)
	var final string
	for _, f := range frames {
		if f.GetMetadata().GetValue("full_content") == "true" {
			final = f.GetData()
		}
	}
	if final != "你好,我是纯对话助手" {
		t.Errorf("纯对话最终全文异常: %q", final)
	}
	// 请求不应携带 tools。
	srv.mu.Lock()
	first := srv.requests[0]
	srv.mu.Unlock()
	if strings.Contains(first, `"tools"`) {
		t.Errorf("纯对话请求不应带 tools: %s", first)
	}
}

func TestAgentNodeSkillTool(t *testing.T) {
	skillsDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(skillsDir, "sop"), 0o755)
	_ = os.WriteFile(filepath.Join(skillsDir, "sop", "SKILL.md"),
		[]byte("---\nname: sop\ndescription: 测试技能\n---\n技能正文内容"), 0o644)

	srv := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()
	srv.scripts = [][]string{
		{tcFrame(0, "c1", "skill", `{"name":"sop"}`), doneFrame("tool_calls")},
		{contentFrame("done"), doneFrame("stop")},
	}

	eng := buildAgentChain(t, httpSrv.URL, `, "skillsDir": `+strconvQuote(skillsDir), false)
	t.Cleanup(func() { eng.Stop(context.Background()) })

	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"x"}]}`)
	var skillResult string
	for _, f := range frames {
		if f.GetMetadata().GetValue("tool_call") == "true" &&
			strings.Contains(f.GetData(), "TOOL_CALL_RESULT") {
			skillResult = f.GetData()
		}
	}
	if !strings.Contains(skillResult, "技能正文内容") {
		t.Errorf("skill 工具未取到全文: %q", skillResult)
	}
}

// 同名同参连续调用第 3 次起,BeforeCall 警告应前缀进该轮 role:tool 结果。
func TestAgentNodeDoomLoopWarning(t *testing.T) {
	srv := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()
	same := tcFrame(0, "", "echo", `{"text":"x"}`)
	srv.scripts = [][]string{
		{same, doneFrame("tool_calls")},
		{same, doneFrame("tool_calls")},
		{same, doneFrame("tool_calls")},
		{contentFrame("done"), doneFrame("stop")},
	}

	eng := buildAgentChain(t, httpSrv.URL, "", true)
	t.Cleanup(func() { eng.Stop(context.Background()) })

	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"x"}]}`)
	_ = frames

	// 第 3 次 echo(count=3 ≥ MILD 阈值)的警告出现在第 4 个请求的 role:tool 消息里。
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.requests) < 4 {
		t.Fatalf("应发生 4 轮请求, got %d", len(srv.requests))
	}
	if !strings.Contains(srv.requests[3], "已用相同参数调用") {
		t.Errorf("第 3 次重复调用的警告未进入 tool 结果: %s", srv.requests[3])
	}
	if strings.Contains(srv.requests[2], "已用相同参数调用") {
		t.Errorf("第 2 次调用不应告警: %s", srv.requests[2])
	}
}

// 配置 images:预置图片以 image_url parts 追加到末条 user 消息。
func TestAgentNodePresetImages(t *testing.T) {
	srv := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()
	srv.scripts = [][]string{
		{contentFrame("图里有猫"), doneFrame("stop")},
	}

	eng := buildAgentChain(t, httpSrv.URL, `, "images": ["https://e.com/cat.png"]`, false)
	t.Cleanup(func() { eng.Stop(context.Background()) })

	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"这是什么"}]}`)
	_ = frames

	srv.mu.Lock()
	first := srv.requests[0]
	srv.mu.Unlock()
	if !strings.Contains(first, `"type":"image_url"`) || !strings.Contains(first, "cat.png") {
		t.Errorf("预置图片未追加到末条 user 消息: %s", first)
	}
	if !strings.Contains(first, `"这是什么"`) {
		t.Errorf("原文本应保留为 text part: %s", first)
	}
}

// 入参 content 数组(带内多模态)应原样到达服务端。
func TestAgentNodeInlineImageParts(t *testing.T) {
	srv := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()
	srv.scripts = [][]string{
		{contentFrame("收到图"), doneFrame("stop")},
	}

	eng := buildAgentChain(t, httpSrv.URL, "", false)
	t.Cleanup(func() { eng.Stop(context.Background()) })

	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":[`+
		`{"type":"text","text":"看这张"},`+
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}]}]}`)
	_ = frames

	srv.mu.Lock()
	first := srv.requests[0]
	srv.mu.Unlock()
	if !strings.Contains(first, "data:image/png;base64,abc") || !strings.Contains(first, "看这张") {
		t.Errorf("入参 parts 数组未原样透传: %s", first)
	}
}

func TestAgentNodeNoModelConfigured(t *testing.T) {
	// model 为空:直接失败且错误信息可读。
	def := fmt.Sprintf(`{
		"ruleChain": {"id": "t_agent_lite2", "name": "t", "root": true},
		"metadata": {
			"nodes": [
				{"id": "n1", "type": %q, "name": "agent",
				 "configuration": {"url": "http://127.0.0.1:1", "model": "", "systemPrompt": "x"}},
				{"id": "n2", "type": "end", "name": "end", "configuration": {}}
			],
			"connections": [{"fromId": "n1", "toId": "n2", "type": "Success"}]
		}
	}`, NodeType)
	rc := rulego.NewConfig()
	pool := rulego.NewRuleGo()
	eng, err := pool.New("t_agent_lite2", []byte(def), types.WithConfig(rc))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Stop(context.Background()) })
	msg := types.NewMsg(0, "chat.completions", types.JSON, types.NewMetadata(), `{"messages":[]}`)
	done := make(chan error, 1)
	eng.OnMsg(msg, types.WithOnEnd(func(_ types.RuleContext, _ types.RuleMsg, err error, _ string) {
		done <- err
	}))
	if err := <-done; err == nil || !strings.Contains(err.Error(), "模型") {
		t.Errorf("期望模型未配置错误: %v", err)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestPutUsageAccumulates 多轮 ReAct 每轮 putUsage 对同一 metadata 累加而非覆盖。
func TestPutUsageAccumulates(t *testing.T) {
	n := &AgentLiteNode{}
	msg := types.NewMsg(0, "chat.completions", types.JSON, types.NewMetadata(), `{}`)
	// 预置第一轮写入的旧值。
	msg.Metadata.PutValue(config.KeyPromptTokens, "10")
	msg.Metadata.PutValue(config.KeyCompletionTokens, "5")
	msg.Metadata.PutValue(config.KeyTotalTokens, "15")

	n.putUsage(msg, &Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}, "mock-model")

	if got := msg.Metadata.GetValue(config.KeyPromptTokens); got != "15" {
		t.Fatalf("prompt tokens = %s, want 15", got)
	}
	if got := msg.Metadata.GetValue(config.KeyCompletionTokens); got != "8" {
		t.Fatalf("completion tokens = %s, want 8", got)
	}
	if got := msg.Metadata.GetValue(config.KeyTotalTokens); got != "23" {
		t.Fatalf("total tokens = %s, want 23", got)
	}
	if got := msg.Metadata.GetValue("model"); got != "mock-model" {
		t.Fatalf("model = %s (仍为覆盖语义)", got)
	}

	// 旧值非数字按 0 处理,不 panic。
	msg.Metadata.PutValue(config.KeyPromptTokens, "garbage")
	n.putUsage(msg, &Usage{PromptTokens: 7, CompletionTokens: 0, TotalTokens: 7}, "")
	if got := msg.Metadata.GetValue(config.KeyPromptTokens); got != "7" {
		t.Fatalf("prompt tokens with garbage old = %s, want 7", got)
	}
}
