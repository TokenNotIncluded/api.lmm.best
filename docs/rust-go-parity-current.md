# 当前 Go / Rust 后端对齐验收

本批完整测试、实际性能数据和测量限制见
[2026-09-24 验证记录](rust-go-validation-2026-09-24.md)。持续目标仍未完成。

2026-09-24 的初始盘点：**当前 Go 有 704 条接口，Rust 尚未达到 100% 对齐**。
旧的 353 条冻结路由检查可以通过，同时当前 Go 的 116 条接口没有 Rust 路由声明。
下文保留初始盘点和各批次的历史数字；最新工作区审计见下一节。不能将“旧接口形状齐全”
当作完整后端验收。

后端参考版本为 `5dbe9206d847042964dd4b4d01bfb7f7351eb03f`。盘点期间 HEAD 更新到
`9aadba0183e354b69e238b2f1814fc0445b3d179`，两版本之间 `apps/api-go`、`apps/api-rust`
没有提交差异。后续 HEAD `e830334b4` 是独立 Go 异步 billing 测试清理；上述清单参考仍保留初始哈希，新增 oracle 每次执行当前工作区 Go。本文测试数字记录各批次当时的工作区结果；代码是否已提交须核对 Git 修订，测试本身不证明发布或部署。
运行 `cmd/route-manifest` 生成的当前 Go 清单 SHA-256 为
`f1185d13e7bed77777a8fa938825dd1cabcc55e3e93cda5d5d2bc014d42f10d5`。
冻结的 `legacy-go-routes.tsv`、其哈希和生产所有权要求保持不变。

## 2026-09-27 工作区路由审计

当前执行 `check-go-route-manifest.sh`：Go 清单 **704** 条；Rust `implemented` 台账
**520** 条（冻结清单内 325 条、新增 195 条）；fail-closed shell **26** 条。
冻结路由的生产所有权仍是 Rust 0 条、Go 353 条。
`audit-current-routes.py` 报告普通监听器挂载台账 **518** 条、尚缺 **186** 条。
该工具对源码的工作区扫描会读入本次提交范围外的获客草稿，不能把它的源码候选数字当作
提交结果；此前批次的 **609** 条候选声明、缺 **95** 条仍待干净工作树复核。
implemented 与普通挂载台账计数口径不同，均不能证明
实际监听器可达、依赖可用或行为与 Go 一致；差分行为的完整验收仍未完成。

本地获客草稿不在本次提交范围；Go 的 22 条获客接口仍全部缺少普通挂载，不能把草稿
计作已交付功能。上述数字是本次工作区静态审计，不代表发布或生产切换。

## 初始差距

| 口径 | 数量 | 能证明的范围 |
| --- | ---: | --- |
| 当前 Go 接口 | 704 | Gin 实际注册的 method/path/handler |
| 当前 Go 中冻结清单以外的接口 | 356 | 新增接口，不能由冻结清单验收覆盖 |
| Rust 有候选源代码声明 | 588 | 包括服务不可用的占位组合，不等于业务完成 |
| Rust 缺少源代码声明 | 116 | 必须新增实现 |
| 普通监听器挂载台账覆盖 | 492 | 仍需验证真实主程序接线和服务能力 |
| 普通监听器 fail-closed 占位 | 31 | 接受路由，但真实业务依赖未启用 |
| 普通监听器台账未挂载，且非上述占位 | 181 | 包含源代码缺失和台账过时 |
| 已有候选源代码，但 implemented 台账未登记 | 63 | 需逐条核对，不能直接批量“认证” |
| 当前 Go 自身返回 501 的兼容接口 | 12 | 仅当当前 Go 仍是 501 才可作为兼容例外 |

初始盘点时 `check-complete-route-coverage.sh` 分类为 482 个 differential-candidate、
179 个 static-only、31 个 mounted-fail-closed-shell、12 个 legacy-501。
未输入独立差分结果时 `differential_verified=0`。该脚本的 `coverage_complete=true`
只表示每条当前接口都被分类；`static-only` 分类本身并不意味着实现完成。

116 条源代码缺失按当前业务分组：

| 模块 | 缺少接口 |
| --- | ---: |
| 工具市场 | 26 |
| 获客统计、来源、同意和修正记录 | 22 |
| 助手人工支持、运行密钥、注册检查 | 12 |
| 用户公告、分享、返佣、会话设置、兑换、Passkey 删除 | 10 |
| 脚本列表、下载、编辑、仓库同步 | 9 |
| 红包和封面绘制 | 8 |
| AI 目录与广告 | 6 |
| Signal 游戏 | 6 |
| 绘图密钥和 MCP 凭证 | 5 |
| Pi 远程控制会话/消息 | 4 |
| 价格通知与投递重试 | 3 |
| 公开分享图 | 1 |
| 更新检查 | 1 |
| 运行价格配置 | 1 |
| `/v1/pricing`、`/v1/usage` | 2 |

63 条“有源代码、无实现台账”来自 HeroSMS 25、公共中转站 16、折扣码 8、订阅 7、
账号操作 7。应核对真实调用链、接线、数据库迁移、权限和成功路径，不能只补 TSV。

本批公告 3 条、脚本 9 条和会话设置 1 条实现并接入普通监听器后，当前 Go 源声明缺口降为
**103 条**。ePay 3 条改用真实 PostgreSQL/Valkey 依赖并通过本批支付事务与 Go fixture 测试后，
普通挂载台账为 **508 条**，fail-closed 占位为 **28 条**。新 ePay 静态检查明确拒绝缺失缓存、
未 merge 或重新接入 Disabled adapter；冻结的生产所有权门禁不变。每次实现继续用上述审计器刷新，
这些数字仍不等于行为对齐数量。

## 第二批已验证的目录和用量接口

AI 目录/广告 6 条接口已接入普通监听器，7 条独立测试全部通过：报价与 URL 清洗使用当前
Go 实际导出的 60 + 22 个向量；4 条 PostgreSQL/Valkey 测试覆盖并发下单只扣一次、请求号重放、
报价变更拒绝、到期/隐藏过滤、分页、权限、一次性全额退款，以及缓存/审计失败不能逆转资金。
新增 `0015_current_catalog.sql` 只增加广告表及必要索引；迁移验证继续检查精确类型、默认值与唯一约束。

`GET /v1/usage` 已接入普通监听器，共 4 条测试通过，其中 3 条使用真实 PostgreSQL/Valkey。
读取仅限当前 API key 和所属用户，保留 UTC 当日窗口、不限额 null 语义；不更新 key、状态或访问时间。
多 API key 和多个实例共用同一用户的查询限流。`GET /v1/pricing` 已完成本轮严格 Go 差分复测，
整个 token_queries target 的 6 条真实 PostgreSQL/Valkey 测试全部通过，包括按 key/用户组披露、
动态价格设置、信任折扣和不修改凭据。数值响应对齐 Go 的整数与指数格式，独立的 14 个当前 Go
浮点编码向量也已通过。至此当批源码声明为 609 条，仍缺 95 条；现场 CI 又完成 Stripe 当前 Go 钱包/订阅参考导出与 Rust 10 条真实测试后，普通挂载台账
为 518 条，shell 降为 26 条；该批清除 Stripe 钱包下单和共享 webhook 两条占位，
既有金额查询不重复计数，订阅 checkout 仍保留真实阻塞分类。

共享信任聚合的真实 PostgreSQL 回归已通过：使用当前 Go 的实际到账额度、旧订单单位回退、
退款状态及内部积分排除规则，统一 dashboard、价格和 Relay 的等级/折扣计算。共享 token 缓存
回归也已通过：用 PostgreSQL 行锁制造“旧读取等待发布、修改等待写入”的交叉窗口，验证写库前
Valkey fence 阻止旧快照回填，完整 hash 只续期且不会恢复耗尽额度；正常 token 更新与删除先失效
缓存，历史刷新只在独立冻结监听器启用。

持续集成新增 `epay`、`stripe`、`catalog`、`token-queries`、`shared-trust`、`token-cache`、
`relay-settlement`、`scripts` 入口，并由 `all` 执行。ePay 与 Stripe 的 12 / 10 个 PostgreSQL
用例分别选择；Stripe 子模块递归扫描，编译产物必须与源码逐名一致。目录、价格、钱包、订阅
和 Relay 资金参考均由本次当前 Go 导出到新临时文件，验证案例完整性后传给 Rust；不接受继承的
旧输出路径。`0013` / `0014` / `0015` 的 4 条数据库契约测试也已加入 migration 入口。

```sh
# 环境必须是已准备的独立 loopback PostgreSQL / Valkey；缺少环境直接失败。
bash apps/api-rust/tests/scripts/run-real-integration-gates.sh all
# 这些是无数据库、无编译的门禁自身回归，不等于真实业务测试通过。
bash apps/api-rust/tests/scripts/check-real-integration-gates.sh
```

## 可重复的当前路由审计

```sh
# 可传已有清单避免重复编译 Go；省略 --manifest 会运行当前 cmd/route-manifest。
GOMAXPROCS=2 GOFLAGS=-p=2 python3 apps/api-rust/tests/scripts/audit-current-routes.py \
  --report-dir /tmp/lmm-current-route-audit

# 有当前接口缺失/硬占位时必须非零退出。
python3 apps/api-rust/tests/scripts/audit-current-routes.py \
  --manifest /tmp/lmm-current-route-audit/current-go-routes.tsv \
  --report-dir /tmp/lmm-current-route-audit --require source

# 在上述基础上，任何未挂载接口或 disabled shell 都必须非零退出。
python3 apps/api-rust/tests/scripts/audit-current-routes.py \
  --manifest /tmp/lmm-current-route-audit/current-go-routes.tsv \
  --report-dir /tmp/lmm-current-route-audit --require mounted

python3 apps/api-rust/tests/scripts/test-audit-current-routes.py
```

输出保留完整的当前 Go TSV、Rust 源码 JSONL、现有分类 JSONL、逐条 JSON/Markdown 报告，
以及静态检查日志。源代码信息复用已有路由扫描器；新增的可选机器输出不改变旧门禁判据。
报告模式返回成功只说明报告生成成功。`source`、`mounted` 都是静态条件，均不认证行为对齐。
`--require source` 在初始盘点中按预期失败，缺失 116 条；7 个门禁回归测试通过。

## 防止测试通过但业务仍不可用

- `all_routes_contract` 校验路由模块/测试文件/构造函数存在，同时调用冻结静态门禁。
  它没有逐条执行当前 Go 的 704 个成功业务流程。
- `root-route-acceptance` 使用 synthetic auth、空状态和不可连接的 lazy PostgreSQL/Valkey，
  主要证明历史核心 root 的路由、权限和边界；它没有复现 `main.rs` 的完整普通监听器依赖图。
- `route-compatibility-matrix.tsv` 当前 54 条场景中，52 条是匿名传输边界，2 条是错误 webhook。
  这些测试必须保留，但匿名 401 与无效签名拒绝不能替代有效凭证和成功业务测试。
- 12 个 Go 原生 501 例外必须继续核对当前 Go handler，不能将它们扩展成任意 Rust 501 白名单。
- `DisabledCheckoutProvider`、`DisabledStripeCreemGateway`、`DisabledTopupRepository`、
  `DisabledWaffoWebhookProcessor`、`FailClosedRelayCompatService`、`FailClosedRelayVideoService`、
  `UnconfiguredResponsesWebSocketService` 等初始监听器依赖必须逐项替换并测试成功路径。
  条件性禁用和外部未配置状态应与 Go 的同配置状态对照。
- MCP 的路由形状也不能证明工具执行、账务、第三方授权和流恢复已实现。
- 当前 Go token 管理模型拒绝修改助手运行密钥，并将 OAuth managed key 排除在普通修改/删除之外。
  2026-09-24 批次记录了 Rust 冻结版 CRUD 筛选缺口；此后 `api_token/current.rs` 已加入
  来源筛选、managed key 排除及运行密钥修改拒绝代码。一次性 reveal 和这些管理权限仍需
  当前 Go 对照与真实数据库验收；缓存 fence 与余额回归不能替代这类测试。

## 验收批次

1. 当前清单、真实主程序组成、模式开关和数据库表结构先统一。每批完成后重跑当前清单。
2. 无外部网关的用户/管理业务：公告、会话、脚本、目录、兑换、游戏、远程控制。
3. 资金业务：充值、订阅、折扣占用/释放、返佣、退款；验证行锁、幂等和不可变支付证据。
4. Relay：Playground、SSE、WebSocket、Realtime、图片/视频和任务生命周期；验证重试和取消结算。
5. 工具市场、MCP、红包、获客、后台任务：验证权限、跨账号隔离、工作队列与单实例任务所有权。
6. 独立双后端差分、故障注入、持续负载以及相同数据/机器下的内存和性能对比。

每个业务批次需要下列场景和证据，不能只用“测试数量”验收：

| 维度 | 至少需要 |
| --- | --- |
| HTTP 合约 | 状态、JSON 字段/类型/空值、相关头、错误、参数、大小限制、路径/方法 |
| 身份与授权 | 匿名、无效/过期/撤销凭证、L0/L1、普通用户/admin/root、跨账号拒绝 |
| 成功业务 | 相同种子数据和配置下 Go/Rust 输出及数据库/缓存副作用一致 |
| 一致性 | 并发、重复请求、重放、顺序颠倒、事务回滚、缓存失败和重启后读取 |
| 流和网关 | 首字节、逐块内容、终止、取消、超时、上游失败及实际扣费 |
| 后台行为 | 周期任务、清理/过期、租约、跨进程写入、恢复和 N/N-1 schema 兼容 |

差分必须记录 Go/Rust 的源码版本、schema、配置、fixture、场景名和允许的具体差异。
不得将新接口加到排除清单、将 ignore 视为 pass，或用 Rust 自己生成的响应充当 Go oracle。

## 本批公告实现

新增 `/api/user/self/announcements`、`/api/user/self/announcements/read`、
`/api/user/:id/announcements`，对应当前 Go 的 mandatory announcement 合约。
实现发布时间/ID 排序、未来/非强制公告过滤、Go JSON 编码的 SHA-256 revision、
管理员控制的 `ackRevision`、同账户事务行锁、最早未读顺序、幂等、跨账号隔离和 admin/root 权限。
`0011_mandatory_announcements.sql` 只增加读记录表；旧 revision 不重写。

```sh
cd apps/api-rust
cargo test --locked --lib routes::mandatory_announcements
cargo test --locked --test mandatory_announcements
# 必须是独立测试数据库；缺少环境变量会失败。
LMM_MANDATORY_ANNOUNCEMENTS_TEST_DATABASE_URL=... \
  cargo test --locked --test mandatory_announcements -- --ignored --test-threads=1
LMM_TEST_DATABASE_URL=... cargo test --locked -p lmm-db-migrate \
  --test current_parity_schema -- --ignored --test-threads=1
```

这些测试覆盖本模块，不等于完成全后端验收。`0012_payment_runtime.sql` 的 schema verifier
同时检查支付事件/交易双唯一键、折扣占用和返佣流水的重放约束；对应迁移故障测试拒绝
非唯一、部分、缺失或错误列的索引以及被改变的默认值/空值/序列。

## 本批支付范围

普通监听器的 ePay 三条路由已接入 `PgEpayRepository`、`PgEpayGateway` 和 Valkey。
下单保留六位小数，保存平台数量、到账额度、支付金额和币种的不可变快照；先提交 pending
订单，再生成支付参数。支付签名使用原始字段字节，配置和合规状态按当前 Go 门禁读取。
回调必须匹配金额、方式、provider 和唯一交易证据；未知订单、错误金额或数据库失败返回
`fail`，不会仅因签名合法便应答成功。钱包、优惠券占用消耗、首充标记和返佣流水在同一事务内完成。
缓存失效和充值日志在提交后尽力执行，失败不会逆转已完成的资金，也不会让重放重复充值。

ePay 的 12 条真实 PostgreSQL/Valkey 测试已经通过。后者包含一项执行
15 个当前 Go 共同 fixture 的对照：`RequestEpay` / `EpayNotify` 的成功与业务拒绝响应、
结算快照、checkout 签名、错误签名、金额不符、成功回调、重放、钱包和充值日志条数。
例如请求 `5.25`、人民币基础比例 `2` 时，当前 Go 的中间除法舍入导致实付 `2.62`；
fixture 保留这个实际行为，而不是以理论上的 `2.63` 替代 Go 结果。

```sh
# 在仓库根目录执行；现场运行当前 Go oracle，再启动一次性 PostgreSQL/Valkey 对照 Rust。
CARGO_BUILD_JOBS=2 python3 apps/api-rust/tests/scripts/epay-current-differential.py \
  --output-dir /tmp/lmm-current-epay-differential
```

Go 参考执行使用原生 controller 测试中的内存 SQLite，Rust 使用隔离 PostgreSQL 18。
这组结果证明所列 fixture 的业务和持久化结果，不能证明两种数据库驱动的全部错误行为，
也不能比较两种数据库下的测试耗时。输出保留 Go 参考、源码 revision、fixture 数量和两端日志；
脚本要求 Rust 精确命中一项含全部 fixture 的测试，零测试不算通过。

以下三个输入差异已修复，并纳入上述 15 个共同 fixture：

| 输入 | 当前 Go 与 Rust 的共同结果 |
| --- | --- |
| 正常金额/支付方式，加 `"discount_code": null` | 空优惠码，正常下单 |
| 使用 `"AMOUNT"` 字段 | 大小写不敏感，正常解析金额 |
| `"amount": 5.25, "amount": null` | null 保留此前的 5.25 |

解析器按原始 JSON 字段顺序处理重复字段，匹配 Go 的大小写和 null 赋值规则。
这证明所列输入与业务场景，仍不是整个输入域的穷尽证明。

Stripe 已实现原生钱包报价、下单、签名回调、到账和部分/全额退款，并接入普通监听器。
下单先持久化金额/币种/到账快照和优惠券占用，再调用 provider；返回 subtotal 必须与快照相同。
回调核对原始 body 签名、金额、币种和唯一支付凭据；促销实付金额独立保存。
钱包、优惠券、首充返佣和退款流水使用真实 PostgreSQL 事务，缓存与日志在提交后执行。
重复事件不重复入账，退款按累计金额计算，余额不足或流水写入失败时可重试。

共享 Stripe webhook 也处理已有订阅订单的首次开通、账单先于 checkout 到达、续费、历史账单、
取消和支付失败。套餐快照决定已购买的权益，续费提升 quota_version，旧事件不覆盖新周期用量。
当前 Go 的首次授权与回执记录分两次提交：回执存储失败返回 500，但首次权益已存在；重试补齐
回执，不再次授权，也不清空期间产生的用量。Rust 已按这一边界实现，并加入同一故障 fixture。

本轮支付 PostgreSQL 测试为 **22/22 通过**（ePay 12、Stripe 10），包括 Stripe 钱包及订阅的
当前 Go 对照。订阅 exporter 实际执行 10 次成功回调，以及一次回执故障和重试；Rust 比较同一份
结果，确认首次权益保留、回执重试成功、没有再次授权，且重试保留期间消费的 31 点额度。
这包含两阶段故障恢复和订阅生命周期的真实数据库验证。普通监听器已接线，完整路由组合测试和
普通进程启动冒烟均已通过；Stripe 业务使用本地 provider 夹具验证，未发起外部真实支付。

```sh
# 现场导出当前 Go 的钱包及订阅结果，再各执行一项精确命中的 Rust/PG 对照。
CARGO_BUILD_JOBS=2 python3 apps/api-rust/tests/scripts/stripe-current-differential.py \
  --output-dir /tmp/lmm-current-stripe-differential
```

**`/api/subscription/stripe/pay` 的原生订阅下单仍使用 Disabled checkout，尚未完成。**
Stripe SDK 的网络重试等边界、Creem、Waffo/Pancake 的真实下单和完整生命周期也仍需继续对齐。
已有测试不能证明这批代码已部署，也不能用来声明整个 Stripe 或整个后端已经达到 100%。

## 会话与测试依赖执行

会话普通监听器现在复用登录/refresh 的 `DashboardAuth` 权威实现：列表先做周龄清理，过滤
旧 auth_version，当前会话置顶，其余按活动时间排序；设置更新只修改 `session_auto_logout`；
单个/批量撤销先写共享 Valkey deny fence，再提交 PostgreSQL，并仅清除匹配 SID 的 refresh cookie。
新增真实 PostgreSQL/Valkey 测试覆盖上述行为、未知设置保留、跨用户拒绝、重复/并发删除和缓存故障。

```sh
python3 apps/api-rust/tests/scripts/run-all-ignored.py \
  --output-dir /tmp/lmm-backend-dependency-tests --fuzz-seconds 30
python3 apps/api-rust/tests/scripts/test-local-services.py
```

这个执行器从编译产物枚举全部 ignored 测试，并显式纳入 12 个此前没有标记 ignored、
但缺少依赖时会直接返回成功的测试：HeroSMS 2、状态读取 1、助手密钥 5、安全卡片 2、
充值完成 1、Go 签名收据互操作 1。这些测试实际执行时不传 `--ignored`，依赖字段缺失或
编译清单中缺少名字会失败，不能算作空运行成功。收据 fixture 由当前 Go exporter 现场生成。

每条测试使用单独的临时数据库，缓存单条测试前清空；所有地址指向新建 loopback 服务。
执行器清除继承的 libpq/DB/Redis 目标，检查 PostgreSQL data_directory 确实属于本次启动的进程；
adoption fixture 设置精确的 public search_path，保持生产的严格校验。umask subprocess helper
通过原父测试执行，由父测试提供私有目录、设置 umask 并校验文件权限。profile、rankings 和
token-owner 查询使用真实基线表；Relay 外部边界使用托管的本地 provider，保留命中记录。

首轮依赖执行实际选中 144 条，137 通过、7 失败；这批失败揭示了 fixture 缺口、schema 元数据漂移、
收据格式与 profile 缓存等真实问题，必须修复后复测。初始非依赖 suite 中条件性早退的测试不能
当作已经通过数据库/外部边界验证。最终结果以执行器 `inventory.json`、`results.json` 和单测试日志为准。

## 内存与性能验收边界

必须用同一主机、相同数据库种子/配置/上游固定响应、相同构建模式与请求集合对比。
分别测冷启动、空闲 RSS/PSS、稳态内存、峰值内存、吞吐、CPU、p50/p95/p99、错误率、
流首字节和取消后释放；记录并发数、预热和持续时间。每组重复多次并保留原始采样。
在同一个依赖不完整的监听器上得到更少内存或更快的 401/503，不能据此宣称业务性能更好。
共享依赖进程占用与后端自身占用应分别报告，并说明是否包含数据库/缓存/负载生成器。
