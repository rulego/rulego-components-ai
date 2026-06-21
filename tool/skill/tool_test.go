package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSkillTool 测试技能工具的基本功能
func TestSkillTool(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "hello")
	err := os.MkdirAll(skillDir, 0755)
	assert.NoError(t, err)

	skillContent := `---
name: hello
description: Say hello
---
Hello world from skill!
`
	err = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644)
	assert.NoError(t, err)

	// Test NewTool with user dirs
	tTool, err := NewTool(Config{LocalDirs: []string{tmpDir}})
	assert.NoError(t, err)

	ctx := context.Background()

	// Test Info — 应返回稳定描述，不含具体技能列表
	info, err := tTool.Info(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "skill", info.Name)
	assert.NotContains(t, info.Desc, "hello")        // 技能名不应出现在 tool description 中
	assert.NotContains(t, info.Desc, "Say hello")    // 技能描述不应出现在 tool description 中
	assert.Contains(t, info.Desc, "skills_instructions") // 应包含使用说明

	// Test ListSkills — 应返回动态技能列表
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "hello")
	assert.Contains(t, skillsText, "Say hello")

	// Test InvokableRun
	invokable, ok := tTool.(tool.InvokableTool)
	assert.True(t, ok)

	input := `{"skill": "hello"}`
	output, err := invokable.InvokableRun(ctx, input)
	assert.NoError(t, err)

	// Eino skill output format check
	assert.Contains(t, output, "Base directory for this skill:")
	assert.Contains(t, output, "Hello world from skill!")
}

// TestMultiBackendBasic 测试 MultiBackend 基本功能
func TestMultiBackendBasic(t *testing.T) {
	// Setup: 创建两个目录
	globalDir := t.TempDir()
	userDir := t.TempDir()

	// 在全局目录创建技能
	globalSkillDir := filepath.Join(globalDir, "global_skill")
	err := os.MkdirAll(globalSkillDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte(`---
name: global_skill
description: A global skill
---
Global skill content
`), 0644)
	assert.NoError(t, err)

	// 在用户目录创建技能
	userSkillDir := filepath.Join(userDir, "user_skill")
	err = os.MkdirAll(userSkillDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(userSkillDir, "SKILL.md"), []byte(`---
name: user_skill
description: A user skill
---
User skill content
`), 0644)
	assert.NoError(t, err)

	// 使用 MultiBackend 创建工具
	tTool, err := NewTool(Config{
		LocalDirs:   []string{userDir},
		GlobalDirs: []string{globalDir},
	})
	assert.NoError(t, err)

	ctx := context.Background()

	// 通过 ListSkills 验证两个技能都被列出
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "global_skill")
	assert.Contains(t, skillsText, "user_skill")

	// 测试调用全局技能
	invokable, _ := tTool.(tool.InvokableTool)
	output, err := invokable.InvokableRun(ctx, `{"skill": "global_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "Global skill content")

	// 测试调用用户技能
	output, err = invokable.InvokableRun(ctx, `{"skill": "user_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "User skill content")
}

// TestMultiBackendPriority 测试技能优先级（用户技能覆盖全局同名技能）
func TestMultiBackendPriority(t *testing.T) {
	// Setup: 创建两个目录
	globalDir := t.TempDir()
	userDir := t.TempDir()

	// 在全局目录创建同名技能
	globalSkillDir := filepath.Join(globalDir, "common_skill")
	err := os.MkdirAll(globalSkillDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte(`---
name: common_skill
description: Common skill from global
---
This is GLOBAL version
`), 0644)
	assert.NoError(t, err)

	// 在用户目录创建同名技能
	userSkillDir := filepath.Join(userDir, "common_skill")
	err = os.MkdirAll(userSkillDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(userSkillDir, "SKILL.md"), []byte(`---
name: common_skill
description: Common skill from user
---
This is USER version
`), 0644)
	assert.NoError(t, err)

	// 使用 MultiBackend 创建工具，用户目录放在前面
	tTool, err := NewTool(Config{
		LocalDirs:   []string{userDir},
		GlobalDirs: []string{globalDir},
	})
	assert.NoError(t, err)

	ctx := context.Background()
	invokable, _ := tTool.(tool.InvokableTool)

	// 调用同名技能，应该返回用户版本（优先级高）
	output, err := invokable.InvokableRun(ctx, `{"skill": "common_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "USER version")
	assert.NotContains(t, output, "GLOBAL version")
}

// TestMultiBackendEmptyDirs 测试空目录配置
func TestMultiBackendEmptyDirs(t *testing.T) {
	// 创建临时目录用于默认路径
	tmpDir := t.TempDir()

	// 测试空配置 - 应该使用默认目录
	backend := NewMultiBackend([]string{tmpDir})
	ctx := context.Background()

	// 空目录应该返回空列表，不报错
	skills, err := backend.List(ctx)
	assert.NoError(t, err)
	assert.Empty(t, skills)
}

// TestMultiBackendMultipleGlobalDirs 测试多个全局目录
func TestMultiBackendMultipleGlobalDirs(t *testing.T) {
	// Setup: 创建三个目录
	globalDir1 := t.TempDir()
	globalDir2 := t.TempDir()
	userDir := t.TempDir()

	// 在第一个全局目录创建技能
	skillDir1 := filepath.Join(globalDir1, "skill1")
	err := os.MkdirAll(skillDir1, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(skillDir1, "SKILL.md"), []byte(`---
name: skill1
description: Skill from global dir 1
---
Content from global dir 1
`), 0644)
	assert.NoError(t, err)

	// 在第二个全局目录创建技能
	skillDir2 := filepath.Join(globalDir2, "skill2")
	err = os.MkdirAll(skillDir2, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(skillDir2, "SKILL.md"), []byte(`---
name: skill2
description: Skill from global dir 2
---
Content from global dir 2
`), 0644)
	assert.NoError(t, err)

	// 在用户目录创建技能
	userSkillDir := filepath.Join(userDir, "user_only")
	err = os.MkdirAll(userSkillDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(userSkillDir, "SKILL.md"), []byte(`---
name: user_only
description: User only skill
---
User only content
`), 0644)
	assert.NoError(t, err)

	// 使用多目录配置
	tTool, err := NewTool(Config{
		LocalDirs:   []string{userDir},
		GlobalDirs: []string{globalDir1, globalDir2},
	})
	assert.NoError(t, err)

	ctx := context.Background()

	// 通过 ListSkills 验证所有技能都被列出
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "skill1")
	assert.Contains(t, skillsText, "skill2")
	assert.Contains(t, skillsText, "user_only")

	// 测试调用
	invokable, _ := tTool.(tool.InvokableTool)

	output, err := invokable.InvokableRun(ctx, `{"skill": "skill1"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "global dir 1")

	output, err = invokable.InvokableRun(ctx, `{"skill": "skill2"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "global dir 2")

	output, err = invokable.InvokableRun(ctx, `{"skill": "user_only"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "User only content")
}

// TestNewTool_InjectedDefaultGlobalDirs 验证 SetDefaultGlobalSkillDirs 注入的默认目录
// 在 GlobalDirs 未配置时生效（供宿主把自身技能目录接入 agent 运行时）。
func TestNewTool_InjectedDefaultGlobalDirs(t *testing.T) {
	globalDir := t.TempDir()
	skillDir := filepath.Join(globalDir, "injected_skill")
	assert.NoError(t, os.MkdirAll(skillDir, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: injected_skill
description: from injected default dir
---
body
`), 0644))

	SetDefaultGlobalSkillDirs([]string{globalDir})
	defer SetDefaultGlobalSkillDirs(nil)

	// GlobalDirs 未配置 → 使用注入的默认目录
	tTool, err := NewTool(Config{})
	assert.NoError(t, err)

	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(context.Background())
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "injected_skill")
}

// TestMultiBackendMultipleLocalDirs 测试多个用户目录
func TestMultiBackendMultipleLocalDirs(t *testing.T) {
	// Setup: 创建多个用户目录
	userDir1 := t.TempDir()
	userDir2 := t.TempDir()

	// 在第一个用户目录创建技能
	skillDir1 := filepath.Join(userDir1, "user_skill1")
	err := os.MkdirAll(skillDir1, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(skillDir1, "SKILL.md"), []byte(`---
name: user_skill1
description: Skill from user dir 1
---
Content from user dir 1
`), 0644)
	assert.NoError(t, err)

	// 在第二个用户目录创建技能
	skillDir2 := filepath.Join(userDir2, "user_skill2")
	err = os.MkdirAll(skillDir2, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(skillDir2, "SKILL.md"), []byte(`---
name: user_skill2
description: Skill from user dir 2
---
Content from user dir 2
`), 0644)
	assert.NoError(t, err)

	// 使用多用户目录配置
	tTool, err := NewTool(Config{
		LocalDirs: []string{userDir1, userDir2},
	})
	assert.NoError(t, err)

	ctx := context.Background()

	// 通过 ListSkills 验证所有技能都被列出
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "user_skill1")
	assert.Contains(t, skillsText, "user_skill2")

	// 测试调用
	invokable, _ := tTool.(tool.InvokableTool)

	output, err := invokable.InvokableRun(ctx, `{"skill": "user_skill1"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "user dir 1")

	output, err = invokable.InvokableRun(ctx, `{"skill": "user_skill2"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "user dir 2")
}

// TestMultiBackendSkipNonExistentDir 测试跳过不存在的目录
func TestMultiBackendSkipNonExistentDir(t *testing.T) {
	// 创建一个存在的目录
	existingDir := t.TempDir()
	skillDir := filepath.Join(existingDir, "existing_skill")
	err := os.MkdirAll(skillDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: existing_skill
description: Existing skill
---
Existing content
`), 0644)
	assert.NoError(t, err)

	// 使用包含不存在目录的配置
	tTool, err := NewTool(Config{
		LocalDirs: []string{existingDir, "./non/existent/dir1", "./non/existent/dir2"},
	})
	assert.NoError(t, err)

	ctx := context.Background()

	// 通过 ListSkills 验证存在的目录中的技能被正确加载
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "existing_skill")

	// 测试调用
	invokable, _ := tTool.(tool.InvokableTool)
	output, err := invokable.InvokableRun(ctx, `{"skill": "existing_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "Existing content")
}

// TestDynamicSkillToolInfoStable 验证 Info() 返回稳定描述，不含具体技能列表
func TestDynamicSkillToolInfoStable(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "my_skill")
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: my_skill
description: My skill description
---
Content here
`), 0644))

	tTool, err := NewTool(Config{LocalDirs: []string{tmpDir}})
	require.NoError(t, err)

	ctx := context.Background()
	info, err := tTool.Info(ctx)
	require.NoError(t, err)

	// Info 应返回稳定描述
	assert.Equal(t, "skill", info.Name)
	assert.Contains(t, info.Desc, "skills_instructions")
	// 不应包含具体技能列表
	assert.NotContains(t, info.Desc, "my_skill")
	assert.NotContains(t, info.Desc, "My skill description")
	assert.NotContains(t, info.Desc, "<available_skills>")
}

// TestDynamicSkillToolListSkills 验证 ListSkills() 返回动态技能列表
func TestDynamicSkillToolListSkills(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建两个技能
	for _, name := range []string{"skill_a", "skill_b"} {
		skillDir := filepath.Join(tmpDir, name)
		require.NoError(t, os.MkdirAll(skillDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(fmt.Sprintf(`---
name: %s
description: Description of %s
---
Content of %s
`, name, name, name)), 0644))
	}

	tTool, err := NewTool(Config{LocalDirs: []string{tmpDir}})
	require.NoError(t, err)

	dst, ok := tTool.(*dynamicSkillTool)
	require.True(t, ok)

	ctx := context.Background()
	skillsText, err := dst.ListSkills(ctx)
	require.NoError(t, err)

	// 应包含 <available_skills> 格式
	assert.Contains(t, skillsText, "<available_skills>")
	assert.Contains(t, skillsText, "skill_a")
	assert.Contains(t, skillsText, "skill_b")
	assert.Contains(t, skillsText, "Description of skill_a")
	assert.Contains(t, skillsText, "Description of skill_b")
}

// TestDynamicSkillToolHotReload 验证热更新：运行时新增/修改/删除技能文件后 ListSkills 能感知变化
func TestDynamicSkillToolHotReload(t *testing.T) {
	tmpDir := t.TempDir()

	// 初始技能
	skillDir := filepath.Join(tmpDir, "original_skill")
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: original_skill
description: Original skill
---
Original content
`), 0644))

	tTool, err := NewTool(Config{LocalDirs: []string{tmpDir}})
	require.NoError(t, err)

	dst := tTool.(*dynamicSkillTool)
	ctx := context.Background()

	// 1. 初始状态：只有 original_skill
	skillsText, err := dst.ListSkills(ctx)
	require.NoError(t, err)
	assert.Contains(t, skillsText, "original_skill")
	assert.NotContains(t, skillsText, "new_skill")

	// 2. 新增技能文件
	time.Sleep(10 * time.Millisecond) // 确保修改时间不同
	newSkillDir := filepath.Join(tmpDir, "new_skill")
	require.NoError(t, os.MkdirAll(newSkillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(newSkillDir, "SKILL.md"), []byte(`---
name: new_skill
description: New skill added at runtime
---
New content
`), 0644))

	skillsText, err = dst.ListSkills(ctx)
	require.NoError(t, err)
	assert.Contains(t, skillsText, "original_skill")
	assert.Contains(t, skillsText, "new_skill")

	// 3. 修改已有技能内容
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: original_skill
description: Updated description
---
Updated content
`), 0644))

	skillsText, err = dst.ListSkills(ctx)
	require.NoError(t, err)
	assert.Contains(t, skillsText, "Updated description")
	assert.NotContains(t, skillsText, "Original skill")

	// 4. 删除技能
	require.NoError(t, os.RemoveAll(newSkillDir))
	skillsText, err = dst.ListSkills(ctx)
	require.NoError(t, err)
	assert.Contains(t, skillsText, "original_skill")
	assert.NotContains(t, skillsText, "new_skill")
}

// TestDynamicSkillToolGetSkillInstruction 验证 GetSkillInstruction 返回技能系统使用说明
func TestDynamicSkillToolGetSkillInstruction(t *testing.T) {
	tmpDir := t.TempDir()
	tTool, err := NewTool(Config{LocalDirs: []string{tmpDir}})
	require.NoError(t, err)

	dst, ok := tTool.(*dynamicSkillTool)
	require.True(t, ok)

	instruction := dst.GetSkillInstruction()
	assert.NotEmpty(t, instruction)
	assert.Contains(t, instruction, "Skill")
}

// TestRenderSkillList 验证 renderSkillList 边界情况
func TestRenderSkillList(t *testing.T) {
	// 空列表
	result, err := renderSkillList(nil)
	assert.NoError(t, err)
	assert.Empty(t, result)

	// 单个技能
	result, err = renderSkillList([]einoskill.FrontMatter{
		{Name: "test", Description: "Test skill"},
	})
	assert.NoError(t, err)
	assert.Contains(t, result, "<available_skills>")
	assert.Contains(t, result, "test")
	assert.Contains(t, result, "Test skill")

	// 多个技能
	result, err = renderSkillList([]einoskill.FrontMatter{
		{Name: "a", Description: "Skill A"},
		{Name: "b", Description: "Skill B"},
	})
	assert.NoError(t, err)
	assert.Contains(t, result, "a")
	assert.Contains(t, result, "Skill A")
	assert.Contains(t, result, "b")
	assert.Contains(t, result, "Skill B")
}
