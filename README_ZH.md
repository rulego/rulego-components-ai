# rulego-components-ai

基于 [RuleGo](https://github.com/rulego/rulego) 规则引擎的 AI 智能体开发框架。可用于开发独立的 AI 智能体，也可作为基础构建类 Claude Code / OpenClaw 等 AI 编程助手应用。采用**规则链即智能体**的设计理念，将 LLM 推理能力嵌入事件驱动的规则链工作流，通过 JSON 声明式配置、10 种 AOP 切面、四类工具统一抽象和多维会话管理，构建生产级智能体应用。

**完整文档：** [文档](https://rulego.cc/pages/ai-agent-overview/) 

## 与主流框架的差异

| 维度 | LangChain / LangGraph | AutoGen / CrewAI | 本框架 |
|------|----------------------|------------------|--------|
| **定义方式** | Python 代码构建 | Python 代码构建 | **JSON 声明式，支持热更新** |
| **运行模型** | 请求-响应 | 请求-响应 | **事件驱动，规则链编排** |
| **拦截机制** | 简单回调列表 | 无统一机制 | **10 种 AOP 切面，支持 PointCut 条件匹配** |
| **工具来源** | 函数 | 函数 | **内置工具 + 规则链工具 + 子智能体 + MCP 双向** |
| **会话管理** | 基础 Memory 类 | 依赖外部存储 | **6 种作用域，自动压缩/修剪，可扩展存储** |
| **业务集成** | 需自行实现 | 需自行实现 | **原生接入规则引擎 200+ 节点** |
| **模型切换** | 构建时绑定 | 构建时绑定 | **运行时按会话动态切换** |
| **部署** | Python 运行时 | Python 运行时 | **单二进制，低资源占用** |

## 核心特性

- **规则链即智能体** — 智能体用 JSON 声明式定义，可与规则引擎的其他节点（REST 调用、JS 过滤、消息队列等）自由组合，实现 AI 推理与业务编排的混合流水线
- **10 种 AOP 切面** — 覆盖智能体执行的完整生命周期（启动/执行前/环绕/执行后/完成/LLM 调用前后/流式分块/工具调用前后），支持 PointCut 条件匹配和 Order 优先级排序
- **四类工具统一抽象** — 4 种原子工具（bash/read/write/edit）构成感知-创造-执行-进化的基础能力闭环，规则链工具（将任意业务流程暴露为工具）、子智能体工具（多智能体协作）、MCP 双向协议（客户端 + 服务端）
- **Skill 技能系统** — 兼容 Claude Code / OpenClaw 等 AI 编程助手的 SKILL.md 格式，支持多目录聚合、指纹缓存和自动热重载
- **多维会话管理** — 6 种作用域（全局/按用户/按渠道/按线程/按任务），自动 LLM 压缩和智能修剪
- **运行时模型切换** — 通过装饰器链实现会话级动态模型切换，同一个智能体可为不同用户使用不同模型
- **ReAct 推理循环** — 支持同步/流式执行，多模态输入，自动重试
- **MCP 双向协议** — 既是 MCP 客户端（消费外部工具），也是 MCP 服务端（将规则链暴露为 MCP 工具）
- **意图识别** — LLM 分类 + 嵌入向量相似度两种方案
- **OpenAI 兼容** — 流式响应处理器，标准 Chat Completions API 输出

## 模块结构

```
ai/
├── agent/          # 核心 ReAct 智能体节点（类型: ai/agent）
├── action/         # 简单 LLM 操作节点
│                   #   - ai/llm       文本生成
│                   #   - ai/createImage 图片生成
├── intent/         # 意图识别节点
│                   #   - ai/intent     基于 LLM
│                   #   - ai/localIntent 基于嵌入向量
├── aspect/         # AOP 切面框架
│   └── builtin/    #   内置切面（日志、会话、可视化）
├── config/         # 共享配置类型与模型能力注册表
├── constants/      # 常量（提供商 URL、模型名称、超时）
├── embedding/      # 轻量嵌入客户端 + 余弦相似度
├── endpoint/       # MCP Server 端点（规则链暴露为 MCP 工具）
├── errors/         # 结构化错误码（AgentError with Retryable）
├── mcp/            # MCP 客户端节点（调用远程 MCP 服务）
├── processor/      # OpenAI 兼容流式响应处理器
├── session/        # 会话/对话历史管理
├── tool/           # 工具注册表 + 内置工具实现
│   ├── bash/       #   Shell 命令执行
│   ├── read/       #   文件读取与搜索
│   ├── write/      #   文件写入
│   ├── edit/       #   文件编辑（行级、搜索替换）
│   ├── browseruse/ #   浏览器自动化（chromedp）
│   ├── mcp/        #   MCP 工具适配器（self + 远程模式）
│   └── skill/      #   技能调用
├── utils/          # 工具函数
│   ├── contextx/   #   类型安全的 Context Key
│   ├── image/      #   图片加载、Base64 转换
│   ├── llm/        #   LLM 响应解析
│   ├── token/      #   Token 估算与指标采集
│   └── tool/       #   工具参数 JSON Schema 解析
└── all/            # 一键引入所有组件
```

## 快速开始

### 安装

```bash
go get github.com/rulego/rulego-components-ai
```

### 引入组件

一键引入所有组件：

```go
import _ "github.com/rulego/rulego-components-ai/all"
```

按需引入：

```go
import (
    _ "github.com/rulego/rulego-components-ai/agent"
    _ "github.com/rulego/rulego-components-ai/tool/bash"
    _ "github.com/rulego/rulego-components-ai/tool/read"
)
```

### 最小示例：在规则链中使用 AI 智能体

以下是一个完整的智能体规则链 JSON 配置，节点类型为 `ai/agent`：

```json
{
  "ruleChain": {
    "id": "my-agent",
    "name": "我的助手"
  },
  "nodes": [
    {
      "id": "s1",
      "type": "ai/agent",
      "configuration": {
        "url": "${global.llm.url}",
        "key": "${global.llm.key}",
        "model": "glm-5.1",
        "systemPrompt": "你是一个智能助手，请用中文回答。",
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

通过 Go 代码加载并执行：

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
    // 1. 创建 RuleGo 实例
    config := rulego.NewConfig()
    ruleEngine := rulego.NewRuleGo()

    // 2. 加载智能体规则链（JSON 同上）
    chainJson := `{"ruleChain":{"id":"my-agent","name":"我的助手"},"nodes":[{"id":"s1","type":"ai/agent","configuration":{"url":"https://open.bigmodel.cn/api/paas/v4","key":"your-api-key","model":"glm-5.1","systemPrompt":"你是一个智能助手。","tools":[{"type":"builtin","name":"bash"}]}}]}`

    _, err := ruleEngine.OnLoad(chainJson, types.WithConfig(config))
    if err != nil {
        panic(err)
    }

    // 3. 发送消息给智能体
    msg := rulego.NewMsg(0, "user", types.JSON, map[string]string{}, "今天天气怎么样？")
    ruleEngine.OnMsg(msg, rulego.WithContext(context.Background()))

    // 注意：实际使用中需要通过 callback 或 endpoint 获取响应
    fmt.Println("消息已发送")
}
```

## 节点类型

| 节点类型 | 包路径 | 说明 |
|---------|--------|------|
| `ai/agent` | `agent` | ReAct 智能体，支持工具调用、流式输出、多模态 |
| `ai/llm` | `action` | 单次文本生成 |
| `ai/createImage` | `action` | 图片生成（DALL-E 3） |
| `ai/intent` | `intent` | 基于 LLM 的意图识别 |
| `ai/localIntent` | `intent` | 基于嵌入向量的意图识别（低延迟、零 LLM 调用） |
| `x/mcpClient` | `mcp` | MCP 客户端节点 |

## 智能体配置（ai/agent）

```json
{
  "url": "https://open.bigmodel.cn/api/paas/v4",
  "key": "your-api-key",
  "model": "glm-5.1",
  "systemPrompt": "系统提示词，支持 ${variable} 变量和 ${include(\"/path/to/file\")} 文件引用",
  "messages": [
    {"role": "user", "content": "预设消息，用于定时任务场景"}
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
    {"type": "agent", "name": "subAgent", "targetId": "sub-agent-001", "description": "子智能体"},
    {"type": "mcp", "name": "mcpTools", "config": {"serverUrl": "http://localhost:8080/mcp"}}
  ]
}
```

### 工具类型

| 类型 | 说明 |
|------|------|
| `builtin` | 内置工具：`bash`、`read`、`write`、`edit`、`browseruse`、`skill` |
| `rulechain` | 调用另一条规则链作为工具 |
| `agent` | 调用子智能体（rulechain 的语义别名） |
| `mcp` | MCP 协议工具，支持 self（进程内）和远程（http/stdio）模式 |

### 4 种原子工具

| 工具 | 能力 | 说明 |
|------|------|------|
| `bash` | 执行 | Shell 命令执行、HTTP 请求，支持允许/拒绝安全模式、超时控制、输出截断 |
| `read` | 感知 | 文件读取、行范围读取、内容搜索，支持记忆读取和 grep 模式 |
| `write` | 创造 | 文件写入、记忆写入、经验记录，支持安全路径解析 |
| `edit` | 进化 | 文件编辑（行级、搜索替换、插入删除），支持备份历史 |

这 4 种原子工具构成智能体的基础能力闭环：**感知**环境 → **创造**内容 → **执行**操作 → **进化**改进。

### Skill 技能系统

Skill 是基于 SKILL.md 文件定义的可复用能力单元，兼容 Claude Code / OpenClaw 等主流 AI 编程助手的 skill 格式：

```
skills/
  code-review/
    SKILL.md
  refactor/
    SKILL.md
```

SKILL.md 格式（YAML frontmatter + Markdown）：

```markdown
---
name: code-review
description: 代码审查技能
---
请对以下代码进行全面审查，关注：安全性、性能、可维护性...
```

特性：
- **兼容主流格式** — 与 Claude Code 的 SKILL.md 格式完全兼容，可直接复用现有 skill 生态
- **多目录聚合** — 支持用户目录 > 全局目录的优先级叠加，同名 skill 高优先级覆盖
- **自动热重载** — 基于 FNV-1a 指纹缓存，文件变更时自动重新加载，无需重启
- **多种执行模式** — inline（当前智能体执行）、fork（独立子智能体执行）、fork_with_context（携带上下文的子智能体执行）

## 切面框架（AOP）

切面框架允许在不修改智能体核心逻辑的情况下，插入横切关注点（日志、会话、可视化等）。

### 10 种切面接口

| 切面 | 拦截点 |
|------|--------|
| `AgentStartAspect` | 智能体启动 |
| `AgentBeforeAspect` | 执行前（可修改输入） |
| `AgentAroundAspect` | 执行环绕（可替换整个执行） |
| `AgentAfterAspect` | 执行后（可修改输出） |
| `AgentCompletedAspect` | 智能体完成 |
| `MessageBeforeAspect` | LLM 调用前（可修改消息） |
| `MessageAfterAspect` | LLM 调用后 |
| `StreamChunkAspect` | 流式分块 |
| `ToolCallBeforeAspect` | 工具调用前（可修改调用参数） |
| `ToolCallAfterAspect` | 工具调用后 |

### 内置切面

| 切面 | Order | 说明 |
|------|-------|------|
| `SessionAspect` | 50 | 会话管理（加载历史、保存消息、自动压缩） |
| `VizAspect` | 100 | AG-UI 可视化事件推送 |
| `LoggingAspect` | 200 | 执行日志记录 |

### 注册自定义切面

```go
import "github.com/rulego/rulego-components-ai/aspect"

// 注册全局切面
aspect.RegisterAspect("my_aspect", &MyAspect{})
```

## 会话管理

```go
import agentsession "github.com/rulego/rulego-components-ai/session"

// 创建内存会话管理器
storage := agentsession.NewMemoryStorage()
manager := agentsession.NewManager(storage, &agentsession.SessionConfig{
    MaxMessages:   100,
    MaxTokenCount: 100000,
    Pruning: agentsession.PruningConfig{
        Enabled:         true,
        KeepRecentCount: 10,
    },
})

// 通过 SessionAspect 自动集成到智能体
sessionAspect := builtin.NewSessionAspect(manager, builtin.SessionAspectConfig{})
aspect.RegisterAspect("session", sessionAspect)
```

### 会话作用域

| 作用域 | Key 格式 | 说明 |
|--------|----------|------|
| `main` | `agent:{id}` | 默认，每个 Agent 一个会话 |
| `per_peer` | `agent:{id}:peer:{userId}` | 按对话对象隔离 |
| `per_channel_peer` | `agent:{id}:channel:{ch}:peer:{userId}` | 按渠道+对话对象隔离 |
| `thread` | `agent:{id}:thread:{threadId}` | 按话题隔离 |
| `task` | `agent:{id}:task:{taskId}` | 按任务隔离 |

## 相关文档

- [RuleGo 文档](https://rulego.cc/) — RuleGo 规则引擎文档
- [Eino 框架](https://github.com/cloudwego/eino) — 智能体框架

## License

Apache License 2.0
