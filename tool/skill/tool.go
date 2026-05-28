package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	aitool "github.com/rulego/rulego-components-ai/tool"
)

const (
	// DefaultSkillsPath 默认技能存储相对路径
	DefaultSkillsPath = ".agents/skills"
)

type Config struct {
	// GlobalDirs 全局技能目录列表，所有用户共享的技能
	GlobalDirs []string `json:"globalDirs" label:"全局技能目录" desc:"全局技能目录列表，所有用户共享的技能，多个目录用逗号分隔"`
	// LocalDirs 本地技能目录列表，当前智能体专属的技能，优先级高于全局技能
	LocalDirs []string `json:"localDirs" label:"本地技能目录" desc:"本地技能目录列表，当前智能体专属的技能，优先级高于全局技能"`
	// UseChinese controls prompt language
	UseChinese bool `json:"useChinese" label:"使用中文" desc:"是否使用中文提示"`
	// Backend allows providing a custom backend implementation.
	// If nil, a default backend using directories will be created.
	Backend einoskill.Backend `json:"-"`
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

		// 如果 globalDirs 未配置，添加默认目录 ~/.agents/skills
		if len(config.GlobalDirs) == 0 {
			if home, err := os.UserHomeDir(); err == nil {
				dirs = append(dirs, filepath.Join(home, DefaultSkillsPath))
			}
		}

		backend = NewMultiBackend(dirs)
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

	return middleware.AdditionalTools[0], nil
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
