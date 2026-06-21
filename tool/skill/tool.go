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
	// DefaultSkillsPath 默认技能存储相对路径
	DefaultSkillsPath = ".agents/skills"
)

// defaultGlobalSkillDirs 可注入的默认全局技能目录；非空时覆盖默认的 ~/.agents/skills。
// 供宿主应用把自身技能目录接入 agent 运行时，使宿主管理的技能对 agent 可见。
// 应在进程启动阶段（加载 agent 之前）调用 SetDefaultGlobalSkillDirs 设置一次。
var defaultGlobalSkillDirs []string

// SetDefaultGlobalSkillDirs 覆盖 agent 未配置 GlobalDirs 时的默认全局技能目录。
// 传 nil/空切片恢复默认行为（~/.agents/skills）。
func SetDefaultGlobalSkillDirs(dirs []string) {
	defaultGlobalSkillDirs = dirs
}

// 以下常量基于 eino adk/middlewares/skill/prompt.go 中的未导出常量修改。
// 与 eino 原版的区别：去掉了 <available_skills> 动态技能列表段落。
// 原因：技能列表改由 MessageModifier 在每次请求时动态注入 system prompt，
// 避免 tool description 中的静态快照与 system prompt 中的动态列表不一致。
// 如果 eino 更新了工具描述格式，此处需要同步维护。

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

// skillListTmpl 技能列表模板，与 eino prompt.go 中 toolDescriptionTemplate 格式一致
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
	// GlobalDirs 全局技能目录列表，所有用户共享的技能
	GlobalDirs []string `json:"globalDirs" label:"全局技能目录" desc:"全局技能目录列表，所有用户共享的技能，多个目录用逗号分隔"`
	// LocalDirs 本地技能目录列表，当前智能体专属的技能，优先级高于全局技能
	LocalDirs []string `json:"localDirs" label:"本地技能目录" desc:"本地技能目录列表，当前智能体专属的技能，优先级高于全局技能"`
	// DisabledSkills 禁用的技能名称列表，仅对当前智能体生效
	DisabledSkills []string `json:"disabledSkills" label:"禁用的技能" desc:"该智能体禁用的技能名称列表，仅对当前智能体生效"`
	// UseChinese controls prompt language
	UseChinese bool `json:"useChinese" label:"使用中文" desc:"是否使用中文提示"`
	// Backend allows providing a custom backend implementation.
	// If nil, a default backend using directories will be created.
	Backend einoskill.Backend `json:"-"`
}

// dynamicSkillTool 包装 eino skillTool，支持动态技能列表热更新。
// 通过覆写 Info() 返回稳定的 tool description（不含具体技能列表），
// 技能列表改由 MessageModifier 在每次请求时动态注入 system prompt。
type dynamicSkillTool struct {
	tool.BaseTool                   // 嵌入 eino skillTool（通过 InvokableRun 委托执行）
	backend     einoskill.Backend   // MultiBackend 引用，ListSkills 时调用以触发指纹检查
	toolName    string              // 工具名称
	useChinese  bool                // 是否使用中文
	instruction string              // eino middleware 的 AdditionalInstruction（技能系统使用说明）
}

// Info 覆写：返回稳定描述，不含具体技能列表。
// 原始 skillTool.Info() 会在 Desc 中嵌入初始化时的技能列表快照，
// 如果不覆写，genToolInfos() 会把快照绑定到 chat model，导致两份列表不一致。
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

// ListSkills 获取当前可用技能列表，渲染为 <available_skills> 格式文本。
// 每次调用通过 backend.List() 触发 MultiBackend 指纹检查，实现热更新。
func (d *dynamicSkillTool) ListSkills(ctx context.Context) (string, error) {
	skills, err := d.backend.List(ctx)
	if err != nil {
		return "", err
	}
	return renderSkillList(skills)
}

// GetSkillInstruction 返回技能系统的使用说明。
func (d *dynamicSkillTool) GetSkillInstruction() string {
	return d.instruction
}

// InvokableRun 委托给嵌入的 eino skillTool 执行技能。
// 嵌入 tool.BaseTool 接口不会提升 InvokableTool 的方法，需要显式委托。
func (d *dynamicSkillTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	invokable, ok := d.BaseTool.(tool.InvokableTool)
	if !ok {
		return "", fmt.Errorf("underlying skill tool does not support InvokableRun")
	}
	return invokable.InvokableRun(ctx, argumentsInJSON, opts...)
}

// renderSkillList 将技能列表渲染为 <available_skills> XML 格式。
// 格式与 eino prompt.go 中 toolDescriptionTemplate 一致。
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

// NewTool 创建一个新的技能工具
func NewTool(config Config) (tool.BaseTool, error) {
	var backend einoskill.Backend = config.Backend
	if backend == nil {
		// 收集所有目录：优先级为 LocalDirs > GlobalDirs > 默认目录
		var dirs []string

		// 添加本地目录（高优先级，放在前面）
		dirs = append(dirs, config.LocalDirs...)

		// 添加全局目录
		dirs = append(dirs, config.GlobalDirs...)

		// 如果 globalDirs 未配置，优先用注入的默认目录，否则回退 ~/.agents/skills
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

// Register 注册一个技能工具
func Register(config Config) error {
	t, err := NewTool(config)
	if err != nil {
		return err
	}
	return aitool.Registry.Register(t)
}

// RegisterDefault 注册默认配置的技能工具
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
