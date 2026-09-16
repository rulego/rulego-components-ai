package lite

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
)

// fixedProvider 返回固定文本的工具提供者(截断测试用)。
type fixedProvider struct{ out string }

func (fixedProvider) ListToolDefinitions() ([]types.MCPToolDefinition, error) { return nil, nil }
func (p fixedProvider) CallTool(_ context.Context, _ string, _ map[string]interface{}) (string, error) {
	return p.out, nil
}

// maxStep 缺省 50,与 eino 版 agent.DefaultMaxStep 一致。
func TestDefaultMaxStep(t *testing.T) {
	var n AgentLiteNode
	if err := n.Init(rulego.NewConfig(), types.Configuration{"url": "http://x", "model": "m"}); err != nil {
		t.Fatal(err)
	}
	if n.maxStep != 50 {
		t.Fatalf("maxStep 缺省应为 50(对齐 eino 版),得到 %d", n.maxStep)
	}
}

// params 缺省时套用与 eino 版一致的采样默认(temperature 0.7 / topP 0.9)。
func TestDefaultSamplingParams(t *testing.T) {
	srv := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()
	srv.scripts = [][]string{{contentFrame("ok"), doneFrame("stop")}}

	eng := buildAgentChain(t, httpSrv.URL, "", false) // 配置不带 params
	t.Cleanup(func() { eng.Stop(context.Background()) })
	_ = runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"hi"}]}`)

	srv.mu.Lock()
	first := srv.requests[0]
	srv.mu.Unlock()
	if !strings.Contains(first, `"temperature":0.7`) || !strings.Contains(first, `"top_p":0.9`) {
		t.Fatalf("缺省采样参数应套 eino 版默认值: %s", first)
	}
}

// tools 为 eino 版描述符对象数组时,Init 给出指向兼容矩阵的可读错误
// (替代 mapstructure 的类型转换报错)。
func TestToolsObjectShapeRejected(t *testing.T) {
	var n AgentLiteNode
	err := n.Init(rulego.NewConfig(), types.Configuration{
		"model": "m",
		"tools": []interface{}{map[string]interface{}{"type": "builtin", "name": "bash"}},
	})
	if err == nil || !strings.Contains(err.Error(), "描述符") {
		t.Fatalf("对象形状 tools 应报可读错误: %v", err)
	}
}

// 工具输出按 rune 截断(不劈开多字节字符),后缀格式与 eino 版 truncateResult 一致。
func TestToolOutputRuneTruncation(t *testing.T) {
	long := strings.Repeat("温", 10) // 10 runes / 30 bytes
	n := &AgentLiteNode{provider: fixedProvider{out: long}, maxToolOut: 4}
	out, failed := n.runTool(nil, "echo", `{}`)
	if failed {
		t.Fatal("不应失败")
	}
	want := strings.Repeat("温", 4) + fmt.Sprintf("...(truncated, original: %d bytes)", len(long))
	if out != want {
		t.Fatalf("截断结果不符:\n got=%q\nwant=%q", out, want)
	}
}

// images 支持本地文件路径(转 data: base64 URL),输入口径与 eino 版一致。
func TestImagesLocalFile(t *testing.T) {
	png := filepath.Join(t.TempDir(), "img.png")
	// PNG 魔数开头,http.DetectContentType 识别为 image/png。
	if err := os.WriteFile(png, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := &sseLLM{}
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()
	srv.scripts = [][]string{{contentFrame("ok"), doneFrame("stop")}}

	eng := buildAgentChain(t, httpSrv.URL, `, "images": [`+strconvQuote(png)+`]`, false)
	t.Cleanup(func() { eng.Stop(context.Background()) })
	_ = runStreamMsg(t, eng, `{"messages":[{"role":"user","content":"这是什么"}]}`)

	srv.mu.Lock()
	first := srv.requests[0]
	srv.mu.Unlock()
	if !strings.Contains(first, `"data:image/png;base64,`) {
		t.Fatalf("本地图片应转 data: URL: %s", first)
	}
}

// circuitCooldownSec 可配:自定义基础冷却到期后主端点恢复重试。
func TestBreakerConfigurableCooldown(t *testing.T) {
	primary := &notFoundLLM{}
	pSrv := httptest.NewServer(http.HandlerFunc(primary.handler))
	defer pSrv.Close()
	backup := &jsonLLM{}
	bSrv := httptest.NewServer(http.HandlerFunc(backup.handler))
	defer bSrv.Close()

	cur := time.Now()
	r := newChatRouter(breakerCooldown(1)) // 1s 基础冷却
	r.setNow(func() time.Time { return cur })
	eps := []chatEndpoint{{client: New(pSrv.URL, "")}, {client: New(bSrv.URL, "")}}
	req := Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}

	if _, err := r.Complete(context.Background(), eps, req); err != nil {
		t.Fatal(err)
	}
	hits := primary.hitCount()

	cur = cur.Add(500 * time.Millisecond) // 冷却期内:跳过主端点
	if _, err := r.Complete(context.Background(), eps, req); err != nil {
		t.Fatal(err)
	}
	if got := primary.hitCount(); got != hits {
		t.Fatalf("冷却期内不应请求主端点: %d → %d", hits, got)
	}

	cur = cur.Add(600 * time.Millisecond) // 1.1s > 1s 冷却:恢复重试
	if _, err := r.Complete(context.Background(), eps, req); err != nil {
		t.Fatal(err)
	}
	if got := primary.hitCount(); got != hits+1 {
		t.Fatalf("冷却到期应重试主端点: %d → %d", hits, got)
	}
}

// min(内置)与 breakerCooldown 缺省值冒烟:0=60s,正数=秒数。
func TestBreakerCooldownConfig(t *testing.T) {
	if got := breakerCooldown(0); got != 60*time.Second {
		t.Fatalf("缺省基础冷却应为 60s(对齐 eino 版): %v", got)
	}
	if got := breakerCooldown(5); got != 5*time.Second {
		t.Fatalf("自定义冷却应为 5s: %v", got)
	}
}
