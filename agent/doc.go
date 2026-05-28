// Package agent provides AI agent implementations for the rulego-components-ai framework.
//
// The agent package implements various agent patterns and utilities:
//   - ReAct Agent: Reasoning and Acting agent pattern
//   - Tool Agent: Agent with tool-calling capabilities
//   - Factory: Agent factory for creating different agent types
//
// # Key Components
//
//   - ReactAgentNode: Implements the ReAct (Reasoning + Acting) agent pattern
//   - ToolAgent: Agent specialized for tool execution
//   - ChatAgentConfig: Configuration for chat-based agents
//   - SubAgentConfig: Configuration for sub-agents in multi-agent systems
//
// # Agent Patterns
//
// The package supports several agent patterns:
//
//   - ReAct: Combines reasoning and action in an iterative loop
//   - Tool-calling: Agents that can invoke external tools
//   - Multi-agent: Support for orchestrating multiple sub-agents
//
// # Integration with RuleGo
//
// Agents can be used as RuleGo nodes, allowing them to be composed in
// rule chains for complex workflows. The ReactAgentNode registers itself
// with the RuleGo registry for automatic discovery.
//
// # Error Handling
//
// The package uses the github.com/rulego/rulego-components-ai/errors
// package for structured error handling with error codes and retryable flags.
package agent
