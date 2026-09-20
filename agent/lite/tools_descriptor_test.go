package lite

import (
	"testing"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/utils/maps"
	"github.com/rulego/rulego-components-ai/config"
)

// tools 配置结构与 agent 包相同（config.Tool），展开后得到工具允许列表与技能目录。
func TestResolveTools(t *testing.T) {
	t.Run("字符串条目按名解析", func(t *testing.T) {
		n := &AgentLiteNode{config: AgentLiteConfig{
			Tools: []config.Tool{{Name: "list_rule_chains"}, {Name: "get_rule_chain"}},
		}}
		if err := n.resolveTools(); err != nil {
			t.Fatalf("resolveTools: %v", err)
		}
		if len(n.toolAllow) != 2 {
			t.Fatalf("允许列表应含 2 个工具名, 实得 %v", n.toolAllow)
		}
	})

	t.Run("mcp self 展开工具名", func(t *testing.T) {
		n := &AgentLiteNode{config: AgentLiteConfig{
			Tools: []config.Tool{{
				Type: config.ToolTypeMCP,
				Config: types.Configuration{
					"server": "self",
					"tools":  []interface{}{"get_rule_chain", "save_rule_chain"},
				},
			}},
		}}
		if err := n.resolveTools(); err != nil {
			t.Fatalf("resolveTools: %v", err)
		}
		if len(n.toolAllow) != 2 {
			t.Fatalf("允许列表应含 2 个工具名, 实得 %v", n.toolAllow)
		}
	})

	t.Run("mcp 远程 server 登记待连接", func(t *testing.T) {
		n := &AgentLiteNode{config: AgentLiteConfig{
			Tools: []config.Tool{{
				Type:   config.ToolTypeMCP,
				Config: types.Configuration{"server": "http://x"},
			}},
		}}
		if err := n.resolveTools(); err != nil {
			t.Fatalf("resolveTools: %v", err)
		}
		if len(n.remoteServers) != 1 || n.remoteServers[0] != "http://x" {
			t.Fatalf("远程 server 应登记原文待 Init 渲染,实得 %v", n.remoteServers)
		}
	})

	t.Run("允许列表含 * 表示全量开放", func(t *testing.T) {
		n := &AgentLiteNode{config: AgentLiteConfig{
			Tools: []config.Tool{{
				Type:   config.ToolTypeMCP,
				Config: types.Configuration{"server": "self", "tools": []interface{}{"*"}},
			}},
		}}
		if err := n.resolveTools(); err != nil {
			t.Fatalf("resolveTools: %v", err)
		}
		defs := []types.MCPToolDefinition{{Name: "get_rule_chain"}, {Name: "save_rule_chain"}}
		if got := filterTools(defs, n.toolAllow); len(got) != 2 {
			t.Fatalf("含 * 的允许列表应全量放行,实得 %v", got)
		}
		if !n.toolAllowed("get_rule_chain") {
			t.Fatal("执行层也应全量放行")
		}
	})

	t.Run("builtin skill 提供技能目录", func(t *testing.T) {
		n := &AgentLiteNode{config: AgentLiteConfig{
			Tools: []config.Tool{{
				Type: config.ToolTypeBuiltin,
				Name: "skill",
				Config: types.Configuration{
					"globalDirs": []interface{}{"${global.skill_path}"},
					"localDirs":  []interface{}{"${global.data_dir}/skills"},
				},
			}},
		}}
		if err := n.resolveTools(); err != nil {
			t.Fatalf("resolveTools: %v", err)
		}
		if n.config.SkillsDir != "${global.data_dir}/skills" {
			t.Fatalf("skillsDir 应取 localDirs[0], 实得 %q", n.config.SkillsDir)
		}
	})

	t.Run("rulechain 不支持", func(t *testing.T) {
		n := &AgentLiteNode{config: AgentLiteConfig{
			Tools: []config.Tool{{Type: config.ToolTypeRuleChain, TargetId: "x"}},
		}}
		if err := n.resolveTools(); err == nil {
			t.Fatal("rulechain 应报错")
		}
	})

	t.Run("字符串速记经 NormalizeToolsShorthand 解码", func(t *testing.T) {
		cfg := types.Configuration{
			"tools": []interface{}{"a", map[string]interface{}{"type": "", "name": "b"}},
		}
		config.NormalizeToolsShorthand(cfg)
		n := &AgentLiteNode{}
		if err := maps.Map2Struct(cfg, &n.config); err != nil {
			t.Fatalf("Map2Struct: %v", err)
		}
		if err := n.resolveTools(); err != nil {
			t.Fatalf("resolveTools: %v", err)
		}
		if len(n.toolAllow) != 2 {
			t.Fatalf("允许列表应含 2 个工具名, 实得 %v", n.toolAllow)
		}
	})
}
