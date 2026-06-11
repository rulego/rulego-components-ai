package tool

import (
	"context"
	"reflect"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/utils/maps"
	rulegoReflect "github.com/rulego/rulego/utils/reflect"
)

// DynamicSkillLister 支持动态获取技能列表的工具接口。
// 实现此接口的工具会在每次请求时通过 MessageModifier 将最新技能列表注入 system prompt，
// 而不是在初始化时将技能列表固化到 tool description 中。
type DynamicSkillLister interface {
	tool.BaseTool
	// ListSkills 获取当前可用技能列表，渲染为可注入 system prompt 的文本。
	// 每次调用应检查底层数据源是否有变化（如文件指纹）。
	ListSkills(ctx context.Context) (string, error)
	// GetSkillInstruction 返回技能系统的使用说明（如"如何使用 Skill"的指引文本）。
	GetSkillInstruction() string
}

// GetToolFromConfig 从 rulego Config 中获取 AI 工具
func GetToolFromConfig(c types.Config, name string) (tool.BaseTool, bool) {
	if t := c.GetUdf(name, types.AiTool); t != nil {
		if toolInstance, ok := t.(tool.BaseTool); ok {
			return toolInstance, true
		}
	}
	return nil, false
}

var (
	// Registry 全局工具注册表
	Registry = &ToolRegistry{
		tools: make(map[string]tool.BaseTool),
		defs:  make(map[string]ToolDefinition),
	}
)

// ToolFactory 工具工厂
type ToolFactory func(config map[string]interface{}) (tool.BaseTool, error)

// ToolDefinition 工具定义
type ToolDefinition struct {
	Name string
	Desc string
	// Config struct for form generation (similar to Node)
	Config  interface{}
	Factory ToolFactory
	// Basic instance for backward compatibility or info
	Instance tool.BaseTool
}

// RegisterTool simplifies tool registration with a generic config type.
// Usage:
//
//	func init() {
//	    _ = tool.RegisterTool("bash", "Shell executor", DefaultConfig(), NewTool)
//	}
func RegisterTool[T any](name, desc string, defaultCfg T, factory func(T) (tool.BaseTool, error)) error {
	def := ToolDefinition{
		Name:   name,
		Desc:   desc,
		Config: defaultCfg,
		Factory: func(m map[string]interface{}) (tool.BaseTool, error) {
			var cfg T
			if err := maps.Map2Struct(m, &cfg); err != nil {
				return nil, err
			}
			return factory(cfg)
		},
	}

	// Create default instance
	instance, err := factory(defaultCfg)
	if err != nil {
		return err
	}
	def.Instance = instance

	return Registry.RegisterDef(def)
}

// ToolRegistry 工具注册表
type ToolRegistry struct {
	tools map[string]tool.BaseTool
	defs  map[string]ToolDefinition
	mu    sync.RWMutex
}

// Register 注册工具
func (r *ToolRegistry) Register(t tool.BaseTool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx := context.Background()
	info, err := t.Info(ctx)
	if err != nil {
		return err
	}
	r.tools[info.Name] = t
	return nil
}

// RegisterDef 注册工具定义
func (r *ToolRegistry) RegisterDef(def ToolDefinition) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if def.Instance != nil {
		ctx := context.Background()
		info, err := def.Instance.Info(ctx)
		if err != nil {
			return err
		}
		if def.Name == "" {
			def.Name = info.Name
		}
		r.tools[def.Name] = def.Instance
	}
	r.defs[def.Name] = def
	return nil
}

// Range 遍历工具
func (r *ToolRegistry) Range(f func(name string, t tool.BaseTool) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, t := range r.tools {
		if !f(name, t) {
			break
		}
	}
}

// RangeDef 遍历工具定义
func (r *ToolRegistry) RangeDef(f func(name string, def ToolDefinition) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, def := range r.defs {
		if !f(name, def) {
			break
		}
	}
}

// Get 获取工具
func (r *ToolRegistry) Get(name string) (tool.BaseTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// GetDef 获取工具定义
func (r *ToolRegistry) GetDef(name string) (ToolDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[name]
	return def, ok
}

// List 获取所有工具信息
func (r *ToolRegistry) List() []*schema.ToolInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var infos []*schema.ToolInfo
	ctx := context.Background()
	for _, t := range r.tools {
		info, err := t.Info(ctx)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

// Builtins 获取内置工具列表
func (r *ToolRegistry) Builtins() map[string]interface{} {
	infos := r.List()
	return map[string]interface{}{
		"tools": infos,
	}
}

// ToolForm 扩展 ComponentForm，增加 ParamsOneOf
type ToolForm struct {
	types.ComponentForm
	ParamsOneOf interface{} `json:"paramsOneOf,omitempty"`
}

// getFieldsFromConfig extracts form fields from a config struct using reflection.
// This is a helper function to avoid code duplication.
func getFieldsFromConfig(config interface{}) []types.ComponentFormField {
	if config == nil {
		return nil
	}

	configType := reflect.TypeOf(config)
	if configType.Kind() == reflect.Ptr {
		configType = configType.Elem()
	}
	configValue := reflect.ValueOf(config)
	if configValue.Kind() == reflect.Ptr {
		configValue = configValue.Elem()
	}
	dummyField := reflect.StructField{Type: configType}
	return rulegoReflect.GetFields(dummyField, configValue)
}

// GetToolForms 获取工具表单列表（包含参数定义）
func (r *ToolRegistry) GetToolForms() []ToolForm {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var forms []ToolForm
	ctx := context.Background()

	// 遍历所有工具实例
	for name, t := range r.tools {
		form := ToolForm{}
		form.Type = name
		form.Label = name
		form.Category = "AI Tool"

		// 获取运行时信息
		if info, err := t.Info(ctx); err == nil {
			form.ParamsOneOf = info.ParamsOneOf
			form.Desc = info.Desc
		}

		// 尝试获取定义中的配置字段
		if def, ok := r.defs[name]; ok && def.Config != nil {
			if form.Desc == "" {
				form.Desc = def.Desc
			}
			form.Fields = getFieldsFromConfig(def.Config)
		}

		forms = append(forms, form)
	}

	// 遍历只有定义但没有实例的工具（如 browseruse，避免创建重量级实例）
	for name, def := range r.defs {
		// 跳过已有实例的工具
		if _, hasInstance := r.tools[name]; hasInstance {
			continue
		}

		form := ToolForm{}
		form.Type = name
		form.Label = name
		form.Category = "AI Tool"
		form.Desc = def.Desc
		form.Fields = getFieldsFromConfig(def.Config)

		forms = append(forms, form)
	}

	return forms
}
