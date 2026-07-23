package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	aitool "github.com/rulego/rulego-components-ai/tool"
)

const (
	// DefaultSkillsPath The default skill stores relative paths
	DefaultSkillsPath = ".agents/skills"
)

// defaultGlobalSkillDirs can be injected into the default global skill directory; Overrides the default ~/.agents/skills when not null.
// Allows the host to access its own skill directory into the agent runtime, making the skills managed by the host visible to the agent.
// You should call SetDefaultGlobalSkillDirs once during the process startup phase (before loading the agent).
var defaultGlobalSkillDirs []string

// SetDefaultGlobalSkillDirs overrides the default global skill directory when the agent is not configured with GlobalDirs.
// Send nil/empty slice to restore default behavior (~/.agents/skills).
func SetDefaultGlobalSkillDirs(dirs []string) {
	defaultGlobalSkillDirs = dirs
}

// The following constants are based on modifications from unexported constants in eino adk/middlewares/skill/prompt.go.
// Difference from the original Eino: Removed the <available_skills> dynamic skill list paragraph.
// Reason: The skill list is now dynamically injected into the system prompt by MessageModifier with each request,
// Prevents inconsistencies between static snapshots in tool descriptions and dynamic lists in system prompts.
// If eino updates the tool description format, synchronized maintenance is required here.

const toolDescBase = `Execute a skill within the main conversation

<skills_instructions>
When users ask you to perform tasks, check if any of the available skills below can help complete the task more effectively. Skills provide specialized capabilities and domain knowledge.

How to invoke:
- Use the exact string inside <name> tag as the skill name (no arguments)
- Examples:
  - ` + "`" + `skill: "pdf"` + "`" + ` - invoke the pdf skill
  - ` + "`" + `skill: "xlsx"` + "`" + ` - invoke the xlsx skill
  - ` + "`" + `skill: "ms-office-suite:pdf"` + "`" + ` - invoke using fully qualified name

Important:
- When a skill is relevant, you must invoke this tool IMMEDIATELY as your first action
- NEVER just announce or mention a skill in your text response without actually calling this tool
- This is a BLOCKING REQUIREMENT: invoke the relevant Skill tool BEFORE generating any other response about the task
- Do not invoke a skill that is already running
- Skill content may contain relative paths. Convert them to absolute paths using the base directory provided in the tool result
</skills_instructions>

`

const toolDescBaseChinese = `在主对话中执行 Skill（技能）

<skills_instructions>
当用户要求你执行任务时，检查下方可用 Skill 列表中是否有 Skill 可以更有效地完成任务。Skill 提供专业能力和领域知识。

如何调用：
- 使用 <name> 标签内的完整字符串作为 Skill 名称（无需其他参数）
- 示例：
  - ` + "`" + `skill: "pdf"` + "`" + ` - 调用 pdf Skill
  - ` + "`" + `skill: "xlsx"` + "`" + ` - 调用 xlsx Skill
  - ` + "`" + `skill: "ms-office-suite:pdf"` + "`" + ` - 使用完全限定名称调用

重要说明：
- 当 Skill 相关时，你必须立即调用此工具作为第一个动作
- 切勿仅在文本回复中提及 Skill 而不实际调用此工具
- 这是阻塞性要求：在生成任何关于任务的其他响应之前，先调用相关的 Skill 工具
- 仅使用系统提示词中 <available_skills> 列出的 Skill
- 不要调用已经运行中的 Skill
- Skill 内容中可能包含相对路径，需使用工具返回的 base directory 将其转换为绝对路径
</skills_instructions>

`

// skillListTmpl is a skill list template, which matches the toolDescriptionTemplate format in eino prompt.go
var skillListTmpl = template.Must(template.New("skills").Parse(`
<available_skills>
{{- range . }}
<skill>
<name>
{{ .Name }}
</name>
<description>
{{ .Description }}
</description>
</skill>
{{- end }}
</available_skills>
`))

type Config struct {
	// GlobalDirs global skill catalog list, skills shared by all users
	GlobalDirs []string `json:"globalDirs" label:"全局技能目录" desc:"全局技能目录列表，所有用户共享的技能，多个目录用逗号分隔"`
	// LocalDirs Local Skill Directory List, currently exclusive to the agent, with priority over global skills
	LocalDirs []string `json:"localDirs" label:"本地技能目录" desc:"本地技能目录列表，当前智能体专属的技能，优先级高于全局技能"`
	// List of disabled skill names in DisabledSkills, effective only for the current agent
	DisabledSkills []string `json:"disabledSkills" label:"禁用的技能" desc:"该智能体禁用的技能名称列表，仅对当前智能体生效"`
	// UseChinese controls prompt language
	UseChinese bool `json:"useChinese" label:"使用中文" desc:"是否使用中文提示"`
	// Backend allows providing a custom backend implementation.
	// If nil, a default backend using directories will be created.
	Backend einoskill.Backend `json:"-"`
}

// dynamicSkillTool wraps eino skillTool and supports dynamic skill list hot updates.
// Return a stable tool description (excluding specific skill lists) by overwriting Info(),
// The skill list is now dynamically injected by MessageModifier into the system prompt at each request.
type dynamicSkillTool struct {
	tool.BaseTool                   // Embedding eino skillTool (executed via InvokableRun)
	backend       einoskill.Backend // MultiBackend references and ListSkills are called to trigger fingerprint checks
	toolName      string            // Tool name
	useChinese    bool              // Is it in Chinese?
	instruction   string            // eino middleware's AdditionalInstruction (skill system usage guide)
}

// Info Override: Returns a stable description without a specific skill list.
// The original skillTool.Info() embeds a snapshot of the skill list at initialization in Desc,
// If not overridden, genToolInfos() will bind the snapshot to the chat model, causing the two lists to be inconsistent.
func (d *dynamicSkillTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	descBase := toolDescBase
	paramDesc := "The skill name (no arguments). E.g., \"pdf\" or \"xlsx\""
	if d.useChinese {
		descBase = toolDescBaseChinese
		paramDesc = "技能名称（无需其他参数）。例如：\"pdf\" 或 \"xlsx\""
	}
	return &schema.ToolInfo{
		Name: d.toolName,
		Desc: descBase,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"skill": {
				Type:     schema.String,
				Desc:     paramDesc,
				Required: true,
			},
		}),
	}, nil
}

// ListSkills retrieves the currently available skill list and renders it <available_skills> as formatted text.
// Each call is made through the backend.List() triggers MultiBackend fingerprint checks to enable hot updates.
func (d *dynamicSkillTool) ListSkills(ctx context.Context) (string, error) {
	skills, err := d.backend.List(ctx)
	if err != nil {
		return "", err
	}
	return renderSkillList(skills)
}

// GetSkillInstruction returns instructions for using the skill system.
func (d *dynamicSkillTool) GetSkillInstruction() string {
	return d.instruction
}

// InvokableRun delegates the skill to the embedded eino skillTool to execute the skill.
// Embedding tool.BaseTool interfaces does not improve the method of InvokableTool; it requires explicit delegation.
func (d *dynamicSkillTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	invokable, ok := d.BaseTool.(tool.InvokableTool)
	if !ok {
		return "", fmt.Errorf("underlying skill tool does not support InvokableRun")
	}
	return invokable.InvokableRun(ctx, argumentsInJSON, opts...)
}

// renderSkillList renders the skill list in <available_skills> XML format.
// The format matches the toolDescriptionTemplate in eino prompt.go.
func renderSkillList(skills []einoskill.FrontMatter) (string, error) {
	if len(skills) == 0 {
		return "", nil
	}
	var buf bytes.Buffer
	if err := skillListTmpl.Execute(&buf, skills); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// NewTool creates a new skill tool
func NewTool(config Config) (tool.BaseTool, error) {
	var backend einoskill.Backend = config.Backend
	if backend == nil {
		// Collect all directories: prioritize LocalDirs > GlobalDirs > default directory
		var dirs []string

		// Add local directories (high priority, placed at the front)
		dirs = append(dirs, config.LocalDirs...)

		// Add a global directory
		dirs = append(dirs, config.GlobalDirs...)

		// If globalDirs is not configured, prioritize using the default directory for injection; otherwise, rollback ~/.agents/skills
		if len(config.GlobalDirs) == 0 {
			if len(defaultGlobalSkillDirs) > 0 {
				dirs = append(dirs, defaultGlobalSkillDirs...)
			} else if home, err := os.UserHomeDir(); err == nil {
				dirs = append(dirs, filepath.Join(home, DefaultSkillsPath))
			}
		}

		mb := NewMultiBackend(dirs)
		mb.SetDisabledSkills(config.DisabledSkills)
		backend = mb
	}

	// Create Eino skill middleware to get the tool
	einoConfig := &einoskill.Config{
		Backend:    backend,
		UseChinese: config.UseChinese,
	}

	// We use a dummy context as New only uses it for construction if needed
	middleware, err := einoskill.New(context.Background(), einoConfig)
	if err != nil {
		return nil, err
	}

	if len(middleware.AdditionalTools) == 0 {
		return nil, fmt.Errorf("failed to create skill tool: no tool returned from middleware")
	}

	return &dynamicSkillTool{
		BaseTool:    middleware.AdditionalTools[0],
		backend:     backend,
		toolName:    "skill",
		useChinese:  config.UseChinese,
		instruction: middleware.AdditionalInstruction,
	}, nil
}

// Register a skill tool
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault: Registers the default configuration of skill tools
func RegisterDefault() error {
	return aitool.Registry.RegisterDef(aitool.ToolDefinition{
		Name:   "skill",
		Desc:   "技能调用工具 - 调用预定义的技能文件，支持从目录加载和执行技能",
		Config: &Config{},
		Factory: func(c map[string]interface{}) (tool.BaseTool, error) {
			var cfg Config
			b, _ := json.Marshal(c)
			if err := json.Unmarshal(b, &cfg); err != nil {
				return nil, err
			}
			return NewTool(cfg)
		},
	})
}

func init() {
	_ = RegisterDefault()
}
