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

// DynamicSkillLister supports a tool interface for dynamically retrieving skill lists.
// The tool implementing this interface injects the latest skill list into the system prompt via MessageModifier with each request,
// Instead of fixing the skill list into the tool description at initialization.
type DynamicSkillLister interface {
	tool.BaseTool
	// ListSkills retrieves the current list of available skills and renders it as text that can be injected into system prompts.
	// Each call should check whether the underlying data source has changed (such as file fingerprints).
	ListSkills(ctx context.Context) (string, error)
	// GetSkillInstruction returns instructions for using the skill system (such as the guidance text for "How to use Skill").
	GetSkillInstruction() string
}

// GetToolFromConfig Retrieves AI tools from rulego Config
func GetToolFromConfig(c types.Config, name string) (tool.BaseTool, bool) {
	if t := c.GetUdf(name, types.AiTool); t != nil {
		if toolInstance, ok := t.(tool.BaseTool); ok {
			return toolInstance, true
		}
	}
	return nil, false
}

var (
	// Registry global tool registry
	Registry = &ToolRegistry{
		tools: make(map[string]tool.BaseTool),
		defs:  make(map[string]ToolDefinition),
	}
)

// ToolFactory
type ToolFactory func(config map[string]interface{}) (tool.BaseTool, error)

// ToolDefinition Tool definition
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

// ToolRegistry
type ToolRegistry struct {
	tools map[string]tool.BaseTool
	defs  map[string]ToolDefinition
	mu    sync.RWMutex
}

// Register registration tool
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

// RegisterDef Definition of the registration tool
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

// Range traversal tool
func (r *ToolRegistry) Range(f func(name string, t tool.BaseTool) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, t := range r.tools {
		if !f(name, t) {
			break
		}
	}
}

// RangeDef traversal tool definition
func (r *ToolRegistry) RangeDef(f func(name string, def ToolDefinition) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, def := range r.defs {
		if !f(name, def) {
			break
		}
	}
}

// Get the tool to get it
func (r *ToolRegistry) Get(name string) (tool.BaseTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// GetDef Retrieves the tool definition
func (r *ToolRegistry) GetDef(name string) (ToolDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[name]
	return def, ok
}

// List to get all tool information
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

// Builtins to get a list of built-in tools
func (r *ToolRegistry) Builtins() map[string]interface{} {
	infos := r.List()
	return map[string]interface{}{
		"tools": infos,
	}
}

// ToolForm extends ComponentForm by adding ParamsOneOf
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

// GetToolForms Retrieves a list of tool forms (including parameter definitions)
func (r *ToolRegistry) GetToolForms() []ToolForm {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var forms []ToolForm
	ctx := context.Background()

	// Traverse all tool instances
	for name, t := range r.tools {
		form := ToolForm{}
		form.Type = name
		form.Label = name
		form.Category = "AI Tool"

		// Obtain runtime information
		if info, err := t.Info(ctx); err == nil {
			form.ParamsOneOf = info.ParamsOneOf
			form.Desc = info.Desc
		}

		// Try to get the configuration field from the definition
		if def, ok := r.defs[name]; ok && def.Config != nil {
			if form.Desc == "" {
				form.Desc = def.Desc
			}
			form.Fields = getFieldsFromConfig(def.Config)
		}

		forms = append(forms, form)
	}

	// Traversing tools that only define but no instances (such as browseruse, to avoid creating heavyweight instances)
	for name, def := range r.defs {
		// Tools that skip existing instances
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
