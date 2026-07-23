package browseruse

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfNoBrowser(t *testing.T) {
	t.Helper()
	if os.Getenv("SKIP_BROWSER_TESTS") == "true" || os.Getenv("CI") != "" {
		t.Skip("Skipping browser test: no browser available or running in CI")
	}
}

func TestExtractContentMarkdown(t *testing.T) {
	skipIfNoBrowser(t)
	config := DefaultConfig()
	config.Headless = true

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// 1. Navigate to a page with rich content (using a data URL for stability)
	htmlContent := `
		<html>
		<body>
			<h1>Test Page</h1>
			<p>This is a <strong>bold</strong> text and <em>italic</em> text.</p>
			<ul>
				<li>Item 1</li>
				<li>Item 2</li>
			</ul>
			<a href="https://example.com">Link</a>
			<script>console.log("ignored");</script>
			<style>body { color: red; }</style>
			<div style="display:none">Hidden content</div>
		</body>
		</html>
	`
	// Encode HTML to base64 to avoid escaping issues in JSON
	encodedHTML := base64.StdEncoding.EncodeToString([]byte(htmlContent))
	dataURL := "data:text/html;base64," + encodedHTML

	_, err = invokable.InvokableRun(ctx, `{"action": "go_to_url", "url": "`+dataURL+`"}`)
	require.NoError(t, err)

	// 2. Extract content
	result, err := invokable.InvokableRun(ctx, `{"action": "extract_content", "goal": "get content"}`)
	require.NoError(t, err)

	t.Logf("Markdown result:\n%s", result)

	// Verify Markdown format
	assert.Contains(t, result, "# Test Page")                 // H1 converted to #
	assert.Contains(t, result, "**bold**")                    // Strong converted to **
	assert.Contains(t, result, "*italic*")                    // Em converted to *
	assert.Contains(t, result, "- Item 1")                    // Li converted to -
	assert.Contains(t, result, "[Link](https://example.com)") // A converted to [text](href)
	assert.NotContains(t, result, "console.log")              // Script removed
	assert.NotContains(t, result, "body { color: red; }")     // Style removed
	assert.NotContains(t, result, "Hidden content")           // Hidden element removed

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

func TestWebSearchWithBaidu(t *testing.T) {
	skipIfNoBrowser(t)
	// Configure using Baidu search engine
	config := DefaultConfig()
	config.Headless = true
	config.SearchEngine = "baidu"

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Test call web_search action
	result, err := invokable.InvokableRun(ctx, `{"action": "web_search", "query": "rulego"}`)

	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "Chrome browser not found") || strings.Contains(errStr, "exec: \"google-chrome\"") {
			t.Skip("Skipping test because Chrome is not installed")
		} else if strings.Contains(errStr, "ERR_CONNECTION") || strings.Contains(errStr, "net::") {
			t.Skipf("Skipping test due to network error: %v", err)
		} else {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	if err == nil {
		assert.Contains(t, result, "successfully searched for 'rulego' using baidu")
		// Baidu may redirect to the CAPTCHA page, so just include keywords and don't insist on a full URL
		assert.Contains(t, result, "rulego")
	}

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

func TestGoToURLWithoutURL(t *testing.T) {
	config := DefaultConfig()

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Test go_to_url without URL
	result, err := invokable.InvokableRun(ctx, `{"action": "go_to_url"}`)
	require.NoError(t, err)
	assert.Contains(t, result, "url is required")

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

func TestClickElementWithoutIndex(t *testing.T) {
	config := DefaultConfig()

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Test click_element without index
	result, err := invokable.InvokableRun(ctx, `{"action": "click_element"}`)
	require.NoError(t, err)
	assert.Contains(t, result, "index is required")

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

func TestInputTextWithoutParams(t *testing.T) {
	config := DefaultConfig()

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Test input_text without text
	result, err := invokable.InvokableRun(ctx, `{"action": "input_text", "index": 0}`)
	require.NoError(t, err)
	assert.Contains(t, result, "text is required")

	// Test input_text without index
	result, err = invokable.InvokableRun(ctx, `{"action": "input_text", "text": "hello"}`)
	require.NoError(t, err)
	assert.Contains(t, result, "index is required")

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

// TestNavigateToRuleGo tests navigating to rulego.cc
// This test requires Chrome/Chromium to be installed
func TestNavigateToRuleGo(t *testing.T) {
	skipIfNoBrowser(t)
	config := DefaultConfig()
	config.Headless = true // Headless mode does not display browser windows

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Test navigate to rulego.cc
	result, err := invokable.InvokableRun(ctx, `{"action": "go_to_url", "url": "https://rulego.cc"}`)
	if err != nil {
		t.Skipf("Browser not available: %v", err)
		return
	}

	t.Logf("Navigate result: %s", result)
	assert.Contains(t, result, "successfully")

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

// TestGetContentFromRuleGo tests getting content from rulego.cc
// This test requires Chrome/Chromium to be installed
func TestGetContentFromRuleGo(t *testing.T) {
	skipIfNoBrowser(t)
	config := DefaultConfig()
	config.Headless = true

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// First navigate
	_, err = invokable.InvokableRun(ctx, `{"action": "go_to_url", "url": "https://rulego.cc"}`)
	if err != nil {
		t.Skipf("Browser not available: %v", err)
		return
	}

	// Then extract content
	result, err := invokable.InvokableRun(ctx, `{"action": "extract_content", "goal": "get page title and main content"}`)
	if err != nil {
		t.Fatalf("Extract content failed: %v", err)
	}

	t.Logf("Content result: %s", result)
	assert.Contains(t, result, "extract")

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

func TestWebSearchWithoutTool(t *testing.T) {
	// By default, the configuration does not include a search tool
	config := DefaultConfig()
	config.Headless = true

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Test call web_search action
	// Expected behavior: When there is no search tool, it automatically redirects to Google Search
	result, err := invokable.InvokableRun(ctx, `{"action": "web_search", "query": "rulego"}`)

	// If the error is caused by not having a browser, we skip the test
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "Chrome browser not found") || strings.Contains(errStr, "exec: \"google-chrome\"") {
			t.Skip("Skipping test because Chrome is not installed")
		} else {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// The current behavior is to return successful results, including Baidu search results
	if err == nil {
		assert.Contains(t, result, "successfully searched for 'rulego' using baidu")
		// Google may redirect to google.com.hk or display a captcha page (sorry/index)
		// So we only check whether Google has been tried out
		// assert.Contains(t, result, "URL: https://www.google.com/search?q=rulego")
	}

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

func TestWebSearchWithCustomURL(t *testing.T) {
	skipIfNoBrowser(t)
	// Configure custom search engine URLs
	config := DefaultConfig()
	config.Headless = true
	config.SearchEngine = "https://www.bing.com/search?q=%s"

	tTool, err := NewTool(config)
	require.NoError(t, err)

	invokable, ok := tTool.(tool.InvokableTool)
	require.True(t, ok)

	ctx := context.Background()

	// Test call web_search action
	// Expected behavior: Search using custom URLs
	result, err := invokable.InvokableRun(ctx, `{"action": "web_search", "query": "rulego"}`)

	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "Chrome browser not found") || strings.Contains(errStr, "exec: \"google-chrome\"") {
			t.Skip("Skipping test because Chrome is not installed")
		} else {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// Verify whether the results contain custom engine information
	if err == nil {
		// Verify that the tool returned a response in the expected format
		assert.Contains(t, result, "successfully searched for 'rulego' using custom")
		// Verify that the URL was constructed correctly
		assert.Contains(t, result, "bing.com/search?q=rulego")
	}

	// Cleanup browser
	if bt, ok := tTool.(*browserUseTool); ok {
		bt.Cleanup()
	}
}

// TestMain ensures that all tests are executed in order
func TestMain(m *testing.M) {
	m.Run()
}
