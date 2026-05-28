// Package constants provides centralized constant definitions for the AI components.
//
// This package eliminates hardcoded values throughout the codebase and provides:
//   - LLM provider URLs (OpenAI, Gitee AI, Zhipu AI, etc.)
//   - Default model names for various providers
//   - Timeout and retry configurations
//   - Third-party service URLs (search engines, IM platforms, etc.)
//
// Usage:
//
//	import "github.com/rulego/rulego-components-ai/constants"
//
//	// Use provider URL
//	config.Url = constants.ProviderOpenAI
//
//	// Use default model
//	config.Model = constants.ModelGPT4
//
//	// Use timeout
//	ctx, cancel := context.WithTimeout(ctx, constants.DefaultHTTPTimeout)
package constants
