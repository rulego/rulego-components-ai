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

// ParseToolParameters 解析工具参数 JSON Schema
func ParseToolParameters(parameters string) (*schema.ParamsOneOf, error) {
	var jsonSchema jsonschema.Schema
	if parameters != "" {
		err := json.Unmarshal([]byte(parameters), &jsonSchema)
		if err != nil {
			return nil, err
		}
	} else {
		// 空参数，默认为空对象
		jsonSchema = jsonschema.Schema{
			Type: "object",
		}
	}
	return schema.NewParamsOneOfByJSONSchema(&jsonSchema), nil
}

// NewToolInfoFromConfig 根据配置创建 ToolInfo
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
		// 内置工具
		if t, ok := aitool.Registry.Get(toolConfig.Name); ok {
			info, err := t.Info(context.Background())
			if err != nil {
				return nil, fmt.Errorf("failed to get info for builtin tool %s: %v", toolConfig.Name, err)
			}
			return info, nil
		}
		// 尝试通过工厂创建实例（如 browser_use 等没有预注册实例的工具）
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
