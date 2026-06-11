/*
 * Copyright 2025 CloudWeGo Authors
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

package browseruse

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"

	"github.com/cloudwego/eino-ext/components/tool/duckduckgo/v2"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const (
	toolName        = "browser_use"
	toolDescription = `
A browser automation tool that controls Chrome/Chromium to perform web interactions.
IMPORTANT: This tool launches a real browser. Use it ONLY for:
1. Navigating to websites and interacting with web pages (clicking, typing, scrolling)
2. Extracting content from web pages
3. Web searching and browsing
4. Managing browser tabs

DO NOT use this tool for:
- Image recognition or analysis -> Use your built-in vision/multimodal capabilities directly instead
- Analyzing screenshots or images -> Use your built-in vision/multimodal capabilities directly instead
- Simple URL fetching -> Use appropriate HTTP tools instead

Supported actions:
Navigation:
- 'go_to_url': Go to a specific URL in the current tab
- 'web_search': Search the query in the current tab. If a search tool is configured, it uses the search tool; otherwise, it navigates to a search engine (Google, Baidu, Bing, DuckDuckGo) directly.

Element Interaction:
- 'click_element': Click an element by index
- 'input_text': Input text into a form element
- 'scroll_down'/'scroll_up': Scroll the page (with optional pixel amount)
Content Extraction:
- 'extract_content': Extract page content to retrieve specific information from the page, e.g.all company names, a specific description, links with companies in structured format or simply links
Tab Management:
- 'switch_tab': Switch to a specific tab
- 'open_tab': Open a new tab with a URL
- 'close_tab': Close the current tab
Utility:
- 'wait': Wait for a specified number of seconds
`

	extractContentPrompt = `
Your task is to extract the content of the page. You will be given a page and a goal, and you should extract all relevant information around this goal from the page. If the goal is vague, summarize the page. Respond in json format.
Extraction goal: {goal}

Page content:
{page}
`
)

type ToolResult struct {
	Output      string `json:"output,omitempty"`
	Error       string `json:"error,omitempty"`
	Base64Image string `json:"base64_image,omitempty"`
}

type BrowserState struct {
	URL                 string     `json:"url"`
	Title               string     `json:"title"`
	Tabs                []TabInfo  `json:"tabs"`
	InteractiveElements string     `json:"interactive_elements"`
	ScrollInfo          ScrollInfo `json:"scroll_info"`
	ViewportHeight      int        `json:"viewport_height"`
	Screenshot          string     `json:"screenshot"`
}

type TabInfo struct {
	ID       int       `json:"id"`
	TargetID target.ID `json:"target_id"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
}

type ScrollInfo struct {
	PixelsAbove int `json:"pixels_above"`
	PixelsBelow int `json:"pixels_below"`
	TotalHeight int `json:"total_height"`
}

type ElementInfo struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	Type        string `json:"type"`
	XPath       string `json:"xpath"`
}

type Tool struct {
	info *schema.ToolInfo

	mu              sync.Mutex
	ctx             context.Context
	allocatorCtx    context.Context
	allocatorCancel context.CancelFunc
	elements        []ElementInfo
	currentTabID    int
	tabs            []TabInfo
	searchTool      duckduckgo.Search
	searchEngine    string
	cm              model.BaseChatModel
	tpl             prompt.ChatTemplate

	// timeout 操作超时时间（秒）
	timeout int
	// headless 是否使用无头模式
	headless bool

	// 延迟初始化
	pendingConfig *Config
	initialized   bool

	// cleanupOnce 确保 allocatorCancel 只被调用一次，避免 close of closed channel panic
	// 使用指针类型以便在重新初始化时可以重置
	cleanupOnce *sync.Once
}

func (b *Tool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return b.info, nil
}

func (b *Tool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (res string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered in browser_use tool: %v", r)
		}
	}()

	// 延迟初始化：第一次调用时才启动浏览器
	if err := b.ensureInitialized(ctx); err != nil {
		return "", fmt.Errorf("failed to initialize browser: %w", err)
	}

	param := &Param{}
	err = sonic.UnmarshalString(argumentsInJSON, param)
	result, err := b.Execute(param)
	if err != nil {
		return "", err
	}
	content, err := sonic.MarshalString(result)
	if err != nil {
		return "", err
	}
	return content, nil
}

// ensureInitialized 确保浏览器已初始化（延迟初始化）
func (b *Tool) ensureInitialized(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.initialized {
		// 检查 context 是否已取消
		if b.ctx != nil && b.ctx.Err() == nil {
			return nil
		}
		// 如果 context 已取消，安全地清理并重新初始化
		b.safeCleanup()
	}

	if b.pendingConfig == nil {
		return fmt.Errorf("browser config not set")
	}

	if err := b.initialize(ctx, b.pendingConfig); err != nil {
		return err
	}

	b.initialized = true
	return nil
}

// safeCleanup 安全地清理浏览器资源，避免 close of closed channel panic
func (b *Tool) safeCleanup() {
	if b.allocatorCancel != nil {
		// 使用 recover 捕获可能的 "close of closed channel" panic
		defer func() {
			if r := recover(); r != nil {
				// 忽略 close of closed channel panic，这是 chromedp 内部的问题
			}
		}()
		b.allocatorCancel()
		b.allocatorCancel = nil
	}

	b.ctx = nil
	b.allocatorCtx = nil
	b.elements = nil
	b.tabs = nil
	b.initialized = false

	// 重置 cleanupOnce，以便下次可以使用
	b.cleanupOnce = &sync.Once{}
}

func NewBrowserUseTool(ctx context.Context, config *Config) (*Tool, error) {
	if config == nil {
		config = &Config{}
	}
	actions := []any{
		string(ActionGoToURL),
		string(ActionClickElement),
		string(ActionInputText),
		string(ActionScrollDown),
		string(ActionScrollUp),
		//string(ActionSendKeys),
		string(ActionWait),
		string(ActionExtractContent),
		string(ActionSwitchTab),
		string(ActionOpenTab),
		string(ActionCloseTab),
		string(ActionSetTimeout),
		string(ActionSetSearchEngine),
		string(ActionSetHeadless),
	}

	if config.DDGSearchTool != nil {
		actions = append(actions, string(ActionWebSearch))
	} else {
		// 如果没有搜索工具，也启用 web_search，但使用默认的 Baidu 搜索
		actions = append(actions, string(ActionWebSearch))
	}

	// 获取超时配置，默认 30 秒
	timeout := 30
	if config.Timeout > 0 {
		timeout = config.Timeout
	}

	but := &Tool{
		info: &schema.ToolInfo{
			Name: toolName,
			Desc: toolDescription,
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(
				&jsonschema.Schema{
					Type: string(schema.Object),
					Properties: orderedmap.New[string, *jsonschema.Schema](
						orderedmap.WithInitialData[string, *jsonschema.Schema](
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "action",
								Value: &jsonschema.Schema{
									Type:        string(schema.String),
									Enum:        actions,
									Description: "The browser action to perform",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "url",
								Value: &jsonschema.Schema{
									Type:        string(schema.String),
									Description: "URL for 'go_to_url' or 'open_tab' actions",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "index",
								Value: &jsonschema.Schema{
									Type:        string(schema.Integer),
									Description: "Element index for 'click_element', 'input_text' actions",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "text",
								Value: &jsonschema.Schema{
									Type:        string(schema.String),
									Description: "Text for 'input_text' actions",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "scroll_amount",
								Value: &jsonschema.Schema{
									Type:        string(schema.Integer),
									Description: "Pixels to scroll (positive for down, negative for up) for 'scroll_down' or 'scroll_up' actions",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "tab_id",
								Value: &jsonschema.Schema{
									Type:        string(schema.Integer),
									Description: "Tab ID for 'switch_tab' action",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "query",
								Value: &jsonschema.Schema{
									Type:        string(schema.String),
									Description: "Search query for 'web_search' action",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "goal",
								Value: &jsonschema.Schema{
									Type:        string(schema.String),
									Description: "Extraction goal for 'extract_content' action",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "keys",
								Value: &jsonschema.Schema{
									Type:        string(schema.String),
									Description: "Keys to send for 'send_keys' action",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "seconds",
								Value: &jsonschema.Schema{
									Type:        string(schema.Integer),
									Description: "Seconds to wait for 'wait' action",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "timeout",
								Value: &jsonschema.Schema{
									Type:        string(schema.Integer),
									Description: "Timeout in seconds for 'set_timeout' action",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "search_engine",
								Value: &jsonschema.Schema{
									Type:        string(schema.String),
									Description: "Search engine for 'set_search_engine' action (google, baidu, bing, duckduckgo)",
								},
							},
							orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "headless",
								Value: &jsonschema.Schema{
									Type:        string(schema.Boolean),
									Description: "Headless mode for 'set_headless' action (true to hide browser, false to show)",
								},
							},
						),
					),
				},
			),
		},
		tabs:         make([]TabInfo, 0),
		searchTool:   config.DDGSearchTool,
		searchEngine: config.SearchEngine,
		cm:           config.ExtractChatModel,
		tpl:          prompt.FromMessages(schema.FString, schema.UserMessage(extractContentPrompt)),
		timeout:     timeout,
		headless:     config.Headless,
		cleanupOnce:  &sync.Once{},
	}

	// 保存配置，延迟初始化浏览器
	// 浏览器会在第一次调用 InvokableRun 时才启动
	but.pendingConfig = config

	return but, nil
}

func (b *Tool) initialize(ctx context.Context, config *Config) error {
	if config == nil {
		return fmt.Errorf("config is required")
	}

	if b.ctx != nil {
		b.Cleanup()
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
	}

	if !config.Headless {
		opts = append(opts, chromedp.Flag("headless", false))
	} else {
		opts = append(opts, chromedp.Headless)
	}

	if config.DisableSecurity {
		opts = append(opts, chromedp.Flag("disable-web-security", true))
		opts = append(opts, chromedp.Flag("allow-running-insecure-content", true))
	}

	// 处理布尔类型的额外参数
	for _, arg := range config.ExtraChromiumArgs {
		opts = append(opts, chromedp.Flag(arg, true))
	}

	// 处理带值的 Chromium 参数
	for name, value := range config.ChromiumFlags {
		opts = append(opts, chromedp.Flag(name, value))
	}

	// 处理 UserDataDir（用户数据目录，用于保留登录状态）
	if config.UserDataDir != "" {
		opts = append(opts, chromedp.UserDataDir(config.UserDataDir))
	}

	if config.ChromeInstancePath != "" {
		opts = append(opts, chromedp.ExecPath(config.ChromeInstancePath))
	} else if path := findChromePath(); path != "" {
		opts = append(opts, chromedp.ExecPath(path))
	}

	if config.ProxyServer != "" {
		opts = append(opts, chromedp.ProxyServer(config.ProxyServer))
	}

	b.allocatorCtx, b.allocatorCancel = chromedp.NewExecAllocator(context.Background(), opts...)

	logf := func(string, ...any) {}
	if config.Logf != nil {
		logf = config.Logf
	}
	b.ctx, _ = chromedp.NewContext(
		b.allocatorCtx,
		chromedp.WithLogf(logf),
	)

	if err := chromedp.Run(b.ctx); err != nil {
		// 如果启动失败，可能是找不到浏览器，返回友好提示
		if strings.Contains(err.Error(), "executable file not found") || strings.Contains(err.Error(), "exec: \"google-chrome\"") {
			return fmt.Errorf("Chrome browser not found. Please install Google Chrome or Chromium to use this tool.\nError: %v\n\nTips: You can install Chrome manually or specify the path in the configuration.", err)
		}
		return fmt.Errorf("failed to start browser: %v", err)
	}

	if err := b.updateTabsInfo(b.ctx); err != nil {
		return fmt.Errorf("failed to update tab info: %v", err)
	}

	return nil
}

func (b *Tool) updateTabsInfo(ctx context.Context) error {
	targets, err := chromedp.Targets(ctx)
	if err != nil {
		return err
	}

	b.tabs = make([]TabInfo, 0)
	for i, t := range targets {
		if t.Type == "page" {
			b.tabs = append(b.tabs, TabInfo{
				ID:       i,
				TargetID: t.TargetID,
				Title:    t.Title,
				URL:      t.URL,
			})
		}
	}

	return nil
}

type Param struct {
	Action Action `json:"action"`

	URL          *string `json:"url,omitempty"`
	Index        *int    `json:"index,omitempty"`
	Text         *string `json:"text,omitempty"`
	ScrollAmount *int    `json:"scroll_amount,omitempty"`
	TabID        *int    `json:"tab_id,omitempty"`
	Query        *string `json:"query,omitempty"`
	Goal         *string `json:"goal,omitempty"`
	Keys         *string `json:"keys,omitempty"`
	Seconds      *int    `json:"seconds,omitempty"`
	Timeout      *int    `json:"timeout,omitempty"`
	SearchEngine *string `json:"search_engine,omitempty"`
	Headless     *bool   `json:"headless,omitempty"`
}

type Action string

const (
	ActionGoToURL         Action = "go_to_url"
	ActionClickElement    Action = "click_element"
	ActionInputText       Action = "input_text"
	ActionScrollDown      Action = "scroll_down"
	ActionScrollUp        Action = "scroll_up"
	//ActionSendKeys       Action = "send_keys"
	ActionWebSearch       Action = "web_search"
	ActionWait            Action = "wait"
	ActionExtractContent  Action = "extract_content"
	ActionSwitchTab       Action = "switch_tab"
	ActionOpenTab         Action = "open_tab"
	ActionCloseTab        Action = "close_tab"
	ActionSetTimeout       Action = "set_timeout"
	ActionSetSearchEngine  Action = "set_search_engine"
	ActionSetHeadless     Action = "set_headless"
)

func (b *Tool) Execute(params *Param) (*ToolResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var result *ToolResult

	switch params.Action {
	case ActionGoToURL:
		if params.URL == nil {
			return &ToolResult{Error: "url is required for 'go_to_url' action"}, nil
		}
		url := *params.URL

		err := chromedp.Run(b.ctx,
			chromedp.Navigate(url),
			chromedp.WaitReady("body", chromedp.ByQuery),
		)
		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to navigate to %s: %v", url, err)}, nil
		}

		if err := b.updateElements(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
		}

		result = &ToolResult{Output: fmt.Sprintf("successfully navigated to %s\n\n%s", url, b.getPageContext())}

	case ActionClickElement:
		if params.Index == nil {
			return &ToolResult{Error: "index is required for 'click_element' action"}, nil
		}
		index := *params.Index
		if index >= len(b.elements) {
			return &ToolResult{Error: fmt.Sprintf("index %d out of range", index)}, nil
		}

		element := b.elements[index]
		err := chromedp.Run(b.ctx,
			chromedp.WaitVisible(element.XPath, chromedp.BySearch),
			chromedp.Click(element.XPath, chromedp.BySearch),
		)
		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to click element %d: %v", index, err)}, nil
		}

		// 增加等待时间，确保页面加载完成（特别是搜索结果页跳转）
		err = chromedp.Run(b.ctx, chromedp.Sleep(3*time.Second))

		if err := b.updateElements(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
		}

		result = &ToolResult{Output: fmt.Sprintf("successfully clicked element %d\n\n%s", index, b.getPageContext())}

	case ActionInputText:
		if params.Text == nil {
			return &ToolResult{Error: "text is required for 'input_text' action"}, nil
		}
		if params.Index == nil {
			return &ToolResult{Error: "index is required for 'input_text' action"}, nil
		}
		text := *params.Text
		index := *params.Index
		if index < 0 || index >= len(b.elements) {
			return &ToolResult{Error: "index out of range"}, nil
		}

		element := b.elements[index]

		// 使用 JavaScript 直接设置值，避免 chromedp.Clear/SendKeys 在某些情况下（如 textarea）报错
		// 错误示例：textarea node 181 does not have child #text node
		textJSON, err := sonic.MarshalString(text)
		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to marshal text: %v", err)}, nil
		}

		err = chromedp.Run(b.ctx,
			chromedp.Evaluate(fmt.Sprintf(`
				(() => {
					const result = document.evaluate('%s', document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
					const el = result.singleNodeValue;
					if (!el) throw new Error("element not found");
					
					el.focus();
					const target = %s;

					// 特殊处理 SELECT 元素：尝试匹配文本或值
					if (el.tagName === 'SELECT') {
						const options = Array.from(el.options);
						let found = false;
						
						// 1. 精确匹配 value
						for (let i = 0; i < options.length; i++) {
							if (options[i].value === target) {
								el.selectedIndex = i;
								found = true;
								break;
							}
						}
						
						// 2. 如果没找到，尝试模糊匹配 text
						if (!found) {
							const lowerTarget = target.toLowerCase();
							for (let i = 0; i < options.length; i++) {
								if (options[i].text.toLowerCase().includes(lowerTarget)) {
									el.selectedIndex = i;
									found = true;
									break;
								}
							}
						}

						if (!found) {
							// 尝试直接设置值
							el.value = target;
						}
					} else {
						// 普通 Input/Textarea
						el.value = target;
					}

					el.dispatchEvent(new Event('input', { bubbles: true }));
					el.dispatchEvent(new Event('change', { bubbles: true }));
				})()
			`, element.XPath, textJSON), nil),
		)

		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to input text to element %d: %v", index, err)}, nil
		}

		// 如果文本以 \n 结尾，尝试模拟回车键
		if len(text) > 0 && text[len(text)-1] == '\n' {
			err = chromedp.Run(b.ctx,
				chromedp.Evaluate(fmt.Sprintf(`
					(() => {
						const result = document.evaluate('%s', document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
						const el = result.singleNodeValue;
						if (el) {
							el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true }));
							el.dispatchEvent(new KeyboardEvent('keypress', { key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true }));
							el.dispatchEvent(new KeyboardEvent('keyup', { key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true }));
							
							// 尝试提交表单
							if (el.form) {
								// el.form.submit(); // 直接提交可能绕过验证，暂时注释
							}
						}
					})()
				`, element.XPath), nil),
			)
			if err != nil {
				// 忽略回车模拟错误，不影响主要功能
			}

			// 如果模拟了回车，稍微多等待一会儿
			chromedp.Run(b.ctx, chromedp.Sleep(2*time.Second))
		}

		// 输入完成后，刷新页面元素，因为输入可能会触发 DOM 变化（如下拉框、验证信息等）
		// 同时返回最新的交互元素列表，方便 AI 进行下一步操作
		if err := b.updateElements(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
		}

		result = &ToolResult{Output: fmt.Sprintf("successfully input text '%s' to element %d\n\n%s", text, index, b.getPageContext())}

	case ActionScrollDown, ActionScrollUp:
		direction := 1
		if params.Action == ActionScrollUp {
			direction = -1
		}

		var amount int
		if params.ScrollAmount == nil {
			amount = 500
		} else {
			amount = *params.ScrollAmount
		}

		script := fmt.Sprintf("window.scrollBy(0, %d);", direction*amount)
		err := chromedp.Run(b.ctx,
			chromedp.Evaluate(script, nil),
		)

		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to scroll: %v", err)}, nil
		}

		if err := b.updateElements(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
		}

		result = &ToolResult{Output: fmt.Sprintf("successfully scrolled %s %d pixels\n\n%s", params.Action, amount, b.getPageContext())}

	case ActionWait:
		var seconds = 3
		if params.Seconds != nil {
			seconds = *params.Seconds
		}
		// 限制最大等待时间为 60 秒
		if seconds > 60 {
			seconds = 60
		}

		err := chromedp.Run(b.ctx,
			chromedp.Sleep(time.Duration(seconds)*time.Second),
		)

		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to wait for %d seconds: %v", seconds, err)}, nil
		}

		if err := b.updateElements(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
		}

		result = &ToolResult{Output: fmt.Sprintf("successfully waited for %d seconds\n\n%s", seconds, b.getPageContext())}

	case ActionWebSearch:
		// 如果配置了 searchTool，使用 searchTool (DuckDuckGo API)
		if b.searchTool != nil {
			if params.Query == nil {
				return &ToolResult{Error: "query is required for 'web_search' action"}, nil
			}
			searchResults, err := b.searchTool.TextSearch(b.ctx, &duckduckgo.TextSearchRequest{
				Query: *params.Query,
			})
			if err != nil {
				return &ToolResult{Error: fmt.Sprintf("failed to search: %v", err)}, nil
			}
			if len(searchResults.Results) == 0 {
				return &ToolResult{Error: "search result is empty"}, nil
			}
			newCtx, _ := chromedp.NewContext(b.ctx)
			if err := chromedp.Run(newCtx,
				chromedp.Navigate(searchResults.Results[0].URL),
				chromedp.WaitReady("body", chromedp.ByQuery),
			); err != nil {
				return &ToolResult{Error: fmt.Sprintf("failed to open new tab: %v", err)}, nil
			}
			b.ctx = newCtx

			if err := b.updateTabsInfo(b.ctx); err != nil {
				return &ToolResult{Error: fmt.Sprintf("failed to update tab information: %v", err)}, nil
			}
			if err := b.updateElements(b.ctx); err != nil {
				return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
			}

			result = &ToolResult{Output: "successfully search web and opened new tab: " + searchResults.Results[0].URL + "\n\n" + b.getPageContext()}
		} else {
			// 如果没有配置 searchTool，回退到使用 搜索引擎
			if params.Query == nil {
				return &ToolResult{Error: "query is required for 'web_search' action"}, nil
			}

			// 构造搜索 URL
			var searchURL string
			query := url.QueryEscape(*params.Query)

			// 默认使用 baidu，如果配置了 SearchEngine 则使用配置的引擎
			engine := strings.ToLower(b.searchEngine)
			if engine == "" {
				engine = "baidu"
			}

			// 检查是否是自定义 URL 模板 (包含 %s)
			if strings.Contains(b.searchEngine, "%s") {
				searchURL = fmt.Sprintf(b.searchEngine, query)
				engine = "custom"
			} else {
				switch engine {
				case "baidu":
					searchURL = fmt.Sprintf("https://www.baidu.com/s?wd=%s", query)
				case "bing":
					searchURL = fmt.Sprintf("https://www.bing.com/search?q=%s", query)
				case "duckduckgo":
					searchURL = fmt.Sprintf("https://duckduckgo.com/?q=%s", query)
				default:
					// 默认为 Baidu
					searchURL = fmt.Sprintf("https://www.baidu.com/s?wd=%s", query)
					engine = "baidu"
				}
			}

			// 直接在当前标签页导航
			err := chromedp.Run(b.ctx,
				chromedp.Navigate(searchURL),
				chromedp.WaitReady("body", chromedp.ByQuery),
			)
			if err != nil {
				return &ToolResult{Error: fmt.Sprintf("failed to navigate to search url %s: %v", searchURL, err)}, nil
			}

			if err := b.updateElements(b.ctx); err != nil {
				return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
			}

			result = &ToolResult{Output: fmt.Sprintf("successfully searched for '%s' using %s\n\n%s", *params.Query, engine, b.getPageContext())}
		}

	case ActionExtractContent:
		if params.Goal == nil {
			return &ToolResult{Error: "goal is required for 'extract_content' action"}, nil
		}

		var content string
		// 使用 Turndown.js 将 HTML 转换为 Markdown
		// 我们通过 CDN 动态加载 TurndownService，或者直接注入其核心逻辑
		// 这里采用注入简化版逻辑的方式，将关键 HTML 元素转换为 Markdown 格式
		err := chromedp.Run(b.ctx,
			chromedp.Evaluate(`
				(() => {
					// 辅助函数：转义 Markdown 特殊字符
					function escape(text) {
						return text.replace(/([\\*_{}\[\]()#+\-.!])/g, '\\$1');
					}

					// 递归遍历 DOM 树并生成 Markdown
					function walk(node) {
						let result = "";
						
						// 处理文本节点
						if (node.nodeType === Node.TEXT_NODE) {
							let text = node.textContent.replace(/\s+/g, ' ');
							// 如果父节点是块级元素，则 trim，否则保留空格
							if (['P', 'DIV', 'LI', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'BLOCKQUOTE', 'PRE', 'CODE'].includes(node.parentNode.nodeName)) {
								// text = text.trim(); // 暂时不 trim，保留部分格式
							}
							return text;
						}

						// 忽略注释和不可见元素
						if (node.nodeType !== Node.ELEMENT_NODE) return "";
						const style = window.getComputedStyle(node);
						if (style.display === 'none' || style.visibility === 'hidden') return "";

						// 处理特定标签
						const tagName = node.tagName.toLowerCase();
						
						// 忽略无关标签
						if (['script', 'style', 'noscript', 'svg', 'img', 'video', 'audio', 'iframe', 'link', 'meta'].includes(tagName)) {
							return "";
						}

						// 递归处理子节点
						let childrenMarkdown = "";
						node.childNodes.forEach(child => {
							childrenMarkdown += walk(child);
						});

						// 根据标签类型包装 Markdown
						switch (tagName) {
							case 'h1': return '\n# ' + childrenMarkdown.trim() + '\n\n';
							case 'h2': return '\n## ' + childrenMarkdown.trim() + '\n\n';
							case 'h3': return '\n### ' + childrenMarkdown.trim() + '\n\n';
							case 'h4': return '\n#### ' + childrenMarkdown.trim() + '\n\n';
							case 'h5': return '\n##### ' + childrenMarkdown.trim() + '\n\n';
							case 'h6': return '\n###### ' + childrenMarkdown.trim() + '\n\n';
							case 'p': return '\n' + childrenMarkdown.trim() + '\n\n';
							case 'br': return '\n';
							case 'hr': return '\n---\n';
							case 'b':
							case 'strong': return '**' + childrenMarkdown + '**';
							case 'i':
							case 'em': return '*' + childrenMarkdown + '*';
							case 'a': 
								const href = node.getAttribute('href');
								return href ? '[' + childrenMarkdown + '](' + href + ')' : childrenMarkdown;
							case 'ul': return '\n' + childrenMarkdown + '\n';
							case 'ol': return '\n' + childrenMarkdown + '\n';
							case 'li': return '- ' + childrenMarkdown.trim() + '\n';
							case 'code': return '\x60' + childrenMarkdown + '\x60';
							case 'pre': return '\n\x60\x60\x60\n' + node.innerText + '\n\x60\x60\x60\n\n'; // pre 特殊处理，直接取 innerText
							case 'blockquote': return '\n> ' + childrenMarkdown.trim() + '\n\n';
							case 'div': 
							case 'section':
							case 'article':
							case 'main':
							case 'header':
							case 'footer':
							case 'nav':
								return '\n' + childrenMarkdown + '\n';
							default: return childrenMarkdown;
						}
					}

					return walk(document.body).replace(/\n{3,}/g, '\n\n').trim();
				})()
			`, &content),
		)
		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("extract content fail: %v", err)}, nil
		}

		// 限制内容长度，防止超出上下文限制
		const maxContentLength = 50000
		if len(content) > maxContentLength {
			content = content[:maxContentLength] + "...(truncated)"
		}

		if b.cm == nil {
			result = &ToolResult{Output: fmt.Sprintf("extract content (markdown):\n%s", content)}
		} else {
			message, err := b.tpl.Format(b.ctx, map[string]interface{}{
				"goal": *params.Goal,
				"page": content,
			})
			if err != nil {
				return &ToolResult{Error: fmt.Sprintf("format extract prompt fail: %v", err)}, nil
			}

			extractResult, err := b.cm.Generate(b.ctx, message)
			if err != nil {
				return &ToolResult{Error: fmt.Sprintf("generate extract content fail: %v", err)}, nil
			}

			result = &ToolResult{Output: fmt.Sprintf("extract content: %s", extractResult)}
		}

	case ActionOpenTab:
		if params.URL == nil {
			return &ToolResult{Error: "url is required for 'open_tab' action"}, nil
		}
		url := *params.URL

		newCtx, _ := chromedp.NewContext(b.ctx)
		if err := chromedp.Run(newCtx,
			chromedp.Navigate(url),
			chromedp.WaitReady("body", chromedp.ByQuery),
		); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to open new tab: %v", err)}, nil
		}
		b.ctx = newCtx

		if err := b.updateTabsInfo(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update tab information: %v", err)}, nil
		}
		if err := b.updateElements(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
		}

		result = &ToolResult{Output: fmt.Sprintf("successfully opened new tab %s\n\nInteractive elements:\n%s", url, b.getInteractiveElements())}

	case ActionSwitchTab:
		if params.TabID == nil {
			return &ToolResult{Error: "tabID is required for 'switch_tab' action"}, nil
		}
		tabID := *params.TabID

		if tabID < 0 || tabID >= len(b.tabs) {
			return &ToolResult{Error: fmt.Sprintf("tab ID %d out of range", tabID)}, nil
		}

		targetID := b.tabs[tabID].TargetID

		newCtx, _ := chromedp.NewContext(b.ctx, chromedp.WithTargetID(targetID))
		err := chromedp.Run(newCtx, target.ActivateTarget(targetID))
		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to switch tab: %v", err)}, nil
		}

		b.ctx = newCtx
		b.currentTabID = tabID

		if err := b.updateTabsInfo(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update tab information: %v", err)}, nil
		}
		if err := b.updateElements(b.ctx); err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
		}

		result = &ToolResult{Output: fmt.Sprintf("successfully switched to tab %d\n\n%s", tabID, b.getPageContext())}

	case ActionCloseTab:
		err := chromedp.Run(b.ctx, page.Close())

		if err != nil {
			return &ToolResult{Error: fmt.Sprintf("failed to close tab: %v", err)}, nil
		}

		if len(b.tabs) > 1 {
			if err := b.updateTabsInfo(b.ctx); err != nil {
				return &ToolResult{Error: fmt.Sprintf("failed to update tab information: %v", err)}, nil
			}

			if len(b.tabs) > 0 {
				newTargetID := b.tabs[0].TargetID

				newCtx, _ := chromedp.NewContext(b.ctx, chromedp.WithTargetID(newTargetID))
				b.ctx = newCtx
				b.currentTabID = b.tabs[0].ID

				if err := b.updateElements(b.ctx); err != nil {
					return &ToolResult{Error: fmt.Sprintf("failed to update elements: %v", err)}, nil
				}
			}
		}

		result = &ToolResult{Output: fmt.Sprintf("successfully closed current tab\n\n%s", b.getPageContext())}

	case ActionSetTimeout:
		if params.Timeout == nil {
			return &ToolResult{Error: "timeout is required for 'set_timeout' action"}, nil
		}
		newTimeout := *params.Timeout
		if newTimeout <= 0 {
			return &ToolResult{Error: "timeout must be greater than 0"}, nil
		}
		oldTimeout := b.timeout
		b.timeout = newTimeout
		result = &ToolResult{Output: fmt.Sprintf("successfully set timeout from %d to %d seconds", oldTimeout, newTimeout)}

	case ActionSetSearchEngine:
		if params.SearchEngine == nil {
			return &ToolResult{Error: "search_engine is required for 'set_search_engine' action"}, nil
		}
		newEngine := *params.SearchEngine
		// 验证搜索引擎是否有效
		validEngines := map[string]bool{"google": true, "baidu": true, "bing": true, "duckduckgo": true}
		engineLower := strings.ToLower(newEngine)
		if !validEngines[engineLower] && !strings.Contains(newEngine, "%s") {
			return &ToolResult{Error: fmt.Sprintf("invalid search engine: %s. Valid options are: google, baidu, bing, duckduckgo, or a custom URL template with %%s", newEngine)}, nil
		}
		oldEngine := b.searchEngine
		b.searchEngine = newEngine
		result = &ToolResult{Output: fmt.Sprintf("successfully set search engine from '%s' to '%s'", oldEngine, newEngine)}

	case ActionSetHeadless:
		if params.Headless == nil {
			return &ToolResult{Error: "headless is required for 'set_headless' action"}, nil
		}
		newHeadless := *params.Headless
		oldHeadless := b.headless
		b.headless = newHeadless

		// 如果 headless 值发生变化，需要重启浏览器
		if oldHeadless != newHeadless && b.pendingConfig != nil {
			// 更新 pendingConfig 中的 Headless 值
			b.pendingConfig.Headless = newHeadless

			// 清理当前浏览器
			b.safeCleanup()

			// 重新初始化浏览器（延迟到下次操作时）
			b.initialized = false

			result = &ToolResult{Output: fmt.Sprintf("successfully set headless from %t to %t. Browser will restart with new settings on next action.", oldHeadless, newHeadless)}
		} else {
			result = &ToolResult{Output: fmt.Sprintf("headless is already %t, no change needed", newHeadless)}
		}

	default:
		return &ToolResult{Error: fmt.Sprintf("unknown action: %s", params.Action)}, nil
	}

	return result, nil
}

func (b *Tool) getPageContext() string {
	var url, title string
	chromedp.Run(b.ctx,
		chromedp.Location(&url),
		chromedp.Title(&title),
	)
	return fmt.Sprintf("URL: %s\nTitle: %s\n\nInteractive elements:\n%s", url, title, b.getInteractiveElements())
}

func (b *Tool) getInteractiveElements() string {
	var interactiveElements string
	for _, elem := range b.elements {
		interactiveElements += fmt.Sprintf("[%d] %s\n", elem.Index, elem.Description)
	}
	return interactiveElements
}

func (b *Tool) updateElements(ctx context.Context) error {
	// 设置超时，防止页面元素过多导致卡死
	timeout := b.timeout
	if timeout <= 0 {
		timeout = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	var nodes []*cdp.Node
	err := chromedp.Run(ctx,
		chromedp.Nodes("a, button, input, select, textarea", &nodes, chromedp.ByQueryAll),
	)
	if err != nil {
		return err
	}

	// 限制处理的节点数量，避免性能问题
	if len(nodes) > 500 {
		nodes = nodes[:500]
	}

	b.elements = make([]ElementInfo, 0, len(nodes))

	// 批量检查可见性，减少 RPC 调用
	// 先构建所有节点的 XPath 列表
	xpaths := make([]string, len(nodes))
	for i, node := range nodes {
		xpaths[i] = node.FullXPath()
	}

	xpathsJSON, err := sonic.MarshalString(xpaths)
	if err != nil {
		return fmt.Errorf("failed to marshal xpaths: %v", err)
	}

	var visibleIndices []int
	// 限制返回的元素数量，避免 token 超限
	// 增加到 100 个，以覆盖更多搜索结果
	const maxElements = 100

	err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`
		(() => {
			const xpaths = %s;
			const visibleIndices = [];
			const maxElements = %d;
			
			// 辅助函数：检查元素是否可见
			function isElementVisible(el) {
				if (!el) return false;
				
				// 检查元素是否在文档中
				if (!document.contains(el)) return false;

				// 检查元素尺寸
				const rect = el.getBoundingClientRect();
				if (rect.width === 0 || rect.height === 0) return false;
				
				// 检查 CSS 样式
				const style = window.getComputedStyle(el);
				if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false;
				
				// 检查是否在 viewport 内（可选，目前只检查是否渲染）
				// const viewHeight = Math.max(document.documentElement.clientHeight, window.innerHeight);
				// if (rect.bottom < 0 || rect.top - viewHeight >= 0) return false;

				return true;
			}

			for (let i = 0; i < xpaths.length; i++) {
				if (visibleIndices.length >= maxElements) break;
				
				try {
					const result = document.evaluate(xpaths[i], document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
					const el = result.singleNodeValue;
					if (isElementVisible(el)) {
						visibleIndices.push(i);
					}
				} catch (e) {
					// 忽略错误的 XPath
				}
			}
			return visibleIndices;
		})()
	`, xpathsJSON, maxElements), &visibleIndices))

	if err != nil {
		return fmt.Errorf("failed to check visibility: %v", err)
	}

	// 根据可见性索引构建 elements 列表
	for _, idx := range visibleIndices {
		if idx < 0 || idx >= len(nodes) {
			continue
		}
		node := nodes[idx]

		var description string

		// 获取节点的文本内容，辅助描述
		var textContent string
		// 对于 Link 和 Button，尝试获取 innerText
		if node.NodeName == "A" || node.NodeName == "BUTTON" {
			err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`
				(() => {
					const result = document.evaluate('%s', document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
					const el = result.singleNodeValue;
					return el ? (el.innerText || el.textContent || '').trim() : '';
				})()
			`, node.FullXPath()), &textContent))
			if err != nil {
				// ignore error
			}
			// 限制文本长度
			if len(textContent) > 50 {
				textContent = textContent[:50] + "..."
			}
			// 如果有换行，替换为空格
			textContent = strings.ReplaceAll(textContent, "\n", " ")
		}

		switch node.NodeName {
		case "A":
			description = fmt.Sprintf("Link: %s (href=%s)", textContent, node.AttributeValue("href"))
		case "BUTTON":
			// 优先使用获取到的 textContent，如果为空则尝试 attribute
			if textContent == "" {
				textContent = node.AttributeValue("textContent")
			}
			if textContent == "" {
				textContent = node.AttributeValue("value")
			}
			description = fmt.Sprintf("Button: %s", textContent)
		case "INPUT":
			inputType := node.AttributeValue("type")
			// 尝试获取 value 属性（对于 submit 按钮很有用）
			value := node.AttributeValue("value")
			placeholder := node.AttributeValue("placeholder")

			desc := fmt.Sprintf("Input(%s)", inputType)
			if value != "" {
				desc += fmt.Sprintf(" value='%s'", value)
			}
			if placeholder != "" {
				desc += fmt.Sprintf(" placeholder='%s'", placeholder)
			}
			description = desc
		case "SELECT":
			// 获取选项列表
			var options []string
			err = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`
				(() => {
					const result = document.evaluate('%s', document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
					const el = result.singleNodeValue;
					if (!el) return [];
					return Array.from(el.options).map(o => o.text + ' (value=' + o.value + ')');
				})()
			`, node.FullXPath()), &options))

			if len(options) > 10 {
				options = append(options[:10], fmt.Sprintf("... (%d more)", len(options)-10))
			}
			description = fmt.Sprintf("Select Dropdown: %s Options: [%s]", node.AttributeValue("name"), strings.Join(options, ", "))
		case "TEXTAREA":
			description = fmt.Sprintf("TextArea: %s", node.AttributeValue("placeholder"))
		}

		// 使用当前 visibleNodes 的索引作为 ElementInfo 的 Index
		// 注意：这里的 Index 是用户交互时使用的索引，应该连续
		currentIndex := len(b.elements)
		b.elements = append(b.elements, ElementInfo{
			Index:       currentIndex,
			Description: description,
			Type:        node.NodeName,
			XPath:       node.FullXPath(),
		})
	}

	return nil
}

func (b *Tool) Cleanup() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.cleanupOnce.Do(func() {
		if b.allocatorCancel != nil {
			// 使用 recover 捕获可能的 "close of closed channel" panic
			defer func() {
				if r := recover(); r != nil {
					// 忽略 close of closed channel panic，这是 chromedp 内部的问题
				}
			}()
			b.allocatorCancel()
			b.allocatorCancel = nil
		}
	})

	b.ctx = nil
	b.allocatorCtx = nil
	b.elements = nil
	b.tabs = nil
	b.initialized = false
}

func (b *Tool) GetCurrentState() (*BrowserState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.ctx == nil {
		return nil, fmt.Errorf("browser not initialized")
	}

	var url, title string
	err := chromedp.Run(b.ctx,
		chromedp.Location(&url),
		chromedp.Title(&title),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get url info: %w", err)
	}

	var scrollHeight, clientHeight, scrollTop float64
	err = chromedp.Run(b.ctx,
		chromedp.Evaluate(`
			(() => {
				return {
					scrollHeight: document.documentElement.scrollHeight,
					clientHeight: document.documentElement.clientHeight,
					scrollTop: document.documentElement.scrollTop
				};
			})()
		`, &struct {
			ScrollHeight *float64 `json:"scrollHeight"`
			ClientHeight *float64 `json:"clientHeight"`
			ScrollTop    *float64 `json:"scrollTop"`
		}{
			&scrollHeight,
			&clientHeight,
			&scrollTop,
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get scroll info: %w", err)
	}

	if err := b.updateElements(b.ctx); err != nil {
		return nil, fmt.Errorf("failed to update elements: %w", err)
	}

	if err := b.updateTabsInfo(b.ctx); err != nil {
		return nil, fmt.Errorf("failed to update tab information: %w", err)
	}

	var elementsJS string
	for _, elem := range b.elements {
		elementsJS += fmt.Sprintf(`{xpath: "%s", index: %d},`, elem.XPath, elem.Index)
	}
	err = chromedp.Run(b.ctx, chromedp.Evaluate(fmt.Sprintf(`
		(() => {
			// 移除之前可能存在的标记
			const oldMarkers = document.querySelectorAll('.eino-element-marker, .eino-element-border');
			oldMarkers.forEach(marker => marker.remove());
			
			// 使用XPath查找元素并添加标记
			const elements = [%s];
			
			elements.forEach(elem => {
				try {
					// 使用XPath查找元素
					const result = document.evaluate(elem.xpath, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
					const el = result.singleNodeValue;
					if (!el) return;
					
					// 创建序号标记
					const marker = document.createElement('div');
					marker.className = 'eino-element-marker';
					marker.textContent = elem.index;
					marker.style.position = 'absolute';
					marker.style.zIndex = '10000';
					marker.style.backgroundColor = '#f44336';
					marker.style.color = 'white';
					marker.style.padding = '1px 4px';
					marker.style.borderRadius = '2px';
					marker.style.fontSize = '8px';
					marker.style.fontWeight = 'bold';
					marker.style.boxShadow = '0 0 2px rgba(0,0,0,0.3)';
					
					// 获取元素位置
					const rect = el.getBoundingClientRect();
					marker.style.top = (window.scrollY + rect.top - 10) + 'px';
					marker.style.left = (window.scrollX + rect.left - 5) + 'px';
					
					// 创建元素边框
					const border = document.createElement('div');
					border.className = 'eino-element-border';
					border.style.position = 'absolute';
					border.style.zIndex = '9999';
					border.style.border = '2px solid #f44336';
					border.style.borderRadius = '3px';
					border.style.pointerEvents = 'none';
					
					// 设置边框位置和大小
					border.style.top = (window.scrollY + rect.top) + 'px';
					border.style.left = (window.scrollX + rect.left) + 'px';
					border.style.width = rect.width + 'px';
					border.style.height = rect.height + 'px';
					
					document.body.appendChild(marker);
					document.body.appendChild(border);
				} catch (e) {
					console.error('Error adding marker for element:', e);
				}
			});
		})()
	`, elementsJS), nil))

	if err != nil {
		return nil, fmt.Errorf("failed to add element markers: %w", err)
	}

	var buf []byte
	err = chromedp.Run(b.ctx,
		chromedp.CaptureScreenshot(&buf),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to capture screenshot: %w", err)
	}

	return &BrowserState{
		URL:                 url,
		Title:               title,
		Tabs:                b.tabs,
		InteractiveElements: b.getInteractiveElements(),
		ScrollInfo: ScrollInfo{
			PixelsAbove: int(scrollTop),
			PixelsBelow: int(scrollHeight - clientHeight - scrollTop),
			TotalHeight: int(scrollHeight),
		},
		ViewportHeight: int(clientHeight),
		Screenshot:     base64.StdEncoding.EncodeToString(buf),
	}, nil
}
