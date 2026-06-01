# rulego-components-ai

> A Declarative AI Agent Development Framework

A **declarative** AI agent development framework built on the [RuleGo](https://github.com/rulego/rulego) rule engine. It can be used to develop standalone AI agents or serve as the foundation for building AI coding assistants like Claude Code / OpenClaw. It adopts the **rule chain as agent** design philosophy, embedding LLM reasoning capabilities into event-driven rule chain workflows. Through JSON declarative configuration, 10 AOP aspects, unified abstraction of four tool categories, and multi-dimensional session management, it enables building production-grade agent applications.

**Full Documentation:** [Docs](https://rulego.cc/en/pages/ai-agent-overview/)

## Differences from Mainstream Frameworks

| Dimension | LangChain / LangGraph | AutoGen / CrewAI | This Framework |
|-----------|-----------------------|------------------|----------------|
| **Definition** | Python code | Python code | **JSON declarative, hot-reloadable** |
| **Runtime Model** | Request-response | Request-response | **Event-driven, rule chain orchestration** |
| **Interception** | Simple callback list | No unified mechanism | **10 AOP aspects with PointCut condition matching** |
| **Tool Sources** | Functions | Functions | **Built-in + Rule chain + Sub-agent + MCP bidirectional** |
| **Session Management** | Basic Memory class | External storage required | **6 scopes, auto-compression/pruning, extensible storage** |
| **Business Integration** | DIY required | DIY required | **Native access to rule engine 200+ nodes** |
| **Model Switching** | Build-time binding | Build-time binding | **Runtime per-session dynamic switching** |
| **Deployment** | Python runtime | Python runtime | **Single binary, low resource usage** |

## Core Features

- **Rule Chain as Agent** — Agents are defined declaratively in JSON and can be freely composed with other rule engine nodes (REST calls, JS filters, message queues, etc.) to build hybrid pipelines of AI reasoning and business orchestration
- **10 AOP Aspects** — Covering the complete agent execution lifecycle (start/before/around/after/completed/pre-LLM/post-LLM/stream-chunk/pre-tool-call/post-tool-call), with PointCut condition matching and Order priority
- **Unified Abstraction of Four Tool Categories** — 4 atomic tools (bash/read/write/edit) form the perception-creation-execution-evolution capability loop; rule chain tools (expose any business process as a tool), sub-agent tools (multi-agent collaboration), MCP bidirectional protocol (client + server)
- **Skill System** — Compatible with the SKILL.md format used by Claude Code / OpenClaw and other AI coding assistants, supporting multi-directory aggregation, fingerprint caching, and automatic hot-reloading
- **Multi-dimensional Session Management** — 6 scopes (global/per-user/per-channel/per-thread/per-task), automatic LLM compression and intelligent pruning
- **Runtime Model Switching** — Session-level dynamic model switching via decorator chains; the same agent can use different models for different users
- **ReAct Reasoning Loop** — Supports synchronous/streaming execution, multimodal input, automatic retry
- **MCP Bidirectional Protocol** — Acts as both MCP client (consuming external tools) and MCP server (exposing rule chains as MCP tools)
- **Intent Recognition** — LLM classification and embedding vector similarity approaches
- **OpenAI Compatible** — Streaming response processor, standard Chat Completions API output

## Module Structure

```
ai/
├── agent/          # Core ReAct agent node (type: ai/agent)
├── action/         # Simple LLM operation nodes
│                   #   - ai/llm       Text generation
│                   #   - ai/createImage Image generation
├── intent/         # Intent recognition nodes
│                   #   - ai/intent     LLM-based
│                   #   - ai/localIntent Embedding vector-based
├── aspect/         # AOP aspect framework
│   └── builtin/    #   Built-in aspects (logging, session, visualization)
├── config/         # Shared configuration types and model capability registry
├── constants/      # Constants (provider URLs, model names, timeouts)
├── embedding/      # Lightweight embedding client + cosine similarity
├── endpoint/       # MCP Server endpoint (rule chains exposed as MCP tools)
├── errors/         # Structured error codes (AgentError with Retryable)
├── mcp/            # MCP client node (calling remote MCP services)
├── processor/      # OpenAI-compatible streaming response processor
├── session/        # Session/conversation history management
├── tool/           # Tool registry + built-in tool implementations
│   ├── bash/       #   Shell command execution
│   ├── read/       #   File reading and search
│   ├── write/      #   File writing
│   ├── edit/       #   File editing (line-level, search-replace)
│   ├── browseruse/ #   Browser automation (chromedp)
│   ├── mcp/        #   MCP tool adapter (self + remote mode)
│   └── skill/      #   Skill invocation
├── utils/          # Utility functions
│   ├── contextx/   #   Type-safe Context Key
│   ├── image/      #   Image loading, Base64 conversion
│   ├── llm/        #   LLM response parsing
│   ├── token/      #   Token estimation and metrics collection
│   └── tool/       #   Tool parameter JSON Schema parsing
└── all/            # One-liner import for all components
```

## Quick Start

### Installation

```bash
go get github.com/rulego/rulego-components-ai
```

### Import Components

Import all components in one line:

```go
import _ "github.com/rulego/rulego-components-ai/all"
```

Import on demand:

```go
import (
    _ "github.com/rulego/rulego-components-ai/agent"
    _ "github.com/rulego/rulego-components-ai/tool/bash"
    _ "github.com/rulego/rulego-components-ai/tool/read"
)
```

### Minimal Example: Using an AI Agent in a Rule Chain

Here is a complete agent rule chain JSON configuration with the node type `ai/agent`:

```json
{
  "ruleChain": {
    "id": "my-agent",
    "name": "My Assistant"
  },
  "nodes": [
    {
      "id": "s1",
      "type": "ai/agent",
      "configuration": {
        "url": "${global.llm.url}",
        "key": "${global.llm.key}",
        "model": "glm-5.1",
        "systemPrompt": "You are a helpful assistant.",
        "maxStep": 50,
        "params": {
          "temperature": 0.7
        },
        "tools": [
          {"type": "builtin", "name": "bash"},
          {"type": "builtin", "name": "read"},
          {"type": "builtin", "name": "write"}
        ]
      }
    }
  ]
}
```

Load and execute via Go code:

```go
package main

import (
    "context"
    "fmt"

    _ "github.com/rulego/rulego-components-ai/all"
    "github.com/rulego/rulego/api/types"
    "github.com/rulego/rulego/rulego"
)

func main() {
    // 1. Create a RuleGo instance
    config := rulego.NewConfig()
    ruleEngine := rulego.NewRuleGo()

    // 2. Load the agent rule chain (JSON as above)
    chainJson := `{"ruleChain":{"id":"my-agent","name":"My Assistant"},"nodes":[{"id":"s1","type":"ai/agent","configuration":{"url":"https://open.bigmodel.cn/api/paas/v4","key":"your-api-key","model":"glm-5.1","systemPrompt":"You are a helpful assistant.","tools":[{"type":"builtin","name":"bash"}]}}]}`

    _, err := ruleEngine.OnLoad(chainJson, types.WithConfig(config))
    if err != nil {
        panic(err)
    }

    // 3. Send a message to the agent
    msg := rulego.NewMsg(0, "user", types.JSON, map[string]string{}, "What's the weather like today?")
    ruleEngine.OnMsg(msg, rulego.WithContext(context.Background()))

    // Note: In production, retrieve responses via callback or endpoint
    fmt.Println("Message sent")
}
```

## Node Types

| Node Type | Package | Description |
|-----------|---------|-------------|
| `ai/agent` | `agent` | ReAct agent with tool calling, streaming output, multimodal support |
| `ai/llm` | `action` | Single-shot text generation |
| `ai/createImage` | `action` | Image generation (DALL-E 3) |
| `ai/intent` | `intent` | LLM-based intent recognition |
| `ai/localIntent` | `intent` | Embedding vector-based intent recognition (low latency, zero LLM calls) |
| `x/mcpClient` | `mcp` | MCP client node |

## Agent Configuration (ai/agent)

```json
{
  "url": "https://open.bigmodel.cn/api/paas/v4",
  "key": "your-api-key",
  "model": "glm-5.1",
  "systemPrompt": "System prompt, supports ${variable} and ${include(\"/path/to/file\")} file references",
  "messages": [
    {"role": "user", "content": "Preset messages for scheduled task scenarios"}
  ],
  "images": ["https://example.com/image.png"],
  "params": {
    "temperature": 0.7,
    "topP": 0.9,
    "maxTokens": 4096,
    "responseFormat": "text",
    "extraFields": {
      "thinking_budget_tokens": 8192
    }
  },
  "maxStep": 50,
  "maxToolOutputLength": 5000,
  "maxRetries": 3,
  "tools": [
    {"type": "builtin", "name": "bash"},
    {"type": "rulechain", "name": "myChain", "targetId": "chain-001"},
    {"type": "agent", "name": "subAgent", "targetId": "sub-agent-001", "description": "Sub-agent"},
    {"type": "mcp", "name": "mcpTools", "config": {"serverUrl": "http://localhost:8080/mcp"}}
  ]
}
```

### Tool Types

| Type | Description |
|------|-------------|
| `builtin` | Built-in tools: `bash`, `read`, `write`, `edit`, `browseruse`, `skill` |
| `rulechain` | Call another rule chain as a tool |
| `agent` | Call a sub-agent (semantic alias of rulechain) |
| `mcp` | MCP protocol tool, supports self (in-process) and remote (http/stdio) modes |

### 4 Atomic Tools

| Tool | Capability | Description |
|------|-----------|-------------|
| `bash` | Execute | Shell command execution, HTTP requests; supports allow/deny security mode, timeout control, output truncation |
| `read` | Perceive | File reading, line-range reading, content search; supports memory reading and grep mode |
| `write` | Create | File writing, memory writing, experience recording; supports safe path resolution |
| `edit` | Evolve | File editing (line-level, search-replace, insert/delete); supports backup history |

These 4 atomic tools form the agent's capability loop: **Perceive** the environment → **Create** content → **Execute** operations → **Evolve** improvements.

### Skill System

Skills are reusable capability units defined via SKILL.md files, compatible with the skill format used by Claude Code / OpenClaw and other mainstream AI coding assistants:

```
skills/
  code-review/
    SKILL.md
  refactor/
    SKILL.md
```

SKILL.md format (YAML frontmatter + Markdown):

```markdown
---
name: code-review
description: Code review skill
---
Please perform a comprehensive code review, focusing on: security, performance, maintainability...
```

Features:
- **Mainstream Format Compatible** — Fully compatible with Claude Code's SKILL.md format; reuse existing skill ecosystems directly
- **Multi-directory Aggregation** — Supports priority overlay of user directory > global directory; higher-priority skills override lower-priority ones
- **Auto Hot-reload** — FNV-1a fingerprint-based caching; automatically reloads on file changes without restart
- **Multiple Execution Modes** — inline (execute in current agent), fork (execute in independent sub-agent), fork_with_context (sub-agent with context)

## Aspect Framework (AOP)

The aspect framework allows inserting cross-cutting concerns (logging, sessions, visualization, etc.) without modifying the agent's core logic.

### 10 Aspect Interfaces

| Aspect | Interception Point |
|--------|-------------------|
| `AgentStartAspect` | Agent startup |
| `AgentBeforeAspect` | Before execution (can modify input) |
| `AgentAroundAspect` | Around execution (can replace entire execution) |
| `AgentAfterAspect` | After execution (can modify output) |
| `AgentCompletedAspect` | Agent completion |
| `MessageBeforeAspect` | Before LLM call (can modify messages) |
| `MessageAfterAspect` | After LLM call |
| `StreamChunkAspect` | Streaming chunk |
| `ToolCallBeforeAspect` | Before tool call (can modify call parameters) |
| `ToolCallAfterAspect` | After tool call |

### Built-in Aspects

| Aspect | Order | Description |
|--------|-------|-------------|
| `SessionAspect` | 50 | Session management (load history, save messages, auto-compress) |
| `VizAspect` | 100 | AG-UI visualization event push |
| `LoggingAspect` | 200 | Execution logging |

### Register Custom Aspects

```go
import "github.com/rulego/rulego-components-ai/aspect"

// Register a global aspect
aspect.RegisterAspect("my_aspect", &MyAspect{})
```

## Session Management

```go
import agentsession "github.com/rulego/rulego-components-ai/session"

// Create an in-memory session manager
storage := agentsession.NewMemoryStorage()
manager := agentsession.NewManager(storage, &agentsession.SessionConfig{
    MaxMessages:   100,
    MaxTokenCount: 100000,
    Pruning: agentsession.PruningConfig{
        Enabled:         true,
        KeepRecentCount: 10,
    },
})

// Auto-integrate into agent via SessionAspect
sessionAspect := builtin.NewSessionAspect(manager, builtin.SessionAspectConfig{})
aspect.RegisterAspect("session", sessionAspect)
```

### Session Scopes

| Scope | Key Format | Description |
|-------|------------|-------------|
| `main` | `agent:{id}` | Default; one session per agent |
| `per_peer` | `agent:{id}:peer:{userId}` | Isolated per conversation peer |
| `per_channel_peer` | `agent:{id}:channel:{ch}:peer:{userId}` | Isolated per channel + peer |
| `thread` | `agent:{id}:thread:{threadId}` | Isolated per thread |
| `task` | `agent:{id}:task:{taskId}` | Isolated per task |

## Related Documentation

- [RuleGo Docs](https://rulego.cc/en/pages/home/) — RuleGo rule engine documentation
- [Eino Framework](https://github.com/cloudwego/eino) — Agent framework

## License

Apache License 2.0
