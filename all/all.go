// Package all allows you to import all AI components with one click.
// Triggering init() automatic registration for each subpackage through blank import.
//
// One-click import:
//
//	import _ "github.com/rulego/rulego-components-ai/all"
//
// Import as needed, directly importing the corresponding subpackages:
//
//	import _ "github.com/rulego/rulego-components-ai/agent"
//	import _ "github.com/rulego/rulego-components-ai/tool/bash"
package all

import (
	_ "github.com/rulego/rulego-components-ai/action"
	// Node component - registered in rulego.Registry
	_ "github.com/rulego/rulego-components-ai/agent"
	_ "github.com/rulego/rulego-components-ai/intent"
	_ "github.com/rulego/rulego-components-ai/mcp"

	// Tool Components - Register to tool.Registry
	_ "github.com/rulego/rulego-components-ai/tool/bash"
	_ "github.com/rulego/rulego-components-ai/tool/browseruse"
	_ "github.com/rulego/rulego-components-ai/tool/edit"
	_ "github.com/rulego/rulego-components-ai/tool/mcp"
	_ "github.com/rulego/rulego-components-ai/tool/read"
	_ "github.com/rulego/rulego-components-ai/tool/skill"
	_ "github.com/rulego/rulego-components-ai/tool/write"

	// Endpoint components
	_ "github.com/rulego/rulego-components-ai/endpoint"

	// Processor component
	_ "github.com/rulego/rulego-components-ai/processor"
)
