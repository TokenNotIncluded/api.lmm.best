# 付费 MCP 测试服务

这个服务专门测试工具市场上传、授权和成功计费。它使用真实 MCP 协议，但只返回固定测试结果，不访问绘图模型、支付接口或其他收费服务。运行命令不会自动上传、审核或发布。

## 启动

在仓库根目录运行：

```sh
cd apps/api-go
go run ./cmd/tool-market-test-mcp -listen 127.0.0.1:8123
```

MCP 地址是 `http://127.0.0.1:8123/mcp`，健康检查是 `/health`。本地 HTTP 地址用于单独测试服务，不能提交给生产工具市场。

需要认证时，设置 `MARKET_TEST_MCP_BEARER` 为至少 24 个字符的随机测试凭证。凭证从环境变量读取，启动日志不会输出它。凭证只属于这个测试服务，与 LMM 登录凭证、API Key、市场连接令牌不同。不要把它放进 URL。

生产市场的 Remote 接入仍要求公网 HTTPS、默认 443 端口和正常证书验证。可以在已有 HTTPS 反向代理后转发至本地服务，也可以直接传入 `-tls-cert` 和 `-tls-key`。没有证书时，命令只允许绑定环回 IP。自签证书、环回地址、私网地址或非 443 端口不能通过市场校验。

## 用例和价格

| Tool | 测试结果 | 示例成功价格（quota） |
| --- | --- | ---: |
| `fixture_echo` | 原生文本和 `structuredContent`，回显输入 | 100 |
| `fixture_add` | 两个有界整数求和 | 10 |
| `fixture_image` | 固定 1×1 PNG，原生 MCP 图片 | 150 |
| `fixture_fail` | `isError: true`，明确失败 | 100，但失败实扣 0 |
| `fixture_invalid_output` | 不符合输出 schema 的结果 | 100，但无效结果实扣 0 |
| `fixture_empty` | 空结果 | 100，但空结果实扣 0 |
| `fixture_pending` | `status: pending`，结果未完成 | 100，但不结算成功 |
| `fixture_unknown` | 故意返回协议错误 | 100，但不结算成功 |
| `fixture_slow_echo` | 最长等待两秒后回显，用于并发重放 | 100 |

价格由市场草稿决定，MCP 服务不会自行收费。`quota` 是平台整数计费单位，不是美元或人民币。上表的固定像素也不是绘图模型的产物。

未知结果会暂时冻结金额，不能自动新建请求重试。到期恢复任务释放冻结金额，不重新执行远端业务；这与已成功消费后的退款不同。

## 上传和授权

示例配置位于 `examples/tool-market-test-mcp/`，所有地址、ID 和有效期都需要替换。默认服务可见范围是“仅自己”。需要另一个测试账号作为买家时，改为 `shared` 并明确设置 `allowed_users`，不用公开整个测试服务。

使用隔离的测试环境和标准测试账号，按下面顺序操作。不要使用生产数据库做计费验证。

1. `POST /api/tool-market/inspect` 传入 `endpoint`。认证服务同时传入 `authentication: {"mode":"bearer","secret":"测试服务凭证"}`；它只用于这次发现。读取定义不调用业务 Tool。
2. 将返回的 `data` 作为草稿的 `tools`，逐 Tool 设置 `price_quota`，再通过 `POST /api/tool-market/services` 保存。`prices.json` 是可复用的价格配置；`service.json` 是草稿元数据模板，缺少 `tools` 时不能直接提交。
3. 认证服务用 `PUT /api/tool-market/services/:id/credentials` 保存 `{version_id, mode:"bearer", secret}`。使用当前草稿版本 ID。凭证另行加密保存，不写入草稿 JSON、URL、调用日志或结果。
4. `POST /api/tool-market/services/:id/validate` 校验当前定义和凭证；`POST /api/tool-market/services/:id/submit` 提交 `{version_id}`。管理员用同版本 ID `POST .../review` 批准时，再次访问远端可信校验。管理员和超级管理员可以批准自己发布的服务；普通用户不能审核。具有管理权限的作者也可拒绝自己的待审版本来撤回提交。
5. 买家创建绑定 `paid-test-client` 的市场连接令牌，使用 `/mcp/market` 连接。先用 `PUT /api/tool-market/installations` 加载精确 Tool 和版本；此时不会授予调用或付款权限。
6. 买家用 `POST /api/tool-market/grants` 明确授权对应客户端、Tool、版本、单次和累计额度、次数及有效期。`grant.json` 的 `expires_at` 要改为未来 Unix 秒。首次测试建议账户总预算 600 quota，单工具最多四次，测试结束撤销授权和令牌。
7. 刷新 `tools/list`，调用 `market_tool_<去掉连字符的 Tool ID>`，传入 `{request_id:"唯一业务标识", arguments:{...}}`。相同标识只用于相同参数的重放；复用标识但改参数会被拒绝。

Bearer 或 API Key 认证的服务，需要在 LMM 测试实例配置稳定的 `TOOL_MARKET_ENCRYPTION_KEY`。凭证属于服务所有者、精确版本和端点；已提交/发布版本不可直接改凭证，轮换需新草稿版本再审核。`GET .../credentials?version_id=...` 只返回是否配置、模式和更新时间，不返回明文。

## 自动验收

```sh
cd apps/api-go
GOMAXPROCS=2 go test -race -p 2 ./internal/toolmarketfixture ./cmd/tool-market-test-mcp
GOMAXPROCS=2 go test -race -p 2 ./service -run 'TestToolMarketPaid' -count=1
```

端到端测试启动真实 HTTPS MCP 服务，通过实际注册的 HTTP 路由发现、上传、校验、审核，为客户端加载和授权，再从 `/mcp/market` 调用。测试账号和余额在独立数据库内，网络映射只编译进 `*_test.go`；生产 SSRF、证书、重定向和凭证边界不变。

数据库并发验收使用 PostgreSQL 18 和多连接。配置 `TEST_POSTGRES_DSN` 指向专用测试数据库，并设置 `TEST_POSTGRES_ISOLATED_SCHEMA=1`，测试会创建独立随机 schema 并在结束时删除。未配置时该用例跳过，不能把 SQLite 的单连接检查当成 PostgreSQL 并发验收。

MySQL 回归使用默认 `REPEATABLE-READ`、`utf8mb4_0900_ai_ci` 的专用 MySQL 8 实例，并要求默认开启的 Performance Schema 事务观测。配置 `TEST_MYSQL_DSN`，测试账户需要 `CREATE/DROP DATABASE`、`PROCESS` 及相关 Performance Schema 表的读取权限；设置 `TEST_MYSQL_ISOLATED_DATABASE=1` 后，每项测试创建并删除自己的随机数据库。不要指向生产数据库。服务资格 CI 显式执行以下四项；未配置 DSN 时的跳过不能算 MySQL 验收：

```sh
TEST_MYSQL_ISOLATED_DATABASE=1 go test -race -count=1 -v ./model \
  -run '^(TestToolMarketDrawingExpiryReadsCommittedBillingMySQL|TestToolMarketClientIdentityMySQL|TestToolMarketConcurrentFinishCountersMySQL|TestToolMarketConcurrentReserveLimitsMySQL)$' \
  -timeout 180s
```

测试通过 Performance Schema 检查两个连接在本测试数据库上的实际服务行锁等待关系，覆盖绘图结算与到期恢复交错、大小写/重音客户端隔离、两笔并发结算的授权与预算计数，以及并发预扣不能突破授权次数、累计额度及三层预算上限。市场事务使用局部 `READ COMMITTED`，数据库会话默认仍为 `REPEATABLE-READ`。

检查内容包括原生图片/结构化结果、成功扣一次、作者和平台入账、失败零费用、未知到期恢复、重复标识冲突、并发重放、预算和次数上限、跨用户/客户端访问、过期/撤销、凭证绑定、参数 schema 和定义漂移。真实生产上架、真实支付或真实绘图模型收费不属于这个固定测试服务的证明范围。
