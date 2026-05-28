package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
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

	// Test Info
	ctx := context.Background()
	info, err := tTool.Info(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "skill", info.Name)
	assert.Contains(t, info.Desc, "hello")
	assert.Contains(t, info.Desc, "Say hello")

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
	info, err := tTool.Info(ctx)
	assert.NoError(t, err)

	// 验证两个技能都被列出
	assert.Contains(t, info.Desc, "global_skill")
	assert.Contains(t, info.Desc, "user_skill")

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
	info, err := tTool.Info(ctx)
	assert.NoError(t, err)

	// 验证所有技能都被列出
	assert.Contains(t, info.Desc, "skill1")
	assert.Contains(t, info.Desc, "skill2")
	assert.Contains(t, info.Desc, "user_only")

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
	info, err := tTool.Info(ctx)
	assert.NoError(t, err)

	// 验证所有技能都被列出
	assert.Contains(t, info.Desc, "user_skill1")
	assert.Contains(t, info.Desc, "user_skill2")

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
	info, err := tTool.Info(ctx)
	assert.NoError(t, err)

	// 验证存在的目录中的技能被正确加载
	assert.Contains(t, info.Desc, "existing_skill")

	// 测试调用
	invokable, _ := tTool.(tool.InvokableTool)
	output, err := invokable.InvokableRun(ctx, `{"skill": "existing_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "Existing content")
}
