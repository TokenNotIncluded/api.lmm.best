# Responses 工具兼容性

Codex 使用 Responses 协议。客户端保持 `wire_api = "responses"`，API 地址使用站点的 `/v1` 基址。配置与协议依据：[Codex 配置参考](https://learn.chatgpt.com/docs/config-file/config-reference)、[工具搜索](https://developers.openai.com/api/docs/guides/tools-tool-search)、[命名空间函数](https://developers.openai.com/api/docs/guides/function-calling)。

## Go 后端的处理方式

原生 OpenAI Responses 渠道保留请求中的 `tools`、`input` 和 `tool_choice`，JSON 和 SSE 回包保留上游原始工具数据。上游仍须支持请求使用的 Responses 功能。

经高级自定义渠道转换到 Chat Completions、或经 Gemini 渠道转换时，转换注册器适配以下工具结构：

| Responses 结构 | 转换行为 |
| --- | --- |
| `namespace` 中的函数 | 分配合法且不冲突的上游名称；历史调用和 `tool_choice` 使用同一映射；JSON、SSE 和最终响应恢复原始 `namespace` 与 `name` |
| 默认或 `execution: "server"` 的 `tool_search` | 提前加载请求及已返回搜索结果中提供的函数，移除搜索声明和已完成服务端搜索历史；工具定义占用的上下文可能增加 |
| `execution: "client"` 的 `tool_search` | 以普通函数向上游提供搜索能力；返回客户端时恢复 `tool_search_call` 和对象形式的 `arguments`，由客户端实际执行搜索 |
| 客户端返回的 `tool_search_output` | 转换成对应的工具结果，并把其中的动态工具加入下一轮上游工具列表 |

映射只保存在当前请求的转换上下文中，不修改客户端请求，也不跨用户共享。网关不执行客户端搜索或业务工具，不根据模型返回值访问外部服务。

## 仅提供 Chat Completions 的上游

管理员可在高级自定义渠道配置一条入站 `/v1/responses`、上游 `/v1/chat/completions` 的路由，选择 **OpenAI Responses to OpenAI Chat** 转换器（`openai_responses_to_openai_chat_completions`）。上游基址和认证沿用该渠道配置。客户端继续请求 `/v1/responses`，由渠道同时转换请求和返回事件。原生 OpenAI 渠道不会根据上游报错自动切换协议。

## 支持边界

- 提前加载需要请求提供工具定义；它没有实现上游专有的服务端搜索引擎。强制执行服务端 `tool_search` 等无法等价转换的请求会被明确拒绝。
- 自定义工具、托管工具以及有状态字段仍受原有转换器能力限制。此变更没有新增 Claude/AWS/Vertex 渠道的原生 Responses 路由，也没有扩展 Rust 预览后端。
- 命名空间或客户端搜索转换需要复用同一份 `convmeta.Meta` 执行请求转换、响应转换和流结束处理。Go 服务已有该上下文；独立 relaykit 调用方可使用 `convmeta.Values`。
- 回归测试使用本地协议数据与模拟上游，覆盖转发和工具调用往返；线上客户端、渠道配置及具体上游模型仍需部署后联调。
