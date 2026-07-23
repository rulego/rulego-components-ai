package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/rulego/rulego-components-ai/config"
	aitool "github.com/rulego/rulego-components-ai/tool"
)

// ParseToolParameters parsing tool parameters JSON Schema
func ParseToolParameters(parameters string) (*schema.ParamsOneOf, error) {
	var jsonSchema jsonschema.Schema
	if parameters != "" {
		err := json.Unmarshal([]byte(parameters), &jsonSchema)
		if err != nil {
			return nil, err
		}
	} else {
		// Null parameters, default is empty objects
		jsonSchema = jsonschema.Schema{
			Type: "object",
		}
	}
	return schema.NewParamsOneOfByJSONSchema(&jsonSchema), nil
}

// NewToolInfoFromConfig creates a ToolInfo based on the configuration
func NewToolInfoFromConfig(toolConfig config.Tool) (*schema.ToolInfo, error) {
	if toolConfig.Type == config.ToolTypeRuleChain {
		paramsOneOf, err := ParseToolParameters(toolConfig.Parameters)
		if err != nil {
			return nil, fmt.Errorf("invalid tool parameters for %s: %v", toolConfig.Name, err)
		}
		return &schema.ToolInfo{
			Name:        toolConfig.Name,
			Desc:        toolConfig.Description,
			ParamsOneOf: paramsOneOf,
		}, nil
	} else if toolConfig.Type == config.ToolTypeBuiltin {
		// Built-in tools
		if t, ok := aitool.Registry.Get(toolConfig.Name); ok {
			info, err := t.Info(context.Background())
			if err != nil {
				return nil, fmt.Errorf("failed to get info for builtin tool %s: %v", toolConfig.Name, err)
			}
			return info, nil
		}
		// Attempting to create instances through factories (such as browser_use and other tools without pre-registered instances)
		if def, okDef := aitool.Registry.GetDef(toolConfig.Name); okDef && def.Factory != nil {
			if instance, err := def.Factory(map[string]interface{}{}); err == nil {
				info, infoErr := instance.Info(context.Background())
				if infoErr != nil {
					return nil, fmt.Errorf("failed to get info for builtin tool %s: %v", toolConfig.Name, infoErr)
				}
				return info, nil
			} else {
				return nil, fmt.Errorf("failed to create builtin tool %s: %v", toolConfig.Name, err)
			}
		}
		return nil, fmt.Errorf("builtin tool not found: %s", toolConfig.Name)
	}
	return nil, fmt.Errorf("unsupported tool type: %s", toolConfig.Type)
}
