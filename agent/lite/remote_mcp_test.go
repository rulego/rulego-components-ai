package lite

// remote_mcp_test.go 远程 MCP 接入的节点级验证:
//   - streamable http fixture 上工具表暴露与调用路由端到端走通;
//   - 允许列表同样约束远程工具;
//   - server 不可达时链 Init 不失败、消息降级纯对话(懒连接语义);
//   - 主 provider 与远程工具重名时定义只暴露一次且调用走主 provider。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rulego/rulego/api/types"
)

// startRemoteMCP 起 streamable http 的 MCP server fixture,注册 name 工具
// (回显 city 参数)。
func startRemoteMCP(t *testing.T, name string) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("test-mcp", "1.0.0")
	s.AddTool(
		mcp.NewTool(name,
			mcp.WithDescription("查询城市天气"),
			mcp.WithString("city", mcp.Required()),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			city, _ := req.GetArguments()["city"].(string)
			return mcp.NewToolResultText(fmt.Sprintf("%s 晴 25 度", city)), nil
		},
	)
	httpSrv := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

// remoteCfg 生成声明一个远程 server 的节点配置。
func remoteCfg(llmURL, mcpURL, toolsFilter string) string {
	return `{"url":"` + llmURL + `","key":"k","model":"m","maxStep":5,` +
		`"tools":[{"type":"mcp","config":{"server":"` + mcpURL + `"` + toolsFilter + `}}]}`
}

// 远程工具表暴露给模型,调用路由到远程并执行,结果回传 role:tool。
func TestRemoteMCPHTTPToolFlow(t *testing.T) {
	mcpSrv := startRemoteMCP(t, "get_weather")

	llm := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(llm.handler))
	defer httpSrv.Close()
	llm.scripts = [][]string{
		{tcFrame(0, "c1", "get_weather", `{"city":"北京"}`), doneFrame("tool_calls")},
		{contentFrame("北京晴 25 度"), doneFrame("stop")},
	}

	eng := buildLiteChain(t, httpSrv.URL, remoteCfg(httpSrv.URL, mcpSrv.URL, ""), nil)
	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"北京天气"}]}`)
	if !hasFullContent(frames) {
		t.Fatalf("链未正常收尾: %d 帧", len(frames))
	}

	llm.mu.Lock()
	first, second := llm.requests[0], llm.requests[1]
	llm.mu.Unlock()
	if got := llmToolNames(t, first); len(got) != 1 || got[0] != "get_weather" {
		t.Fatalf("远程工具应暴露给模型: %v", got)
	}
	if tr := llmToolResult(second); !strings.Contains(tr, "北京 晴 25 度") {
		t.Fatalf("role:tool 未收到远程执行结果: %q", tr)
	}
}

// config.tools 过滤列表同样约束远程工具:列表外的工具不暴露。
func TestRemoteMCPAllowlist(t *testing.T) {
	mcpSrv := startRemoteMCP(t, "get_weather")

	llm := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(llm.handler))
	defer httpSrv.Close()
	llm.scripts = [][]string{{contentFrame("好的"), doneFrame("stop")}}

	filter := `,"tools":["not_registered"]`
	eng := buildLiteChain(t, httpSrv.URL, remoteCfg(httpSrv.URL, mcpSrv.URL, filter), nil)
	runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"hi"}]}`)

	llm.mu.Lock()
	first := llm.requests[0]
	llm.mu.Unlock()
	if got := llmToolNames(t, first); len(got) != 0 {
		t.Fatalf("列表外的远程工具不应暴露: %v", got)
	}
}

// server 不可达:链 Init 不失败,消息降级纯对话,不炸链。
func TestRemoteMCPUnreachableDegrades(t *testing.T) {
	llm := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(llm.handler))
	defer httpSrv.Close()
	llm.scripts = [][]string{{contentFrame("好的"), doneFrame("stop")}}

	eng := buildLiteChain(t, httpSrv.URL, remoteCfg(httpSrv.URL, "http://127.0.0.1:1", ""), nil)
	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"hi"}]}`)
	if !hasFullContent(frames) {
		t.Fatalf("不可达 server 应降级纯对话: %d 帧", len(frames))
	}

	llm.mu.Lock()
	first := llm.requests[0]
	llm.mu.Unlock()
	if got := llmToolNames(t, first); len(got) != 0 {
		t.Fatalf("不可达 server 的工具不应暴露: %v", got)
	}
}

// 主 provider 与远程工具重名:定义只暴露一次,调用走主 provider(合并顺序即路由顺序)。
func TestRemoteAndProviderNameClash(t *testing.T) {
	mcpSrv := startRemoteMCP(t, "list_devices")

	llm := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(llm.handler))
	defer httpSrv.Close()
	llm.scripts = [][]string{
		{tcFrame(0, "c1", "list_devices", `{"value":1}`), doneFrame("tool_calls")},
		{contentFrame("完成"), doneFrame("stop")},
	}

	prov := &multiToolProvider{}
	cfg := `{"url":"` + httpSrv.URL + `","key":"k","model":"m","maxStep":5,` +
		`"tools":["list_devices",{"type":"mcp","config":{"server":"` + mcpSrv.URL + `"}}]}`
	eng := buildLiteChain(t, httpSrv.URL, cfg, prov)
	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"列设备"}]}`)
	if !hasFullContent(frames) {
		t.Fatalf("链未正常收尾: %d 帧", len(frames))
	}

	llm.mu.Lock()
	first := llm.requests[0]
	llm.mu.Unlock()
	if got := llmToolNames(t, first); len(got) != 1 || got[0] != "list_devices" {
		t.Fatalf("重名工具应只暴露一次: %v", got)
	}
	if tr := llmToolResult(llm.requests[1]); !strings.Contains(tr, `{"devices":["meter-1"]}`) {
		t.Fatalf("重名调用应走主 provider: %q", tr)
	}
}

// hasFullContent 帧序列里是否有 full_content 收尾帧。
func hasFullContent(frames []types.RuleMsg) bool {
	for _, f := range frames {
		if f.GetMetadata().GetValue("full_content") == "true" {
			return true
		}
	}
	return false
}

// config.tools 含 "*":远程工具全量暴露。
func TestRemoteMCPWildcard(t *testing.T) {
	mcpSrv := startRemoteMCP(t, "get_weather")

	llm := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(llm.handler))
	defer httpSrv.Close()
	llm.scripts = [][]string{{contentFrame("好的"), doneFrame("stop")}}

	filter := `,"tools":["*"]`
	eng := buildLiteChain(t, httpSrv.URL, remoteCfg(httpSrv.URL, mcpSrv.URL, filter), nil)
	runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"hi"}]}`)

	llm.mu.Lock()
	first := llm.requests[0]
	llm.mu.Unlock()
	if got := llmToolNames(t, first); len(got) != 1 || got[0] != "get_weather" {
		t.Fatalf("含 * 的过滤列表应全量暴露远程工具: %v", got)
	}
}

// server 挂起(连接建立后不响应):拉表超时后降级纯对话,不无限阻塞。
func TestRemoteMCPHangTimesOut(t *testing.T) {
	old := remoteMCPListTimeout
	remoteMCPListTimeout = 300 * time.Millisecond
	t.Cleanup(func() { remoteMCPListTimeout = old })

	// handler 挂到测试收尾才醒:mcp-go 超时取消后 keep-alive 连接不断开,
	// server 端 r.Context() 不会 Done,不主动放行的话 hang.Close() 会一直
	// 等这个请求结束。
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer hang.Close()
	defer close(release)

	llm := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(llm.handler))
	defer httpSrv.Close()
	llm.scripts = [][]string{{contentFrame("好的"), doneFrame("stop")}}

	start := time.Now()
	eng := buildLiteChain(t, httpSrv.URL, remoteCfg(httpSrv.URL, hang.URL, ""), nil)
	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"hi"}]}`)
	if !hasFullContent(frames) {
		t.Fatalf("挂起 server 应超时降级纯对话: %d 帧", len(frames))
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("挂起场景耗时 %v,疑似未生效超时", elapsed)
	}

	llm.mu.Lock()
	first := llm.requests[0]
	llm.mu.Unlock()
	if got := llmToolNames(t, first); len(got) != 0 {
		t.Fatalf("挂起 server 的工具不应暴露: %v", got)
	}
}

// 两个远程 server 并存:工具表合并暴露,调用按名路由到各自 server。
func TestRemoteMCPMultipleServers(t *testing.T) {
	srvA := startRemoteMCP(t, "get_weather")
	srvB := startRemoteMCP(t, "get_air")

	llm := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(llm.handler))
	defer httpSrv.Close()
	llm.scripts = [][]string{
		{tcFrame(0, "c1", "get_air", `{"city":"北京"}`), doneFrame("tool_calls")},
		{contentFrame("完成"), doneFrame("stop")},
	}

	cfg := `{"url":"` + httpSrv.URL + `","key":"k","model":"m","maxStep":5,` +
		`"tools":[{"type":"mcp","config":{"server":"` + srvA.URL + `"}},` +
		`{"type":"mcp","config":{"server":"` + srvB.URL + `"}}]}`
	eng := buildLiteChain(t, httpSrv.URL, cfg, nil)
	frames := runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"北京空气"}]}`)
	if !hasFullContent(frames) {
		t.Fatalf("链未正常收尾: %d 帧", len(frames))
	}

	llm.mu.Lock()
	first, second := llm.requests[0], llm.requests[1]
	llm.mu.Unlock()
	names := llmToolNames(t, first)
	if len(names) != 2 {
		t.Fatalf("双 server 工具应合并暴露: %v", names)
	}
	// get_air 只有 server B 有,结果应来自 B 的回执。
	if tr := llmToolResult(second); !strings.Contains(tr, "北京 晴 25 度") {
		t.Fatalf("调用应路由到声明该工具的 server: %q", tr)
	}
}

// parseServerCommand:空白拆分、成对引号、尾部空白、空串。
func TestParseServerCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a b c", []string{"a", "b", "c"}},
		{"  a   b  ", []string{"a", "b"}},
		{`run --path "C:\x y"`, []string{"run", "--path", `C:\x y`}},
		{`run --name 'lite agent'`, []string{"run", "--name", "lite agent"}},
	}
	for _, c := range cases {
		got := parseServerCommand(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("parseServerCommand(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("parseServerCommand(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}
