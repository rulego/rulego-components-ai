// Package browseruse provides a browser automation tool for AI agents.
//
// # 使用示例
//
// 导航到网页:
//
//	{"action": "go_to_url", "url": "https://example.com"}
//
// 点击元素:
//
//	{"action": "click_element", "index": 0}
//
// 输入文本:
//
//	{"action": "input_text", "index": 0, "text": "hello"}
//
// 滚动页面:
//
//	{"action": "scroll_down", "scroll_amount": 500}
//
// 提取内容:
//
//	{"action": "extract_content", "goal": "获取页面主要内容"}
//
// 打开新标签页:
//
//	{"action": "open_tab", "url": "https://example.com"}
//
// 等待:
//
//	{"action": "wait", "seconds": 3}
package browseruse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cloudwego/eino-ext/components/tool/duckduckgo/v2"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	aitool "github.com/rulego/rulego-components-ai/tool"
	"github.com/rulego/rulego/utils/maps"
)

const (
	// ToolName is the name of the browser use tool.
	ToolName = "browser_use"
)

// Config holds browser use tool configuration.
type Config struct {
	// Headless runs browser in headless mode (no UI)
	Headless bool `json:"headless" label:"无头模式" desc:"是否使用无头浏览器模式（不显示界面）"`

	// DisableSecurity disable web security
	DisableSecurity bool `json:"disableSecurity" label:"禁用安全" desc:"是否禁用浏览器安全策略"`

	// ExtraChromiumArgs extra chromium command line arguments (boolean flags only)
	ExtraChromiumArgs []string `json:"extraChromiumArgs" label:"额外参数" desc:"额外的 Chromium 布尔命令行参数，如 disable-gpu, no-sandbox"`

	// ChromiumFlags chromium command line flags with values
	// 例如: {"lang": "zh-CN", "disable-blink-features": "AutomationControlled"}
	ChromiumFlags map[string]any `json:"chromiumFlags" label:"Chromium参数" desc:"Chromium 命令行参数（支持带值），如 lang, disable-blink-features 等"`

	// ChromeInstancePath path to Chrome/Chromium executable
	ChromeInstancePath string `json:"chromeInstancePath" label:"Chrome路径" desc:"Chrome/Chromium 可执行文件路径（为空则自动查找）"`

	// ProxyServer HTTP proxy address
	ProxyServer string `json:"proxyServer" label:"代理地址" desc:"HTTP 代理地址，如 http://127.0.0.1:7890"`

	// UserDataDir Chrome user data directory for persistent sessions
	// 支持相对路径和绝对路径，相对路径会基于当前工作目录解析
	// 设置此目录可以保留登录状态、cookies、localStorage 等数据
	UserDataDir string `json:"userDataDir" label:"用户数据目录" desc:"Chrome 用户数据目录，用于保留登录状态和浏览数据（支持相对路径）"`

	// SearchEngine 默认搜索引擎，可选值: google, baidu, bing, duckduckgo
	// 也可以直接填写 URL 模板，例如: "https://www.sogou.com/web?query=%s"
	// 默认为 baidu
	SearchEngine string `json:"searchEngine" label:"搜索引擎" desc:"默认搜索引擎 (google, baidu, bing, duckduckgo) 或自定义 URL 模板"`

	// Timeout 操作超时时间（秒），用于页面加载、元素等待等操作
	// 默认为 30 秒
	Timeout int `json:"timeout" label:"超时时间" desc:"操作超时时间（秒），用于页面加载、元素等待等操作，默认 30 秒"`

	// DDGSearchTool DuckDuckGo search tool for web_search action
	DDGSearchTool duckduckgo.Search `json:"-"`

	// ExtractChatModel chat model for extract_content action
	ExtractChatModel model.BaseChatModel `json:"-"`

	// Logf custom log function
	Logf func(string, ...any) `json:"-"`
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		Headless: true,
		Timeout:  30,
	}
}

// browserUseTool wraps the browseruse tool.
type browserUseTool struct {
	config Config
	tool   *Tool
	logf   func(string, ...any)
	mu     sync.Mutex
}

func (t *browserUseTool) log(format string, args ...any) {
	if t.logf != nil {
		t.logf(format, args...)
	}
}

// NewTool creates a new browser use tool.
func NewTool(config Config) (tool.BaseTool, error) {
	return NewToolWithContext(context.Background(), config)
}

// NewToolWithContext creates a new browser use tool with a custom context.
func NewToolWithContext(ctx context.Context, config Config) (tool.BaseTool, error) {
	logf := config.Logf
	if logf == nil {
		logf = func(format string, args ...any) {}
	}

	// 解析 UserDataDir 为绝对路径
	userDataDir := config.UserDataDir
	if userDataDir != "" {
		// 如果是相对路径，转换为绝对路径
		if !filepath.IsAbs(userDataDir) {
			absPath, err := filepath.Abs(userDataDir)
			if err != nil {
				return nil, fmt.Errorf("解析用户数据目录路径失败: %w", err)
			}
			userDataDir = absPath
		}
		// 确保目录存在
		if err := os.MkdirAll(userDataDir, 0755); err != nil {
			return nil, fmt.Errorf("创建用户数据目录失败: %w", err)
		}
		logf("[browseruse] UserDataDir 解析为: %s", userDataDir)
	}

	localConfig := &Config{
		Headless:           config.Headless,
		DisableSecurity:    config.DisableSecurity,
		ExtraChromiumArgs:  config.ExtraChromiumArgs,
		ChromeInstancePath: config.ChromeInstancePath,
		ProxyServer:        config.ProxyServer,
		UserDataDir:        userDataDir,
		ChromiumFlags:      config.ChromiumFlags,
		SearchEngine:       config.SearchEngine,
		DDGSearchTool:      config.DDGSearchTool,
		ExtractChatModel:   config.ExtractChatModel,
		Logf:               logf,
	}

	// 保存解析后的路径
	config.UserDataDir = userDataDir

	t, err := NewBrowserUseTool(ctx, localConfig)
	if err != nil {
		return nil, fmt.Errorf("创建浏览器工具失败: %w", err)
	}

	return &browserUseTool{
		config: config,
		tool:   t,
		logf:   logf,
	}, nil
}

// Info returns tool information.
func (t *browserUseTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.tool.Info(ctx)
}

// InvokableRun executes the operation.
func (t *browserUseTool) InvokableRun(ctx context.Context, arguments string, opts ...tool.Option) (res string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered in browser_use wrapper: %v", r)
			// 尝试清理资源
			t.Cleanup()
		}
	}()

	if ctx.Err() != nil {
		return "", fmt.Errorf("context 已取消: %w", ctx.Err())
	}

	result, err := t.tool.InvokableRun(ctx, arguments, opts...)
	if err != nil {
		t.reinitializeIfNeeded()
		return "", err
	}

	return result, nil
}

// reinitializeIfNeeded 检查并重新初始化浏览器
func (t *browserUseTool) reinitializeIfNeeded() error {
	_, err := t.tool.GetCurrentState()
	if err == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	_, err = t.tool.GetCurrentState()
	if err == nil {
		return nil
	}

	t.log("[browseruse] 重新初始化浏览器...")
	t.tool.Cleanup()

	newTool, err := NewBrowserUseTool(context.Background(), &Config{
		Headless:           t.config.Headless,
		DisableSecurity:    t.config.DisableSecurity,
		ExtraChromiumArgs:  t.config.ExtraChromiumArgs,
		ChromeInstancePath: t.config.ChromeInstancePath,
		ProxyServer:        t.config.ProxyServer,
		UserDataDir:        t.config.UserDataDir,
		ChromiumFlags:      t.config.ChromiumFlags,
		DDGSearchTool:      t.config.DDGSearchTool,
		ExtractChatModel:   t.config.ExtractChatModel,
		Logf:               t.logf,
	})
	if err != nil {
		return fmt.Errorf("重新初始化浏览器失败: %w", err)
	}

	t.tool = newTool
	return nil
}

// Cleanup cleans up browser resources.
func (t *browserUseTool) Cleanup() {
	t.tool.Cleanup()
}

// GetCurrentState returns the current browser state.
func (t *browserUseTool) GetCurrentState() (*BrowserState, error) {
	return t.tool.GetCurrentState()
}

// Execute executes a browser action.
func (t *browserUseTool) Execute(params *Param) (*ToolResult, error) {
	return t.tool.Execute(params)
}

// Register registers the browser use tool with custom configuration.
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault registers with default configuration.
func RegisterDefault() error {
	def := aitool.ToolDefinition{
		Name:   ToolName,
		Desc:   "浏览器自动化工具 - 使用 Chrome/Chromium 进行网页导航、元素交互、内容提取和标签页管理。注意：此工具会启动真实浏览器，仅用于网页操作。图片识别/分析请直接使用大模型的多模态能力，不要调用此工具。支持操作: go_to_url, click_element, input_text, scroll_down/up, wait, extract_content, open_tab, switch_tab, close_tab",
		Config: Config{},
		Factory: func(config map[string]interface{}) (tool.BaseTool, error) {
			var cfg Config
			if err := maps.Map2Struct(config, &cfg); err != nil {
				return nil, err
			}
			if cfg.Logf == nil {
				cfg.Logf = func(format string, args ...any) {}
			}
			return NewTool(cfg)
		},
	}

	return aitool.Registry.RegisterDef(def)
}

// ParseAction parses action from JSON string.
func ParseAction(actionJSON string) (Action, error) {
	var action Action
	if err := json.Unmarshal([]byte(fmt.Sprintf(`"%s"`, actionJSON)), &action); err != nil {
		return "", fmt.Errorf("解析操作失败: %w", err)
	}
	return action, nil
}

func init() {
	_ = RegisterDefault()
}
