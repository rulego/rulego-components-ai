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

// TestSkillTool Basic features of the testing skill tool
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

	// Test Info — should return a stable description without a specific skill list
	info, err := tTool.Info(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "skill", info.Name)
	assert.NotContains(t, info.Desc, "hello")            // Skill names should not appear in the tool description
	assert.NotContains(t, info.Desc, "Say hello")        // Skill descriptions should not appear in the tool description
	assert.Contains(t, info.Desc, "skills_instructions") // Instructions for use should be included

	// Test ListSkills — should return a dynamic skill list
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

// TestMultiBackendBasic Tests the basic functions of MultiBackend
func TestMultiBackendBasic(t *testing.T) {
	// Setup: Create two directories
	globalDir := t.TempDir()
	userDir := t.TempDir()

	// Create skills in the global directory
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

	// Create skills in the user directory
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

	// Create tools using MultiBackend
	tTool, err := NewTool(Config{
		LocalDirs:  []string{userDir},
		GlobalDirs: []string{globalDir},
	})
	assert.NoError(t, err)

	ctx := context.Background()

	// Verify with ListSkills that both skills are listed
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "global_skill")
	assert.Contains(t, skillsText, "user_skill")

	// Test calls global skills
	invokable, _ := tTool.(tool.InvokableTool)
	output, err := invokable.InvokableRun(ctx, `{"skill": "global_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "Global skill content")

	// Test user skills
	output, err = invokable.InvokableRun(ctx, `{"skill": "user_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "User skill content")
}

// TestMultiBackendPriority (user skills cover global skills with the same name)
func TestMultiBackendPriority(t *testing.T) {
	// Setup: Create two directories
	globalDir := t.TempDir()
	userDir := t.TempDir()

	// Create skills with the same name in the global directory
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

	// Create skills with the same name in the user directory
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

	// Use MultiBackend to create tools, placing the user directory at the front
	tTool, err := NewTool(Config{
		LocalDirs:  []string{userDir},
		GlobalDirs: []string{globalDir},
	})
	assert.NoError(t, err)

	ctx := context.Background()
	invokable, _ := tTool.(tool.InvokableTool)

	// When calling skills with the same name, it should return to the user version (high priority).
	output, err := invokable.InvokableRun(ctx, `{"skill": "common_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "USER version")
	assert.NotContains(t, output, "GLOBAL version")
}

// TestMultiBackendEmptyDirs tests the configuration of empty directories
func TestMultiBackendEmptyDirs(t *testing.T) {
	// Create temporary directories for default paths
	tmpDir := t.TempDir()

	// Test empty configurations - the default directory should be used
	backend := NewMultiBackend([]string{tmpDir})
	ctx := context.Background()

	// An empty directory should return an empty list without errors
	skills, err := backend.List(ctx)
	assert.NoError(t, err)
	assert.Empty(t, skills)
}

// TestMultiBackendMultipleGlobalDirs tests multiple global directories
func TestMultiBackendMultipleGlobalDirs(t *testing.T) {
	// Setup: Create three directories
	globalDir1 := t.TempDir()
	globalDir2 := t.TempDir()
	userDir := t.TempDir()

	// Create skills in the first global directory
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

	// Create skills in the second global directory
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

	// Create skills in the user directory
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

	// Use multi-directory configuration
	tTool, err := NewTool(Config{
		LocalDirs:  []string{userDir},
		GlobalDirs: []string{globalDir1, globalDir2},
	})
	assert.NoError(t, err)

	ctx := context.Background()

	// Verify all skills with ListSkills when all skills are listed
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "skill1")
	assert.Contains(t, skillsText, "skill2")
	assert.Contains(t, skillsText, "user_only")

	// Test call
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

// TestNewTool_InjectedDefaultGlobalDirs Verify the default directory injected by SetDefaultGlobalSkillDirs
// Effective when GlobalDirs is not configured (allowing the host to connect their skill directory to the agent during runtime).
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

	// GlobalDirs does not configure→ uses the injected default directory
	tTool, err := NewTool(Config{})
	assert.NoError(t, err)

	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(context.Background())
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "injected_skill")
}

// TestMultiBackendMultipleLocalDirs tests multiple user directories
func TestMultiBackendMultipleLocalDirs(t *testing.T) {
	// Setup: Create multiple user directories
	userDir1 := t.TempDir()
	userDir2 := t.TempDir()

	// Create skills in the first user directory
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

	// Create skills in the second user directory
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

	// Configure using multi-user directories
	tTool, err := NewTool(Config{
		LocalDirs: []string{userDir1, userDir2},
	})
	assert.NoError(t, err)

	ctx := context.Background()

	// Verify all skills with ListSkills when all skills are listed
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "user_skill1")
	assert.Contains(t, skillsText, "user_skill2")

	// Test call
	invokable, _ := tTool.(tool.InvokableTool)

	output, err := invokable.InvokableRun(ctx, `{"skill": "user_skill1"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "user dir 1")

	output, err = invokable.InvokableRun(ctx, `{"skill": "user_skill2"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "user dir 2")
}

// TestMultiBackendSkipNonExistentDir tests skipping directories that do not exist
func TestMultiBackendSkipNonExistentDir(t *testing.T) {
	// Create a catalog that exists
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

	// Use a configuration that includes directories that do not exist
	tTool, err := NewTool(Config{
		LocalDirs: []string{existingDir, "./non/existent/dir1", "./non/existent/dir2"},
	})
	assert.NoError(t, err)

	ctx := context.Background()

	// ListSkills verifies that skills in the existing directory are loaded correctly
	dst, ok := tTool.(*dynamicSkillTool)
	assert.True(t, ok)
	skillsText, err := dst.ListSkills(ctx)
	assert.NoError(t, err)
	assert.Contains(t, skillsText, "existing_skill")

	// Test call
	invokable, _ := tTool.(tool.InvokableTool)
	output, err := invokable.InvokableRun(ctx, `{"skill": "existing_skill"}`)
	assert.NoError(t, err)
	assert.Contains(t, output, "Existing content")
}

// TestDynamicSkillToolInfoStable Verifies Info() returns a stable description without a specific skill list
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

	// The Info should return a stable description
	assert.Equal(t, "skill", info.Name)
	assert.Contains(t, info.Desc, "skills_instructions")
	// Specific skill lists should not be included
	assert.NotContains(t, info.Desc, "my_skill")
	assert.NotContains(t, info.Desc, "My skill description")
	assert.NotContains(t, info.Desc, "<available_skills>")
}

// TestDynamicSkillToolListSkills Verifies ListSkills() returns a dynamic skill list
func TestDynamicSkillToolListSkills(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two skills
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

	// should include <available_skills> the format
	assert.Contains(t, skillsText, "<available_skills>")
	assert.Contains(t, skillsText, "skill_a")
	assert.Contains(t, skillsText, "skill_b")
	assert.Contains(t, skillsText, "Description of skill_a")
	assert.Contains(t, skillsText, "Description of skill_b")
}

// TestDynamicSkillToolHotReload verifies hot updates: After adding/modifying/deleting skill files during runtime, ListSkills can detect changes
func TestDynamicSkillToolHotReload(t *testing.T) {
	tmpDir := t.TempDir()

	// Starting skills
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

	// 1. Initial state: Only original_skill
	skillsText, err := dst.ListSkills(ctx)
	require.NoError(t, err)
	assert.Contains(t, skillsText, "original_skill")
	assert.NotContains(t, skillsText, "new_skill")

	// 2. Added skill files
	time.Sleep(10 * time.Millisecond) // Make sure the modification times are different
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

	// 3. Modify existing skill content
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

	// 4. Removal of skills
	require.NoError(t, os.RemoveAll(newSkillDir))
	skillsText, err = dst.ListSkills(ctx)
	require.NoError(t, err)
	assert.Contains(t, skillsText, "original_skill")
	assert.NotContains(t, skillsText, "new_skill")
}

// TestDynamicSkillToolGetSkillInstruction Verifies GetSkillInstruction Returns the skill system usage instructions
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

// TestRenderSkillList verifies the boundary status of renderSkillList
func TestRenderSkillList(t *testing.T) {
	// Empty list
	result, err := renderSkillList(nil)
	assert.NoError(t, err)
	assert.Empty(t, result)

	// Single skills
	result, err = renderSkillList([]einoskill.FrontMatter{
		{Name: "test", Description: "Test skill"},
	})
	assert.NoError(t, err)
	assert.Contains(t, result, "<available_skills>")
	assert.Contains(t, result, "test")
	assert.Contains(t, result, "Test skill")

	// Multiple skills
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
