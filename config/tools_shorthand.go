// tools_shorthand.go tools 配置的字符串速记:与 ai/agent 轻量实现
// (agent/lite)共享的可移植写法——条目直接写工具名,按名解析
// (工厂实例 → RuleConfig UDF → 全局注册表,见 agent.CreateTool)。
//
//	"tools": ["bash", {"type": "rulechain", "name": "清理任务", "targetId": "tool_clean"}]
//
// 字符串条目与对象条目可混用;对象描述符(type=rulechain/builtin/agent/mcp)
// 仍是完整语法,mcp(self) 与 builtin(skill) 两边承接。
package config

import "encoding/json"

// UnmarshalJSON 字符串条目展开为 {name: <string>, type: ""}(空 type 即按名解析);
// 对象条目原样解码。字符串速记是两边通用的子集(Lite 实现把字符串条目并入
// provider 工具允许列表)。
func (t *Tool) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		t.Name = name
		return nil
	}
	type toolPlain Tool // 防递归:别名类型不带本方法
	var p toolPlain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*t = Tool(p)
	return nil
}

// NormalizeToolsShorthand 把链配置 map 里 tools 的字符串条目原地规整为
// {"name": <string>} 对象。规则链节点配置经 mapstructure 解码,不走
// json.UnmarshalJSON,须在 Map2Struct 之前调用;对已是对象的条目无操作。
func NormalizeToolsShorthand(cfg map[string]interface{}) {
	if cfg == nil {
		return
	}
	raw, ok := cfg["tools"]
	if !ok {
		return
	}
	list, ok := raw.([]interface{})
	if !ok {
		return
	}
	changed := false
	for i, item := range list {
		name, isStr := item.(string)
		if !isStr || name == "" {
			continue
		}
		list[i] = map[string]interface{}{"name": name}
		changed = true
	}
	if changed {
		cfg["tools"] = list
	}
}
