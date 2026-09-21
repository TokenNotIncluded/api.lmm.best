# OAuth 授权服务器：第一阶段核心

**状态：未接线，不可直接公开部署。** 本目录提供协议校验、授权事务、令牌轮换/撤销及测试；没有注册 Gin 路由，没有运行生产迁移。

本实现不使用 dashboard JWT、普通 API Key 或现有 OAuth 客户端。`oauth/provider.go`、`service/auth_token.go`、全局 option、router、main 和迁移注册列表均未修改。这里也没有 Pi、Web 授权页或 relay/计费实现。

## 依赖选择与供应链核查

2026-09-13 本地核查：项目 `go.mod` 声明 Go 1.25.1，已有 GORM v1.25.2、PostgreSQL driver v1.5.2、glebarez SQLite v1.9.0。以下库在 Go 版本要求上可兼容：

| 候选 | 核查结果 | 本阶段决定 |
| --- | --- | --- |
| [ORY Fosite](https://github.com/ory/fosite) | GitHub latest release 返回 [v0.49.0](https://github.com/ory/fosite/releases/tag/v0.49.0)，发布于 2024-12-12；其 go.mod 为 Go 1.22、toolchain 1.23.1，依赖面包括 ORY X、JOSE/JWT、OpenTelemetry 等。 | 未引入。它能提供通用协议处理，但浏览器同意绑定、独立 GORM 存储和本项目的事务原子性仍需实现。**v0.49.0 不等于自动符合 RFC 9700。** |
| [go-oauth2/oauth2](https://github.com/go-oauth2/oauth2) | latest release 返回 [v4.6.0](https://github.com/go-oauth2/oauth2/releases/tag/v4.6.0)，发布于 2026-09-01，Go 1.21；近期仍有发布，通用接口覆盖更多 grant。 | 未引入。其默认模型/存储接口不能替代本任务的单次同意与整族重放撤销事务。 |

Fosite 的[公开安全公告](https://github.com/ory/fosite/security/advisories)包含历史 redirect 大小写、loopback host/query 匹配问题（CVE-2020-15234、CVE-2020-15233，修复版本 v0.34.1），以及忽略撤销存储错误（CVE-2020-15223，修复版本 v0.34.0）。这些历史修复不能证明当前依赖树安全。go-oauth2 的公开 advisory API 当时返回空数组，也不表示没有漏洞。

本阶段使用标准库 `crypto/rand`、SHA-256、constant-time comparison 和已有 GORM；**没有修改 go.mod/go.sum，没有新增协议依赖**。代价是团队需要自行承担这个窄协议实现的审计、回归和标准变更维护。公开接线前仍需独立安全审计与客户端互操作测试。

当前供应链检查有一项阻塞风险：

- `go mod verify` 通过，只证明已下载模块的校验和一致。
- `govulncheck v1.8.0 ./oauthserver` **未通过**：报告 [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970)，现有 `golang.org/x/text v0.37.0`，修复版本 v0.39.0。扫描给出经 GORM transaction / 动态数据库驱动 / ClickHouse HTTP / IDNA 的静态可达路径。核心只接受 SQLite/PostgreSQL，但本轮没有据此将报告标成误报，也没有完成可利用性审计。
- 另有 6 项 module-level finding，扫描未发现核心调用路径：GO-2026-5841、5932、5942、6303、6354、6355，涉及已有 compress、x/crypto、x/net。未在本任务中升级共享依赖。
- `-json` 模式即使报告 finding 也可能退出 0，不能用该退出码宣称安全。文本模式本次退出非零。

## 协议范围

依据正式 RFC，而非“已符合 OAuth 2.1”的声明：

| 标准 | 本阶段边界 |
| --- | --- |
| [RFC 6749](https://www.rfc-editor.org/rfc/rfc6749) / [RFC 9700](https://www.rfc-editor.org/rfc/rfc9700) | 授权码 + 强制 PKCE；短期 access；refresh rotation、重用全族撤销；实时权限策略；禁止 implicit/password/client_credentials 等其他 grant。 |
| [RFC 8252 §7.3](https://www.rfc-editor.org/rfc/rfc8252#section-7.3) | 仅预注册 native public client；仅 `http://127.0.0.1` loopback，已登记路径，端口可变。客户端必须使用外部浏览器，后续插件验证。 |
| [RFC 7636](https://www.rfc-editor.org/rfc/rfc7636) | 仅 S256；challenge 严格 32 字节 raw base64url；verifier 为 43–128 个 unreserved ASCII 字符。 |
| [RFC 9207](https://www.rfc-editor.org/rfc/rfc9207) | 成功、拒绝及安全的协议错误重定向含固定 `iss`；客户端必须精确检查 issuer 和 state。 |
| [RFC 8414](https://www.rfc-editor.org/rfc/rfc8414) | 元数据只声明实际支持的 code/query、authorization_code/refresh_token、none、S256；不声明 OIDC 或 DPoP。元数据方法只返回拟接线端点，不证明它们在线。 |
| [RFC 7009](https://www.rfc-editor.org/rfc/rfc7009) | 撤销 access 或 refresh 都撤销整族；未知/其他 client 的 token 返回相同成功结果；不信任 hint；存储失败必须返回错误。 |
| [RFC 8707](https://www.rfc-editor.org/rfc/rfc8707) | 本 profile 只允许一个预注册 HTTPS resource；authorize、code exchange、refresh 必须提供并精确匹配；不允许切换或扩大 audience。 |

这是一个比通用 OAuth 更窄的 profile，不是完整 OAuth/OIDC 实现或认证结果：

- issuer 必须为固定、规范的 HTTPS DNS origin，不允许 path、端口、userinfo、query、fragment；由可信启动配置指定，不读取 `Host`、`Forwarded`、请求参数。
- native redirect 注册为**无端口模板**，例如 `http://127.0.0.1/oauth/callback`。授权请求可使用 `http://127.0.0.1:49213/oauth/callback`。兑换必须提交授权时的完整 URI，不能再换端口。
- 禁止 localhost、IPv6、其他 127/8 地址、外域、userinfo、query/fragment（含空 `?`/`#`）、百分号编码路径、大小写替换、dot-segment、反斜线和非规范端口。
- scope 为明确登记的 token 集合；规范化排序，不推断层级或通配符。refresh 可缩小 scope，后续成员不能恢复已丢弃的 scope；subject、client、resource 和 redirect 均不变。旧 access 的原 scope 可保留至其自身失效，但仍逐次执行当前权限检查。
- 要求 16–512 字节可打印 ASCII state；客户端仍须用 CSPRNG 产生足够熵，本服务无法从长度验证随机性。
- 原始 query/form 限 16 KiB；拒绝重复参数（即使值相同）、多 resource、空字段、非法编码及未列入本 profile 的字段。不会把 query 和 POST body 合并后再验证。
- 不开放匿名动态客户端注册；不支持 client secret、Basic、JWT client assertion、URL bearer、OIDC ID token、隐式授权或密码模式。

## API 契约

授权、兑换、撤销与 access 校验要求调用方传入有期限的 `context.Context`。服务不会注册路由或自动执行迁移。

| API | 输入与输出 | 信任边界 |
| --- | --- | --- |
| `model.MigrateOAuthServer(db)` | 创建四张独立表，可重复调用。 | 后续由主应用显式注册；仅支持 SQLite/PostgreSQL。 |
| `New(db, Config, Policy)` | 静态 client registry、固定 issuer、refresh 生命周期，返回 `*Server`。 | 必须传权威写库和真实、并发安全的 Policy。拒绝外层 SQL transaction、prepared transaction、DryRun；必须由服务掌握实际提交。 |
| `Metadata()` | 返回拟接线 metadata。 | 将来挂 `/.well-known/oauth-authorization-server` 前先完成全部端点。 |
| `BeginAuthorization(ctx, rawQuery, browserBinding)` | 返回 `PendingAuthorization`，含一次性 transaction capability、不可变授权摘要和 5 分钟到期时间。 | browserBinding 必须由可信浏览器会话提供，不能取自请求字段。此时没有用户或授权码。 |
| `TrustedPrepareConsent(ctx, transaction, browserBinding, authenticatedUserID)` | 绑定已认证用户，返回一次性 `Consent.Secret` 与授权摘要。 | **内部可信方法**。先验证真实浏览器会话/登录 CSRF，再传用户 ID；不能暴露为“提交 userID 即批准”的外部 API。不能重复绑定或换用户。 |
| `TrustedApprove(ctx, transaction, browserBinding, consentSecret)` | 单次消费同意，创建 family/code，返回带 code/state/iss 的已验证 redirect。 | HTTP 适配层仍须检查当前会话身份、同源 POST、CSRF，并取得用户对该摘要的明确同意。此方法没有可替换的 userID/scope/client/resource/redirect 参数。 |
| `TrustedDeny(...)` | 单次消费同意，返回 access_denied/state/iss redirect。 | 与批准相同的会话与 CSRF 边界；不创建 code 或 family。 |
| `Exchange(ctx, rawBody, SenderBinding{})` | code 或 refresh 兑换，返回 `TokenResponse`。 | 传原始 form body，HTTP 先用 `ReadPublicClientForm`；刷新重放返回 invalid_grant，同时提交全族撤销。 |
| `Revoke(ctx, rawBody)` | 成功返回 nil；重复/未知 token 幂等。 | 原始 form 包含 client_id/token，可选 token_type_hint；public client ID 只是标识，不是客户端身份证明。 |
| `ValidateAccess(ctx, AccessRequest)` | 返回受限的 Grant。 | 内部资源服务接口；resource 和 RequiredScopes 必须由可信路由策略指定，而非请求者自选。逐次读写库并运行 Policy；不提供公开 introspection 路由。 |

### 授权页必须完成的会话绑定

1. 为浏览器认证会话维护至少 32 字节 CSPRNG 来源的高熵 binding，将它留在可信会话存储中。不要把任意 user ID、公开 transaction ID、可伪造 cookie 或表单字段当作 binding。
2. `BeginAuthorization` 返回的 transaction 与该 binding 绑定。完成登录后，适配层验证当前身份，调用 `TrustedPrepareConsent`。如果登录流程必须旋转认证会话，应在可信服务端保留/迁移这段预认证事务上下文；不能让浏览器提交新的绑定值。
3. 授权页展示服务返回的 client、loopback callback、resource、全部 scope。`Consent.Secret` 只交给该可信会话的页面，禁止写入 URL、日志或公开存储。用户身份切换、注销或 binding 变化必须废弃当前事务并重开；最终提交还要验证当前身份与绑定的 subject 相同。
4. 取得同意后，同源、CSRF 保护的 POST 调用 `TrustedApprove`。仅有 userID、transaction、甚至另一事务的 consent secret 都不足以批准。`Trusted*` 的 Go 可见性不是网络认证措施，不能将其直接绑定到外部 JSON endpoint。
5. 返回的摘要对象、Policy 收到的 Grant 都是值/切片副本。修改这些对象不能修改数据库中的用户、权限或回调。

Policy 在身份绑定、批准、code exchange、refresh 和每次 access 校验时检查当前用户状态及授权。它只能允许或拒绝，不能改写签发内容。Policy 应使用有界、快速的本地权限查询；不要在持有 OAuth 数据库锁时做慢速外部请求，也不要递归修改 OAuth 存储。Policy 错误一律拒绝，不默认为批准。

### HTTP 适配层

- `ReadPublicClientForm` 只允许 POST/form-urlencoded，拒绝 URL 参数、Authorization/DPoP header、压缩 body、重复 Content-Type 和超限/读取失败的 body。随后必须调用 `Exchange` 或 `Revoke`，由协议校验拒绝 body 中的 client_secret/client_assertion 等禁用参数。
- `BearerFromRequest` 只允许一个 `Authorization: Bearer lmm_at_…`。拒绝 URL credential、重复/混合 header、其他 token 类型和未实现的 DPoP。资源服务不得再将请求交给会接受 API Key、dashboard JWT、body/query bearer 的回退路径。
- `ProtocolError` 的 JSON 只包含安全的 error/error_description；按 `Status` 返回。授权阶段只有当 core 已验证 client 和 redirect 后，才会设置可重定向的 `ProtocolError.RedirectURI`。为空时显示本地错误，**不得从原请求自行构建 redirect**。
- `SensitiveResponseHeaders()` 提供 no-store、no-cache 和 no-referrer，成功与失败都要设置。HTML 还需 CSP、`frame-ancestors 'none'`、HTML 转义，禁用第三方页面资源。
- 尚未实现 HTTPS/反代信任检查、Origin/CSRF、请求超时、速率限制、账号锁定策略、响应写入、CORS、日志脱敏、用户授权管理界面和运行时指标。挂路由前必须实现并测试。

## 存储与事务

`model/oauth_server.go` 定义：

- `oauth_server_authorizations`：browser-bound pending/consent，5 分钟，单次消费。
- `oauth_server_grants`：user/client/redirect/resource/初始 scope、绝对过期时间、整族撤销标记和锁版本。
- `oauth_server_codes`：code digest、PKCE challenge、120 秒期限和 used tombstone。
- `oauth_server_tokens`：access/refresh digest、scope、有效期、refresh used tombstone。

code、access、refresh、transaction 和 consent 都使用 `crypto/rand` 产生 256 位随机值。数据库只保存 SHA-256 摘要；browser binding 保存带域分离前缀的摘要。数据库不保存可恢复的 token 密文。family ID 是不能单独授权的随机标识，不是 bearer token。模型中保留了将来 sender binding 的字段，非空值目前会拒绝使用。

code TTL 固定 120 秒，单次使用。access TTL 固定 10 分钟，接近 family 绝对期限时缩短。refresh 默认绝对 30 天、idle 7 天；可配置范围为 `10m <= idle <= absolute <= 90d`。绝对期限从批准创建 family 起算，不随兑换或刷新延长；每次 replacement refresh 的期限为 `min(now + idle, absolute)`。Policy 查询结束后会重新检查期限，不能用慢查询延长授权能力。

修改 family 的事务以 `UPDATE lock_version = lock_version + 1` 为第一个 SQL：PostgreSQL 锁住 family 行，SQLite 先取得写锁，避免 deferred read → write upgrade。服务在 PostgreSQL 上显式使用 READ COMMITTED，以便等待行锁后读取新的 token used 状态，不依赖数据库默认 isolation；SQLite 使用驱动默认事务级别。没有进程内互斥锁，也不依赖 SQLite 会忽略的 `SELECT FOR UPDATE`。

- code 消费/签发、refresh used 标记/新 pair 插入在同一事务中；任何存储失败都回滚，不返回凭证。
- 正确 client/resource（code 还需原 redirect/PKCE）的已用 token 重放会提交整族撤销。协议错误与数据库 rollback 分开传递，避免返回 invalid_grant 时把撤销也回滚。
- 已用 refresh 即使超出原 idle 期限，仍能触发重用检测；更改 scope 不能隐藏重放。错误 client/resource 不能借重放接口撤销其他授权。
- 两个合法客户端进程同时刷新同一个 token，也会导致一个成功、另一个触发整族撤销。首版没有 grace period；后续客户端必须对持久化凭证执行跨进程 single-flight。网络在提交后断开时，重试旧 refresh 会触发撤销，需要重新登录。
- 每次 access 检查都联表读取 token/family，并检查当前 Policy；不缓存“仍有效”。撤销不能撤回在其提交前已经通过验证的在途操作。
- 必须使用主写库，不使用 read replica、读写分离 resolver 或事后可能回滚的外层事务。数据库不可用时 fail closed。生产需要配置连接池、锁/语句超时和可靠时钟。

没有自动清理任务。未来清理应先处理到期 pending，按 family 的绝对期限/撤销与审计保留策略整族删除；**不能只按 used refresh 自身 idle expiry 删除 tombstone**，否则仍存活的后代会失去重放检测。日志不得记录 query、表单、callback code、state 或令牌响应。

## DPoP 与未满足事项

**没有实现 DPoP、mTLS 或其他发送方约束。** `SenderBinding` 是未来可信 proof 验证层的扩展点，当前只接受空值；请求/数据库中的非空 binding 都 fail closed。metadata 不声明 DPoP 支持。

首版使用短 access + refresh rotation。被盗的有效 access 在其到期/撤销前仍是 bearer；轮换也不能阻止首次使用被盗 refresh 的攻击者暂时获得新凭证。尚未验证 Pi credential storage、多进程刷新、loopback 监听、state/issuer 检查、DPoP 的协议与客户端兼容性。公开服务前需要处理上述依赖扫描、独立审计和这些集成项目，不能据单元测试宣称生产安全或 RFC 全面合规。

还未完成：生产迁移注册、静态 client/issuer 的最终运维配置、Web 授权页面/会话适配、真实用户 Policy、relay 分组权限与计费绑定、控制台撤销、客户端安装和发布。

## 测试

在 `apps/api-go` 下：

```sh
go test ./oauthserver -count=1
go test ./oauthserver -race -count=3
go vet ./oauthserver
go mod verify
```

默认跑 SQLite；没有 `OAUTH_SERVER_TEST_POSTGRES_DSN` 时会明确 skip PostgreSQL。要验证 PostgreSQL，显式设置指向**隔离本机测试实例**的 DSN，再运行同一命令。测试会为每个 case 创建随机 `oauth_stage1_*` schema 并清理，使用两个独立连接池。不得指向生产库。测试 harness 拒绝远程主机；本机也可能是生产库，仍需操作者确认隔离。

本次真实验证（2026-09-13 本地日期）：

- Go `go1.27.1-X:nodwarf5`，PostgreSQL **18.6**，以及项目现有 SQLite driver。
- 临时 PostgreSQL 只监听 `/tmp` 下独立 Unix socket，关闭 TCP 监听；测试结束已停止。没有连接生产数据库。
- **37 项顶层测试**；两种数据库及子测试合计 **211 个 pass 事件，0 fail，0 skip**。
- `-race -count=3`：**633 个 pass 事件，0 fail，0 skip**。覆盖跨连接池 code 竞争、refresh 竞争、refresh 与撤销竞争、单次 consent。
- 核心包语句覆盖率 **93.7%**；`go vet ./oauthserver`、`go mod verify`、目标 LSP 检查通过。未跑全仓库测试，也未做真实浏览器/客户端集成或生产负载测试。
- 失败路径覆盖：PKCE/state/issuer/redirect、重复参数、用户/权限绑定、过期边界、slow Policy、refresh scope 缩小与重放、显式撤销、错误 token hint、数据库中无明文 token、事务回滚、撤销写入失败、外层事务/DryRun、保留 sender binding fail closed。
- 本机原始日志与覆盖率位于 `/tmp/lmm-oauth-stage1.sIOmSj/`（临时证据，不随仓库保存）：`tests-final.jsonl`、`race-final.jsonl`、`coverage-final.out`、`govulncheck-final.txt`、`govulncheck.jsonl`。安全扫描结果见上文，**不属于通过项**。
