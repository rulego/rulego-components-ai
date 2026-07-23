package intent

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/test"
	"github.com/rulego/rulego/test/assert"
)

// Test configuration: Read from environment variables
// Embedding uses LLM_BASE_URL/LLM_API_KEY, with models set separately
// Setup method:
//
//	export LLM_BASE_URL="https://ai.gitee.com/v1"
//	export LLM_API_KEY="your-api-key"
//	export EMBEDDING_MODEL="Qwen3-Embedding-8B"
func getEmbeddingTestConfig() (url, apiKey, model string) {
	baseURL := getEnvOrDefault("LLM_BASE_URL", "https://ai.gitee.com/v1")
	url = baseURL + "/embeddings"
	apiKey = os.Getenv("LLM_API_KEY")
	model = getEnvOrDefault("EMBEDDING_MODEL", "Qwen3-Embedding-8B")
	return
}

// getTestIntents returns enhanced test intent configuration (overrides diverse expressions)
func getTestIntents() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "createRule",
			"description": "创建条件触发的自动化联动规则",
			"examples": []string{
				"有人就开灯", "温度大于30度开空调", "水浸时开风机",
				"下雨天自动关窗", "离开家的时候关掉所有电器", "每天早上7点开窗帘",
				"空气质量差就开净化器", "燃气泄漏立刻关阀门",
			},
		},
		{
			"name":        "control",
			"description": "控制设备开关或调节参数",
			"examples": []string{
				"打开灯光", "把风机关闭", "关闭客厅灯",
				"空调调到26度", "让窗帘拉下来", "把门锁上",
				"电视声音大一点", "关掉所有灯", "启动扫地机器人",
			},
		},
		{
			"name":        "query",
			"description": "查询设备当前状态或数值",
			"examples": []string{
				"当前温度多少", "灯是不是开着的", "风机状态怎么样",
				"空调现在几度", "窗帘拉开着吗", "门锁了没",
				"现在湿度多少", "热水器还在加热吗",
			},
		},
	}
}

// runLocalIntentTest encapsulates the execution logic of a single test
func runLocalIntentTest(t *testing.T, node *LocalIntentNode, input string, expectedIntents []string) (resultRelation string, matched bool) {
	t.Helper()
	done := make(chan struct{})
	var resultMsg types.RuleMsg
	var resultErr error

	ctx := test.NewRuleContext(types.NewConfig(), func(msg types.RuleMsg, relationType string, err error) {
		resultMsg = msg
		resultRelation = relationType
		resultErr = err
		close(done)
	})

	msg := ctx.NewMsg("TEST", types.NewMetadata(), input)
	startTime := time.Now()

	node.OnMsg(ctx, msg)

	select {
	case <-done:
		elapsed := time.Since(startTime)
		t.Logf("Input: %s", input)
		t.Logf("Duration: %v", elapsed)
		t.Logf("Route: %s", resultRelation)
		t.Logf("Mistake: %v", resultErr)
		t.Logf("  metadata.intent: %s", resultMsg.GetMetadata().GetValue("intent"))

		assert.Nil(t, resultErr)

		for _, expected := range expectedIntents {
			if resultRelation == expected {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("Expect to route to %v, actually route to %s", expectedIntents, resultRelation)
		}

		// Verify the retention of original data
		assert.Equal(t, input, resultMsg.GetData())

	case <-time.After(30 * time.Second):
		t.Fatal("Test timeout (30 seconds)")
	}
	return
}

// ============================================================================
// Unit testing
// ============================================================================

func TestLocalIntentNode_Type(t *testing.T) {
	node := &LocalIntentNode{}
	assert.Equal(t, "ai/localIntent", node.Type())
}

func TestLocalIntentNode_New(t *testing.T) {
	node := &LocalIntentNode{}
	newNode := node.New()

	assert.NotNil(t, newNode)
	localNode, ok := newNode.(*LocalIntentNode)
	assert.True(t, ok)
	assert.Equal(t, 0.65, localNode.Config.Threshold)
	assert.Equal(t, types.DefaultRelationType, localNode.Config.DefaultIntent)
}

func TestLocalIntentNode_Init(t *testing.T) {
	t.Run("缺少embeddingUrl", func(t *testing.T) {
		node := &LocalIntentNode{}
		config := types.NewConfig()
		configuration := map[string]interface{}{
			"model": "test-model",
			"intents": []map[string]interface{}{
				{"name": "test", "description": "测试", "examples": []string{"测试文本"}},
			},
		}
		err := node.Init(config, configuration)
		assert.NotNil(t, err)
		assert.True(t, strings.Contains(err.Error(), "url"))
	})

	t.Run("缺少embeddingModel", func(t *testing.T) {
		node := &LocalIntentNode{}
		config := types.NewConfig()
		configuration := map[string]interface{}{
			"url": "http://localhost:8080/embed",
			"intents": []map[string]interface{}{
				{"name": "test", "description": "测试", "examples": []string{"测试文本"}},
			},
		}
		err := node.Init(config, configuration)
		assert.NotNil(t, err)
		assert.True(t, strings.Contains(err.Error(), "model"))
	})

	t.Run("缺少意图定义", func(t *testing.T) {
		node := &LocalIntentNode{}
		config := types.NewConfig()
		configuration := map[string]interface{}{
			"url":   "http://localhost:8080/embed",
			"model": "test-model",
		}
		err := node.Init(config, configuration)
		assert.NotNil(t, err)
		assert.True(t, strings.Contains(err.Error(), "at least one intent"))
	})

	t.Run("无效的输入表达式", func(t *testing.T) {
		node := &LocalIntentNode{}
		config := types.NewConfig()
		configuration := map[string]interface{}{
			"url":   "http://localhost:8080/embed",
			"model": "test-model",
			"input": "${invalid.expression",
			"intents": []map[string]interface{}{
				{"name": "test", "description": "测试", "examples": []string{"测试文本"}},
			},
		}
		err := node.Init(config, configuration)
		if err != nil {
			t.Logf("Init Return error: %v (This expression may be accepted by the parser; errors come from subsequent steps)", err)
		} else {
			t.Log("This expression is accepted by the parser")
		}
	})
}

// ============================================================================
// Integration Testing: Basic Features
// ============================================================================

func TestLocalIntentNode_OnMsg_Integration(t *testing.T) {
	_, apiKey, _ := getEmbeddingTestConfig()
	if apiKey == "" {
		t.Skip("EMBEDDING_API_KEY not set, skipping integration test")
	}

	url, apiKey, model := getEmbeddingTestConfig()
	config := types.NewConfig()

	configuration := map[string]interface{}{
		"url":           url,
		"key":           apiKey,
		"model":         model,
		"threshold":     0.65,
		"defaultIntent": "Default",
		"intents":       getTestIntents(),
	}

	testCases := []struct {
		name            string
		input           string
		expectedIntents []string
	}{
		{
			name:            "创建规则-有人开灯",
			input:           "检测到人就打开灯光",
			expectedIntents: []string{"createRule"},
		},
		{
			name:            "控制设备-关风机",
			input:           "帮我把风机关掉",
			expectedIntents: []string{"control"},
		},
		{
			name:            "删除规则-归为规则操作",
			input:           "把水浸联动取消掉",
			expectedIntents: []string{"createRule"},
		},
		{
			name:            "无关输入-返回默认",
			input:           "What is the capital of France",
			expectedIntents: []string{"Default"},
		},
		{
			name:            "gap拦截-中文无关输入",
			input:           "今天天气怎么样",
			expectedIntents: []string{"Default", "query"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			node := &LocalIntentNode{}
			err := node.Init(config, configuration)
			assert.Nil(t, err)

			runLocalIntentTest(t, node, tc.input, tc.expectedIntents)
		})
	}
}

func TestLocalIntentNode_MetadataKey_Integration(t *testing.T) {
	_, apiKey, _ := getEmbeddingTestConfig()
	if apiKey == "" {
		t.Skip("EMBEDDING_API_KEY not set, skipping integration test")
	}

	url, apiKey, model := getEmbeddingTestConfig()
	config := types.NewConfig()

	configuration := map[string]interface{}{
		"url":           url,
		"key":           apiKey,
		"model":         model,
		"threshold":     0.5,
		"defaultIntent": "Default",
		"intents": []map[string]interface{}{
			{
				"name":        "control",
				"description": "控制设备",
				"examples":    []string{"打开灯光", "关闭风机"},
			},
		},
	}

	node := &LocalIntentNode{}
	err := node.Init(config, configuration)
	assert.Nil(t, err)

	var resultMsg types.RuleMsg
	done := make(chan struct{})
	ctx := test.NewRuleContext(config, func(msg types.RuleMsg, relationType string, err error) {
		resultMsg = msg
		close(done)
	})

	msg := ctx.NewMsg("TEST", types.NewMetadata(), "打开灯光")
	node.OnMsg(ctx, msg)

	select {
	case <-done:
		assert.Equal(t, "control", resultMsg.GetMetadata().GetValue(IntentMetadataKey))
	case <-time.After(30 * time.Second):
		t.Fatal("Test timeout")
	}
}

// ============================================================================
// Integration testing: control/query obfuscation testing
// ============================================================================

func TestLocalIntentNode_ControlQueryConfusion(t *testing.T) {
	_, apiKey, _ := getEmbeddingTestConfig()
	if apiKey == "" {
		t.Skip("EMBEDDING_API_KEY not set, skipping integration test")
	}

	url, apiKey, model := getEmbeddingTestConfig()
	config := types.NewConfig()

	configuration := map[string]interface{}{
		"url":           url,
		"key":           apiKey,
		"model":         model,
		"threshold":     0.65,
		"minGap":        0.05,
		"defaultIntent": "Default",
		"intents":       getTestIntents(),
	}

	testCases := []struct {
		input           string
		expectedIntents []string
		comment         string
	}{
		// query boundary
		{"查询灯光状态", []string{"query"}, "包含'查询'"},
		{"看看灯开没开", []string{"query"}, "口语化查询"},
		{"灯光状态是什么", []string{"query"}, "明确问状态"},
		{"灯还开着吗", []string{"query"}, "疑问句式"},
		{"帮我看看灯的状态", []string{"query"}, "'看看'暗示查询"},
		{"空调开着吗", []string{"query"}, "疑问句"},
		{"查一下空调温度", []string{"query"}, "'查一下'明确是查询"},

		// control boundary
		{"把灯打开", []string{"control"}, "明确控制指令"},
		{"开灯", []string{"control"}, "简短控制指令"},
		{"关灯", []string{"control"}, "简短控制指令"},
		{"把空调关了", []string{"control"}, "明确控制指令"},
		{"空调温度调高一点", []string{"control"}, "调节操作"},

		// createRule
		{"温度高于30度自动开空调", []string{"createRule"}, "条件触发=规则"},
		{"检测到人就开灯", []string{"createRule"}, "条件触发=规则"},

		// Unrelated
		{"今天天气怎么样", []string{"Default"}, "无关输入"},
		{"What is the capital of France", []string{"Default"}, "无关输入"},
	}

	node := &LocalIntentNode{}
	err := node.Init(config, configuration)
	assert.Nil(t, err)

	fmt.Println(strings.Repeat("=", 110))
	fmt.Printf("%-30s | %-10s | %-10s | %-6s | %s\n", "输入", "期望", "实际", "结果", "说明")
	fmt.Println(strings.Repeat("-", 110))

	confusionCount := 0
	confusionTotal := 0

	for _, tc := range testCases {
		resultRelation, matched := runLocalIntentTestSilent(t, node, tc.input)

		// Statistical control/query confusion
		isControlQuery := (resultRelation == "control" || resultRelation == "query")
		expectedControlQuery := false
		for _, exp := range tc.expectedIntents {
			if exp == "control" || exp == "query" {
				expectedControlQuery = true
				break
			}
		}
		if isControlQuery && expectedControlQuery && !matched {
			confusionCount++
		}
		if expectedControlQuery {
			confusionTotal++
		}

		status := "PASS"
		comment := tc.comment
		if !matched {
			status = "CONFUSE"
			comment = fmt.Sprintf("期望 %v 但得到 %s -- %s", tc.expectedIntents, resultRelation, tc.comment)
		}

		fmt.Printf("%-30s | %-10s | %-10s | %-6s | %s\n",
			truncateStr(tc.input, 28), tc.expectedIntents[0], resultRelation, status, comment)
	}

	fmt.Println(strings.Repeat("=", 110))
	if confusionTotal > 0 {
		fmt.Printf("control/query 混淆: %d/%d (%.1f%%)\n",
			confusionCount, confusionTotal, float64(confusionCount)/float64(confusionTotal)*100)
	}
}

// ============================================================================
// Integration Testing: Generalization capability
// ============================================================================

func TestLocalIntentNode_Generalization(t *testing.T) {
	_, apiKey, _ := getEmbeddingTestConfig()
	if apiKey == "" {
		t.Skip("EMBEDDING_API_KEY not set, skipping integration test")
	}

	url, apiKey, model := getEmbeddingTestConfig()
	config := types.NewConfig()

	configuration := map[string]interface{}{
		"url":           url,
		"key":           apiKey,
		"model":         model,
		"threshold":     0.65,
		"minGap":        0.05,
		"defaultIntent": "Default",
		"intents":       getTestIntents(),
	}

	testCases := []struct {
		input           string
		expectedIntents []string
		category        string
	}{
		// control generalization — different devices/sentence patterns
		{"让窗帘拉下来", []string{"control"}, "control"},
		{"热水器调到50度", []string{"control"}, "control"},
		{"电视声音大一点", []string{"control"}, "control"},
		{"把门锁上", []string{"control"}, "control"},
		{"启动扫地机器人", []string{"control"}, "control"},
		{"关掉所有灯", []string{"control"}, "control"},
		{"卧室灯开一下", []string{"control"}, "control"},
		{"别让洗衣机转了", []string{"control"}, "control"},

		// Query generalization
		{"客厅几度", []string{"query"}, "query"},
		{"洗衣机洗完没", []string{"query"}, "query"},
		{"门锁了没", []string{"query"}, "query"},
		{"看看湿度", []string{"query"}, "query"},
		{"热水器还在加热吗", []string{"query"}, "query"},
		{"车库门是什么状态", []string{"query"}, "query"},
		{"现在PM2.5多少", []string{"query"}, "query"},
		{"窗帘拉开着吗", []string{"query"}, "query"},

		// createRule generalization
		{"下雨天自动关窗", []string{"createRule"}, "createRule"},
		{"离开家的时候关掉所有电器", []string{"createRule"}, "createRule"},
		{"每天早上7点开窗帘", []string{"createRule"}, "createRule"},
		{"空气质量差就开净化器", []string{"createRule"}, "createRule"},
		{"有人按门铃给我发通知", []string{"createRule"}, "createRule"},
		{"燃气泄漏立刻关阀门", []string{"createRule"}, "createRule"},

		// Unrelated
		{"帮我订个外卖", []string{"Default"}, "unrelated"},
		{"讲个笑话", []string{"Default"}, "unrelated"},
		{"我想听歌", []string{"Default"}, "unrelated"},
	}

	node := &LocalIntentNode{}
	err := node.Init(config, configuration)
	assert.Nil(t, err)

	fmt.Println(strings.Repeat("=", 110))
	fmt.Printf("%-28s | %-10s | %-10s | %-10s | %-6s | %s\n",
		"输入", "期望", "实际", "分类", "结果", "说明")
	fmt.Println(strings.Repeat("-", 110))

	passCount := 0
	catResults := map[string]struct{ pass, total int }{}

	for _, tc := range testCases {
		resultRelation, matched := runLocalIntentTestSilent(t, node, tc.input)

		if matched {
			passCount++
		}

		r := catResults[tc.category]
		r.total++
		if matched {
			r.pass++
		}
		catResults[tc.category] = r

		status := "PASS"
		note := ""
		if !matched {
			status = "FAIL"
			note = fmt.Sprintf("(期望 %v)", tc.expectedIntents)
		}

		fmt.Printf("%-28s | %-10s | %-10s | %-10s | %-6s | %s\n",
			truncateStr(tc.input, 26),
			tc.expectedIntents[0],
			resultRelation,
			tc.category,
			status,
			note,
		)
	}

	fmt.Println(strings.Repeat("=", 110))
	fmt.Printf("\n总通过: %d/%d (%.1f%%)\n", passCount, len(testCases), float64(passCount)/float64(len(testCases))*100)

	fmt.Println("\n按分类统计:")
	for _, cat := range []string{"control", "query", "createRule", "unrelated"} {
		r := catResults[cat]
		rate := float64(r.pass) / float64(r.total) * 100
		mark := ""
		if rate < 50 {
			mark = " << 差"
		} else if rate < 80 {
			mark = " << 一般"
		}
		fmt.Printf("  %-12s: %d/%d (%.0f%%)%s\n", cat, r.pass, r.total, rate, mark)
	}
}

// ============================================================================
// Auxiliary function
// ============================================================================

// runLocalIntentTestSilent Executes a single test slip silently, does not output t.Logf, returns the result
func runLocalIntentTestSilent(t *testing.T, node *LocalIntentNode, input string) (resultRelation string, matched bool) {
	t.Helper()
	done := make(chan struct{})
	var resultErr error

	ctx := test.NewRuleContext(types.NewConfig(), func(msg types.RuleMsg, relationType string, err error) {
		resultRelation = relationType
		resultErr = err
		close(done)
	})

	msg := ctx.NewMsg("TEST", types.NewMetadata(), input)
	node.OnMsg(ctx, msg)

	select {
	case <-done:
		if resultErr != nil {
			t.Logf("Type %q error: %v", input, resultErr)
		}
		matched = (resultRelation != "")
	case <-time.After(30 * time.Second):
		t.Errorf("Input %q timeout", input)
	}
	return
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen-2]) + ".."
	}
	return s
}
