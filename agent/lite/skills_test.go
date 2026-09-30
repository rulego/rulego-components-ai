package lite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rulego/rulego/api/types"
)

// loadSkills:enabled:false 的技能不加载,缺省视为启用。
func TestLoadSkillsEnabled(t *testing.T) {
	dir := t.TempDir()
	write := func(dirName, front, body string) {
		d := filepath.Join(dir, dirName)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(front+"\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("on", "---\nname: 巡检\nenabled: true\n---\n", "巡检流程全文")
	write("off", "---\nname: 下线技能\nenabled: false\n---\n", "不应出现")
	write("noflag", "---\nname: 默认启用\n---\n", "无 enabled 字段")

	skills := loadSkills(dir)
	if len(skills) != 2 {
		t.Fatalf("应加载 2 个技能,得到 %d: %+v", len(skills), skills)
	}
	names := map[string]bool{skills[0].Name: true, skills[1].Name: true}
	if !names["巡检"] || !names["默认启用"] {
		t.Fatalf("技能清单不符(下线技能不应出现): %+v", skills)
	}
}

// 技能目录只认 SKILL.md;仅含 DRAFT.md 的目录(待审草稿)不产生技能。
func TestLoadSkillsSkipsDraft(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "draft-only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "draft-only", "DRAFT.md"),
		[]byte("---\nname: 草稿\n---\n草稿内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if skills := loadSkills(dir); len(skills) != 0 {
		t.Fatalf("仅含 DRAFT.md 的目录不应加载技能: %+v", skills)
	}
}

// filterTools:允许列表为空不过滤;非空只保留列表内工具。
func TestFilterTools(t *testing.T) {
	defs := []types.MCPToolDefinition{
		{Name: "list_devices"},
		{Name: "ack_alarm"},
		{Name: "consult_agent_x"},
	}
	if got := filterTools(defs, nil); len(got) != 3 {
		t.Fatalf("空允许列表应全量保留: %d", len(got))
	}
	got := filterTools(defs, []string{"ack_alarm", "consult_agent_x"})
	if len(got) != 2 || got[0].Name != "ack_alarm" || got[1].Name != "consult_agent_x" {
		t.Fatalf("过滤结果不符: %+v", got)
	}
	if got := filterTools(defs, []string{"nope"}); len(got) != 0 {
		t.Fatalf("全不匹配应为空: %d", len(got))
	}
}
