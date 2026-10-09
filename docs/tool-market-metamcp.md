# 使用 metamcp 发现、管理和调用工具

`metamcp` 是工具市场 MCP 连接的内置入口。管理操作免费；`invoke` 沿用目标工具的价格、授权和预算，没有额外的元工具费用。内置工具需要确认的绘图、转账和悬赏资金等业务费用保持不变。

本文说明默认 Go 后端，不代表 Rust 预览后端已具有相同能力。

## 先搜索摘要，再读取详情

`search` 默认返回 5 个服务。每项只含服务 `id`、发布版本 `version_id`、名称、用途摘要、工具数量、基础价格范围及按用量计费的工具数量，不返回 `tools`、输入参数定义或输出参数定义。用途摘要最多 240 个字符；截短时返回 `description_truncated: true`。服务摘要与价格范围来自同一次列表查询，不逐项读取详情。

先调用：

```json
{"name":"metamcp","arguments":{"action":"search","query":"搜索网页"}}
```

选中服务后，将结果的 `id` 传给 `service_id`：

```json
{"name":"metamcp","arguments":{"action":"details","service_id":"实际服务 ID"}}
```

`details` 保留完整描述、工具 ID、当前版本 ID、参数定义和计费规则。服务可能在两次请求之间更新；加载、授权和调用应使用详情中确认过的版本，不要将旧搜索版本与新详情混用。

`min_price_quota` 和 `max_price_quota` 是各工具的基础费用范围，不是按用量收费的最终总价。`metered_tools > 0` 时，必须查看详情中的用量费率。工具调用免费，也不代表确认后的绘图、转账等业务免费。额度单位沿用返回的 `credits_per_usd`。

`offset` 默认是 0。`limit` 仍接受 1–100，兼容已有显式分页请求；`usage` 和 `calls` 的默认数量仍为 20。继续翻页时增加 `offset`；返回数量小于 `limit` 时已到当前结果末尾。空结果使用 `items: []`。

## 精简模式与旧入口兼容

默认连接地址不变：

```text
https://api.lmm.best/mcp/market
```

需要只向模型暴露一个工具时，将 MCP 连接地址改为：

```text
https://api.lmm.best/mcp/market?mode=compact
```

精简模式的 `tools/list` 只有 `metamcp`。旧的 `lmm_market_*` 和独立 `market_tool_*` 不注册，发现、管理、调用和状态查询均通过 `metamcp` 完成。加载或授权成功后不需要刷新列表，返回的 `refresh_tools_list` 为 `false`。

省略 `mode` 或使用 `mode=full` 时，保留旧管理入口和当前连接已获授权的独立工具入口。只需要这些独立入口时，才按提示刷新列表。不支持组合参数定义的旧客户端可继续使用完整模式中的独立入口。

模式只决定暴露哪些工具，不改变账号、连接身份、权限、授权、额度、用户确认和重复请求处理。两种模式共用已有请求记录。未知模式返回 HTTP 400；所有模式仍需原有认证。客户端应在后续请求中保留同一个连接地址。已有 OAuth 资源标识与授权范围不变。

## 每种操作的必填字段

`inputSchema` 保持对象类型，使用 `oneOf` 列出 12 种操作。每个分支固定 `action`，声明本操作的必填字段，并拒绝其他操作的字段。服务端使用同一份字段规则检查原始 JSON，仍保留动态有效期、额度边界、授权和业务参数检查。

| action | 除 action 外的必填字段 |
| --- | --- |
| search | 无；可选 query、offset、limit |
| details | service_id |
| status | 无 |
| load、unload | tool_id、version_id |
| authorize | tool_id、version_id、max_price_quota、max_total_quota、max_calls、expires_at |
| set_tool_budget | tool_id、version_id、grant_id、limit_quota |
| set_client_budget | limit_quota |
| usage、calls | 无；可选 offset、limit |
| call_status | call_id |
| invoke | tool_id、version_id、request_id、arguments |

`expires_at` 为未来的 Unix 秒数。预算字段必须显式给出；`0` 不是无限额度。`invoke.arguments` 必须是对象，即使无参数也须发送 `{}`。参数定义不能代替服务端的权限检查。

## 直接调用

先用 `search` 选择服务，再用 `details` 取得真实的 `tool_id`、`version_id`、参数定义和价格。已知服务 ID 时可直接读取详情。工具必须已在当前连接加载，并具有该版本的有效授权。加载不等于授权。

向 MCP 的 `tools/call` 发送下面的参数。示例中的 ID 必须替换为实际值。

```json
{
  "name": "metamcp",
  "arguments": {
    "action": "invoke",
    "tool_id": "实际工具 ID",
    "version_id": "已授权版本 ID",
    "request_id": "这次业务请求的唯一 ID",
    "arguments": {}
  }
}
```

外层 `arguments` 是元工具参数，内层 `arguments` 是目标工具参数。不需要先刷新 `tools/list`。已有的 `market_tool_*` 入口仍可使用；只有需要发现这些独立工具入口时才需要刷新列表。

`status` 返回 `invoke_supported`、`can_invoke`、`invoke_requires_tools_list_refresh` 和 `invoke_uses_target_pricing`，供客户端判断能力、权限和计费方式。原有 `free: true` 说明管理操作免费，不代表被调用的业务免费。

## 权限和费用

`invoke` 要求当前连接具有调用权限，且当前账号、连接、工具和版本的加载记录与授权均有效。它不要求额外授予管理权限：账号所有者已配置好的工具授权，可供仅有调用权限的连接使用。

AI 自己加载、卸载工具仍需要管理权限。AI 创建工具授权仍需要账号所有者在连接设置中明确启用 AI 工具管理，并给出有限的额度。参见 [连接管理说明](tool-market-connections.md#ai-tool-management)。

直接调用不会自动加载、授权、改变版本或增加预算。授权的次数、有效期、单次价格上限、工具预算、连接预算和账号预算均沿用原执行入口。元工具参数不接受账号、连接、授权 ID、目标地址或价格覆盖。目标身份来自当前认证，不来自模型提供的参数。

## 用户确认和重复请求

目标工具要求用户确认时，`metamcp` 保留原始 MCP 确认信息。确认后，使用相同的工具、版本、`request_id` 和业务参数，并在 `tools/call` 参数层带回服务器返回的 `requestState` 与用户产生的 `inputResponses`。不要将这两个字段放进元工具或业务参数，也不要自行生成确认结果。

`metamcp` 和 `market_tool_*` 共用请求记录。通过一个入口发起，再通过另一个入口确认或重试，不会新建第二次业务调用。同一 `request_id` 改用其他工具、版本或业务参数会被拒绝。

遇到运行中、结果未知或网络中断，不要更换 `request_id` 重做业务。使用 `call_status` 查询原调用；结果保留时限和过期处理沿用现有工具市场规则。文本、图片、音频、结构化内容、确认信息及 `lmm/market` 调用信息均使用原有结果转换逻辑，不会被包成普通管理查询结果。

## 输入限制

工具、版本和请求 ID 均为非空字符串，最多 128 字节。业务参数必须是 JSON 对象；没有参数时发送 `{}`。业务参数最多 128 KiB，并继续接受目标工具的参数检查。原始数字不会先转为浮点数。

管理操作保留原来的 8 KiB 参数限制。`invoke` 的元工具参数最多 256 KiB；整个 HTTP 请求仍受原有 256 KiB 上限约束，因此可用空间还需扣除协议字段。未知字段、重复顶层字段、错误大小写别名、缺失必填字段以及 `null` 均会被拒绝。

## 验证

```sh
cd apps/api-go
go test -p 2 ./controller -run 'ToolMarketMeta|ToolMarketMCP|ToolMarketBuiltin' -count=1
go test -p 2 ./model ./service -run ToolMarket -count=1
```

测试还覆盖 12 种操作的必填字段与跨操作字段、分页默认值、摘要体积、单次列表查询、完整详情、私有服务过滤、精简与完整模式、跨模式重复请求。原有测试覆盖原始数字、输入边界、只读连接、加载与授权分离、账号及连接隔离、版本检查、撤销和次数上限、目标参数检查，以及跨入口确认和重复请求。参数解码测试不能替代真实数据库、执行器和 MCP HTTP 集成测试。
