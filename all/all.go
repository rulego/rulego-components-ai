// Package all 一键引入所有 AI 组件。
// 通过空白导入触发各子包的 init() 自动注册。
//
// 一键引入:
//
//	import _ "github.com/rulego/rulego-components-ai/all"
//
// 按需引入，直接导入对应子包即可:
//
//	import _ "github.com/rulego/rulego-components-ai/agent"
//	import _ "github.com/rulego/rulego-components-ai/tool/bash"
package all

import (
	_ "github.com/rulego/rulego-components-ai/action"
	// 节点组件 - 注册到 rulego.Registry。ai/agent 由 agent 包提供；
	// agent/lite 不在本包内：它面向不经 agent 包的轻量宿主（32 位构建等），
	// 由宿主显式引入，避免同名组件的生效方取决于包初始化顺序。
	_ "github.com/rulego/rulego-components-ai/agent"
	_ "github.com/rulego/rulego-components-ai/intent"
	_ "github.com/rulego/rulego-components-ai/mcp"

	// 工具组件 - 注册到 tool.Registry
	_ "github.com/rulego/rulego-components-ai/tool/bash"
	_ "github.com/rulego/rulego-components-ai/tool/browseruse"
	_ "github.com/rulego/rulego-components-ai/tool/edit"
	_ "github.com/rulego/rulego-components-ai/tool/mcp"
	_ "github.com/rulego/rulego-components-ai/tool/read"
	_ "github.com/rulego/rulego-components-ai/tool/skill"
	_ "github.com/rulego/rulego-components-ai/tool/write"

	// Endpoint 组件
	_ "github.com/rulego/rulego-components-ai/endpoint"

	// Processor 组件
	_ "github.com/rulego/rulego-components-ai/processor"
)
