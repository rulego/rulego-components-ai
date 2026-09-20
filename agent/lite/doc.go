// Package lite ai/agent 节点的轻量实现,与 agent/(eino 版)共用类型名。
//
// 实现只用 net/http + encoding/json,远程 MCP 经 mark3labs/mcp-go(纯 Go,
// 无汇编依赖):ReAct 主循环(LLM → 工具 → LLM)、流式 SSE、AG-UI 形状
// 工具过程帧、SKILL.md 技能注入、远程 MCP 工具接入、doom-loop 重复检测、
// failover 多端点容灾、token 用量统计、请求级重试与 images 多模态。
// 不依赖 eino/sonic,32 位平台(386/armv7)可编译——这是它独立存在的理由。
//
// 注册语义:与 eino 版同名,同 binary 同时引入两包时先注册者生效——
// all 捆绑包固定 eino 版在前;eino 不可用的构建单独引入本包即可让
// ai/agent 链照常运行。独立组件时期的类型名 ai/agentLite 已废弃。
//
// 工具来源是 rulego core 钦定的中立接口 types.MCPToolProvider:宿主把实现
// 注册到 RuleConfig UDF(key 为 types.MCPToolProviderKey)即可;tools 里声明的
// 远程 MCP server 是另一来源,连接懒建立,server 不可达时降级纯对话,链
// 加载不依赖远程可达。注册表工具可经 tool.Registry.AsMCPToolProvider()
// 桥接后注入,注意该桥位于 tool 包(依赖 eino),32 位构建不可用——此类宿主
// 自行实现 types.MCPToolProvider 即可,接口只有一个 List 与一个 Call。
//
// 与 eino 版的兼容边界(同名组件相互替换):已实现字段的名称、行为与缺省
// 值对齐(采样参数缺省 0.7/0.9、maxStep 缺省 50、maxRetries 缺省 3、工具
// 输出按 rune 截断);仅 eino 版有的字段(messages 预置、streamRetryMode、
// streamToolCallCheck、params 的其余采样字段、failover 端点级 params)忽略
// 不报错。tools 的字符串条目两边通用——eino 版按名解析(工厂实例 →
// RuleConfig UDF → 全局注册表),本实现作为 provider 工具允许列表,是链
// DSL 的可移植写法;mcp 与 builtin(skill) 对象描述符两边承接(本实现分别
// 展开为允许列表/远程连接与技能目录),rulechain/agent 及其余 builtin
// 描述符依赖 agent 包运行时,Init 时显式报错。skillsDir/skills 为本实现特有
// (提示词注入式技能)。字段级明细见 README 的两实现对照表。
package lite
