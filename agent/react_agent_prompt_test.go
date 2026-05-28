package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rulego/rulego/utils/el"
)

// TestPrintSystemPrompt 打印系统提示词，用于调试和测试
func TestPrintSystemPrompt(t *testing.T) {
	// 读取智能体配置文件
	agentConfigPath := "../../../evolver-app/data/agents/main.json"

	config, err := loadAgentConfigFromFile(agentConfigPath)
	if err != nil {
		t.Skipf("跳过测试: 无法加载配置文件 %s: %v", agentConfigPath, err)
		return
	}

	// 从配置中提取系统提示词
	systemPrompt := extractSystemPrompt(config)

	// 打印原始系统提示词
	fmt.Println("\n========== 原始 System Prompt ==========")
	fmt.Println(systemPrompt)
	fmt.Println("========================================")

	// 如果有变量，打印渲染后的系统提示词
	if systemPrompt != "" {
		tmpl, err := el.NewTemplate(systemPrompt)
		if err != nil {
			t.Errorf("创建模板失败: %v", err)
			return
		}

		if tmpl.HasVar() {
			fmt.Println("\n========== 模板包含变量 ==========")

			// 创建测试环境
			env := map[string]interface{}{
				"agentName":   "main",
				"agentId":     "main",
				"currentDate": "2026-02-28",
				"currentTime": "10:30:00",
				"userId":      "test-user",
				"threadId":    "test-thread",
				"metadata":    map[string]string{},
			}

			rendered := tmpl.ExecuteAsString(env)
			fmt.Println("\n========== 渲染后 System Prompt ==========")
			fmt.Println(rendered)
			fmt.Println("========================================")
		} else {
			fmt.Println("\n========== 模板无变量 ==========")
		}
	}
}

// TestPrintAllAgentsSystemPrompt 打印所有智能体的系统提示词
func TestPrintAllAgentsSystemPrompt(t *testing.T) {
	agentsDir := "../../../evolver-app/data/agents"

	agents, err := listAgentFiles(agentsDir)
	if err != nil {
		t.Skipf("跳过测试: 无法列出智能体目录 %s: %v", agentsDir, err)
		return
	}

	for _, agentFile := range agents {
		fmt.Printf("\n#################### 智能体: %s ####################\n", filepath.Base(agentFile))

		config, err := loadAgentConfigFromFile(agentFile)
		if err != nil {
			fmt.Printf("加载失败: %v\n", err)
			continue
		}

		systemPrompt := extractSystemPrompt(config)

		fmt.Println("---------- System Prompt ----------")
		if systemPrompt == "" {
			fmt.Println("(无系统提示词)")
		} else {
			fmt.Println(systemPrompt)
		}

		fmt.Println("--------------------------------------------------")
	}
}

// loadAgentConfigFromFile 从文件加载智能体配置
func loadAgentConfigFromFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return config, nil
}

// extractSystemPrompt 从配置中提取系统提示词
func extractSystemPrompt(config map[string]interface{}) string {
	// 尝试从 metadata.nodes[].configuration.systemPrompt 获取
	if metadata, ok := config["metadata"].(map[string]interface{}); ok {
		if nodes, ok := metadata["nodes"].([]interface{}); ok {
			for _, node := range nodes {
				if nodeMap, ok := node.(map[string]interface{}); ok {
					if configuration, ok := nodeMap["configuration"].(map[string]interface{}); ok {
						if sp, ok := configuration["systemPrompt"].(string); ok && sp != "" {
							return sp
						}
					}
				}
			}
		}
	}

	// 尝试从 ruleChain.additionalInfo.systemPrompt 获取
	if ruleChain, ok := config["ruleChain"].(map[string]interface{}); ok {
		if additionalInfo, ok := ruleChain["additionalInfo"].(map[string]interface{}); ok {
			if sp, ok := additionalInfo["systemPrompt"].(string); ok && sp != "" {
				return sp
			}
		}
	}

	return ""
}

// listAgentFiles 列出所有智能体配置文件
func listAgentFiles(dir string) ([]string, error) {
	var files []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	return files, nil
}

// TestPrintReactAgentDefaultPrompt 打印 React Agent 默认配置
func TestPrintReactAgentDefaultPrompt(t *testing.T) {
	node := &ReactAgentNode{}
	defaultNode := node.New().(*ReactAgentNode)

	fmt.Println("\n========== React Agent 默认配置 ==========")
	fmt.Printf("Temperature: %.2f\n", defaultNode.Config.Params.Temperature)
	fmt.Printf("TopP: %.2f\n", defaultNode.Config.Params.TopP)
	fmt.Printf("FrequencyPenalty: %.2f\n", defaultNode.Config.Params.FrequencyPenalty)
	fmt.Printf("PresencePenalty: %.2f\n", defaultNode.Config.Params.PresencePenalty)
	fmt.Printf("MaxStep: %d\n", defaultNode.Config.MaxStep)
	fmt.Println("==========================================")
}

// PrintSystemPromptFromNode 从 ReactAgentNode 打印系统提示词
// 可以在 OnMsg 方法开头调用此函数进行调试
func PrintSystemPromptFromNode(node *ReactAgentNode) {
	if node == nil {
		fmt.Println("(node 为空)")
		return
	}

	fmt.Println("\n========== 原始 System Prompt ==========")
	fmt.Println(node.Config.SystemPrompt)
	fmt.Println("========================================")

	// 打印预设消息
	if len(node.presetMessagesTmpls) > 0 {
		fmt.Println("\n========== 预设消息模板 ==========")
		for i, msgTmpl := range node.presetMessagesTmpls {
			fmt.Printf("[%d] Role: %s\n", i, msgTmpl.Role)
			fmt.Printf("    Template: (有模板)\n")
		}
		fmt.Println("========================================")
	}

	// 打印工具列表
	if len(node.Config.Tools) > 0 {
		fmt.Println("\n========== 工具列表 ==========")
		for i, tool := range node.Config.Tools {
			fmt.Printf("[%d] %s (%s)\n", i, tool.Name, tool.Type)
		}
		fmt.Println("========================================")
	}
}

// GetRenderedSystemPrompt 获取渲染后的系统提示词
func GetRenderedSystemPrompt(node *ReactAgentNode, env map[string]interface{}) string {
	if node == nil || node.Config.SystemPrompt == "" {
		return ""
	}

	if node.hasVar && node.systemPromptTemplate != nil {
		return node.systemPromptTemplate.ExecuteAsString(env)
	}

	return node.Config.SystemPrompt
}

// PrintSystemPromptSimple 简单打印系统提示词（用于快速调试）
// 使用方法：在 react_agent_node.go 的 OnMsg 方法开头添加：
//
//	agent.PrintSystemPromptSimple(x.Config.SystemPrompt)
func PrintSystemPromptSimple(systemPrompt string) {
	fmt.Println("\n========== System Prompt ==========")
	fmt.Println(systemPrompt)
	fmt.Println("====================================")
}

// PrintFullPromptDebug 打印完整的提示词调试信息
func PrintFullPromptDebug(messages []map[string]interface{}) {
	fmt.Println("\n╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    Messages Debug Info                          ║")
	fmt.Println("╠════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ 消息数量: %d\n", len(messages))

	for i, msg := range messages {
		fmt.Printf("╠════════════════════════════════════════════════════════════════╣\n")
		fmt.Printf("║ 消息 [%d]\n", i)
		if role, ok := msg["role"].(string); ok {
			fmt.Printf("║   Role: %s\n", role)
		}
		if content, ok := msg["content"].(string); ok && content != "" {
			// 限制显示长度
			if len(content) > 500 {
				content = content[:500] + "...(截断)"
			}
			fmt.Printf("║   Content:\n")
			fmt.Printf("║   %s\n", content)
		}
	}
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
}
