# main 增量与微服务主线对齐核查

[文档目录](README.md) · [主线交接](development-branches.md) · [架构状态](core-migration.md)

## 最新同步：2026-10-13 钱包、商店与 Pi 显示设置

以下增量已在本机审查、验证并正常推送，不是候选部署或整站验收：

- `6b3086de5`：Rust 个人 Web 钱包只读投影直接读取当前权威 ledger/balance_state，
  可用额度、预留和债务分开；不再因缺少导入历史计数而丢弃已证明的钱包。
  原生无资金钱包仅凭注册/bootstrap 审计依据显示零，导入数据缺依据仍 unknown；
  没有虚构 USD/CNY 换算。真实隔离 PG 与相关调用方 **27 项通过**，
  Clippy/范围格式通过；Rust **1.98 补充验证**，不是正式 1.99 或全仓验收。
- `03ce0da8c`：Go 商店只读列表保留合法 native/legacy 混合记录，明确 record_kind、
  原价格单位和未知时间；订单计数/分页在同一 SQL statement snapshot，权限隔离保持。
  Store/webcompat **117 个顶层测试通过**，真实隔离 PG **2 个顶层回归通过**，
  race/vet/格式通过。基线 overlay 在同一混合 fixture 确实失败，修复后通过；
  没有删记录或将失败变成空列表。原生无 legacy checkout 能力的商品仍只读。
- 独立 Pi 插件 `9ea8fc8cd` 已推送并更新本机：`/lmm-currency credits|usd|cny [rate]`
  持久化钱包显示偏好，CNY 只用显式手动汇率并标估算，不改实际计费或授权预算。
  价格报告先列 selectable chat models，区分表达式计费、只读项目与 Pi reference cost，
  不把未知价格当免费、不推断图像/语音模型为聊天模型。**142 项测试**、类型检查、
  loader/打包检查通过；本机 Pi **1.1.0** 隔离 offline 加载确认新命令注册。
  已运行会话需 `/reload`；未替用户设置币种或人民币汇率。

钱包与商店前端接线及实际组件浏览器验证由两路独立子 agent 继续进行，**尚未验收或
提交**。尤其不能把 native currency minor amounts 当 credits、把未知时间当 1970、
或把 `financial_projection_available=true` 当历史用量/推荐奖励均已知。
原候选进程和数据库仍保持原版本，**49 credits 扣费及 280 待核对预留未操作**。
没有生产变更、候选二进制替换、新付费模型请求、发布或远程 CI。
证据在 `business-projection-parallel/{wallet,store}-result.md`；父 agent 逐一核对了
11 个后台源码/测试 SHA256 与已完成检查输入一致，未重复堆叠同一检查。

## 最新交接：2026-10-13 独立候选与源码状态

本节覆盖下文旧固定 WIP 的相应验收缺项，不删除历史记录，也不改变传统 main
`3948a0a1` 的历史比较范围。本轮入场源码为 `b70ae815bd60c957de9b18c45b96e61d0433d514`，
分支仍为 `wip/rust-core-go-extensions`；有其他贡献者未提交工作，不能称工作区干净。
三路子 agent 分别处理文档、归档和健康/Issue；父 agent 读回结果并收集本轮文档与
插件指针修改。没有变更生产服务或操作旧库，也没有重新发布或安装候选。

### 已提交、已发布与候选运行不是同一状态

- 已提交源码：`41e52ede49` 实现 Google 内置浏览器 OAuth 预设；`f6081b14f8`
  修复 append-only funding evidence 的运行权限与未知用量处理；`6583c76278`
  加入源码锁定的 GHCR 预览发布。依据为证据根下 `google-integration/commits.json`。
- 入场源码 HEAD `b70ae815bd` 修复助手礼包：Go 在缺少 Rust grant/receipt 合同时返回
  鉴权后的 `503 ASSISTANT_GIFT_UNAVAILABLE`，声明能力不可用；Web 不再提供虚假领取，
  只有已完成的持久回执才认成功，丢失响应不自动重发。不是已经发放礼包或接通资金宿主。
  `assistant-gift-safety/lmm-gift-web-tests.log` 为 **125 通过、0 失败**（5 文件），
  Go 日志记录 assistant/webcompat 两包通过；这些是本地定向回归，非候选浏览器验收。
- 已发布材料：`google-integration/registry-{core,extensions}/publication.json`
  记录 `6583c76278` 的 GHCR 预览镜像发布；正式镜像 Rust **1.99**。
  匿名拉取仍在 `anonymous-registry/{core,extensions}.log` 记录 **401 Unauthorized**，
  不能称公开匿名分发验收通过，更不能称生产上线或最新版礼包已发布。
- 候选运行记录：`deploy/runtime-v3.json` 标为 `candidate-running-not-activated`，
  独立原生候选地址 `http://127.0.0.1:55953`；Core/Web 来源为
  `6583c76278f5f9f63bf8165c4304cb0f16e9c721`，Go 复用 `fd1999a8` 的原候选
  （含当时保留的贡献者 dirty 输入），不是清洁 HEAD 构建。**候选 Go 和 Web 均不是
  最新 `b70ae815`，后续助手/远程控制修改未安装**；没有切换 `api.lmm.local`。
  原生 Core 是 Rust **1.98 补充验证/debug**，不是正式 1.99 镜像运行验收。

### 最新实测覆盖与仍保留的财务状态

`google-integration/final-core-checks/core-focused-tests.log` 七个 target 实际合计
**244 通过、0 失败、1 跳过**：169+19+1+12+28+7+8；跳过的是需 release 显式运行的
`routing_benchmark`。245 是含跳过的总数，不是通过数。对应 `core-commands.json`
中的定向 Cargo test、Clippy、build 均 exit 0；源码绑定
`artifact-commit-binding.json` 核对 Core 140/Web 2479 输入匹配 `6583c76278`。
这不是最新 HEAD 的全仓测试，也不能与历史轮次重复相加。

`deploy/report-v2.md` 保留真实内部 Core/Go/PG 的购买→结算→退款及 Chromium
余额购买/存储交付证据；其当时模型请求因 funding 权限被拒绝、0 dispatch 的结论
**只属于 v2**。v3 的 `deploy/candidate-v3/model-acceptance.json` 覆盖该窄模型缺项：
合成供应商共 3 dispatch、匿名 0 dispatch，真实内部账本扣费 **49 credits**，
140 预留中的 91 已释放；供应商拒绝与缺失用量各 **140 credits**、共 **280 待核对**。
记录钱包 199671、预留 280、journal 守恒，无不安全权限放宽。
必须保留扣费及待核对状态；不得猜测退款、删除、重发请求或更改预留/账本。
**供应商是合成服务，不是真实供应商验收。**

v3 `browser-results.json` 记录真实 Chromium/合成账号、登录 200、HttpOnly 严格
session cookie 与刷新 200，无响应 mock；未配置 Google 不展示入口，start 拒绝 403。
Google 仅内置预设实现，真实 Google 授权/MFA、管理员激活仍未验收。页面 HTML 200
不等于业务通过：财务日志/统计、充值、订阅、返利、catalog/orders 等 API 仍 503，
助手 greeting 404、完整对话与礼包资金链未验收。HumanGrant、支付网关、迁移/恢复、
CLI、负载及全站功能对齐仍缺证据，不编造追平百分比。

证据根：`~/.local/state/api-lmm-microservices-20261011/pr-issue-local-integration/`；
礼包定向日志在同级 `assistant-gift-safety/`。私密材料只留统一 Backups 私有目录，
不输出凭据或用户数据。三路实际结果已读回，分别见
`deploy/handoff-parallel/{docs,archive,health}-result.md`：

- 已将五个未引用 Google 中间构建归档到统一 Backups，逐文件校验内容、权限和链接后
  移除原文件；旧 Core 重复副本复用已有验证恢复文件后移除。计入新归档后，本轮目标
  实际释放约 **368.7 MiB**。旧 Web 仍被候选恢复配置引用，**保留未清理**。
- 归档前后五个候选 unit 均 active/running，应用 PID/二进制哈希未变化；Core、Go
  live/ready 与 Web status 的只读健康检查通过。磁盘仍 79%、余量约 198.7 GiB；
  不把并发文件活动的全盘变化归因于本轮清理，也没有新查库或重发业务请求。
- [#895 状态评论](https://github.com/TokenNotIncluded/lmm-api/issues/895#issuecomment-6129958838)
  已写入并读回，Issue 保持 OPEN。正式镜像与匿名下载回执只证明各自窄范围，
  不抵扣容器内完整业务、升级恢复或真实供应商验收。
- 独立 Pi 插件提交 `255383070c` 兼容余额 v2，显示明确的 public credits 而非 USD；
  129 个本地测试、类型检查、原生 loader 与打包检查通过。本机插件已更新，
  微服务仓库同步该子模块指针。已用原生 `/lmm-prices` 正常续接现有凭据，
  只读余额 HTTP 200/schema v2，解析后状态不再 unavailable，未输出金额或凭据。
  此为用户自身账号的插件校验，不是候选业务验收；没有新增付费模型调用或候选部署。

## 历史后续并行推进：已进入 WIP 的窄增量

以下五笔提交接续本页的历史核查，传统 main 仍固定为 `3948a0a1`。
六个独立子 agent 按文件归属并行开发、执行定向测试，父 agent 串行集成并正常推送；
没有合并候选分支的整段历史，也没有启用支付、访问生产或部署。

- `9208c5d4a`：移植 #889 的动态缓存读取价格与 #898 的密码登录协议反馈；新增
  14/13 项组件回归、既有价格/移动展示回归及 Web 类型检查通过。
- `f90912535`：移植 #899 的 RT16 隔离入口，补输入校验。实际 Chromium 的 24 个
  组件控制/配对探针通过；Python/Node 单元测试和独立 bundle 通过。Python
  Playwright 未安装，浏览器结果来自保留的私有 Node 适配器，不是 Python runner。
  **下文“RT16 入口缺失”仅描述旧固定 WIP；当前组件入口已补齐，存储链仍未验收。**
- `79f8164e8`：移植 #879 的持久收件箱、渠道与回执恢复；Go race、独立 PostgreSQL
  及真实 Go 子进程崩溃/重放测试通过。进程测试的 Core 是合成实现，不能算真实
  账本的恰好一次结算；实际支付宿主仍未接线。
- `03d8c3472`：移植 #876 的只读订单对账、冻结命令校验及付款/退款/交付恢复，
  补 worker 错误合并、分页与 typed-nil 拒绝，以及 Go 命令字节与 Rust UUIDv4
  variant fixture 对照。Go race、独立 PG 与三个宿主调用包通过；Core 标识符
  5 项和 Commerce 23 项通过。真实 Go→Core Commerce 购买/退款联合验收仍缺。
- `90f7810d7`：移植 #877 的原子改密、会话/Key 代际撤销、OAuth MFA 防绕过与
  不支持的邮箱注册明确拒绝，补原生/浏览器隔离和刷新竞争反例。42 项认证回归
  （39 项真实合成 PG、3 项纯测试）、定向 Clippy 和范围格式检查通过。

Rust 实测为已有 **1.98.0**；固定 1.99 的共享安装不完整，旧私有工具链已不存在。
未重装或更改全局配置；上述 Rust 结果不是固定 1.99 验收。新认证合成库已核验归属、
零客户端并停止，材料留在统一 Backups；商店既有隔离测试资源没有被停止。
父 agent 另执行 39 项前端认证回归、19 项源码边界检查；未重跑全仓聚合或正式构建。
源码、命令、输入哈希、失败尝试与六份回执保存在本机私有证据目录
`~/.local/state/api-lmm-microservices-20261011/parallel-main-parity/`。

这是一轮可复核的增量，不是 main 全功能对齐：HumanGrant、完整历史财务、管理员/MFA、
真实模型供应商、支付宿主和真实资金联合链等下述缺口仍须继续实现与验收。
候选 PR 被引用或窄移植不表示整份候选已接受/合并；运行服务没有切换。

## 核查边界

2026-10-12，本次固定源码：

- 传统 `main` / `traditional`：`3948a0a1ee1cefdd016a7aa45a11851545bb4b85`。
- 微服务 WIP：`9f5541b0b6ecb054fe953d539455c1e2028445f1`。
- 共同祖先：`de2c439f26446792d4a605b2b91fa2b84777e284`。

检查共同祖先之后 main 的增量、两端最终文件内容、WIP 的实际路由与宿主接线，
并核查已存在的未合并候选。**不是全站功能等价验收，也不是所有安全控制的独立审计。**
未访问生产、未迁移数据库、未合并候选、未部署。后续提交需重新固定源码核查。

main 相对于 WIP 有 127 个未被其历史包含的非合并提交，main 相对共同祖先变更
435 条文件路径。提交数不能当作缺失功能数：存在重复补丁、不同实现、格式修改、
已经移植的前端，以及两端都删除的旧部署代码。

## Web：不是整体陈旧，主要遗漏是 HumanGrant

main 在分叉后修改的 96 条 Web 文件路径中：

- 71 条在 WIP 中 blob 完全一致，包括多数日志、财务展示、分页、价格估算、
  工具市场引导、个人菜单 API Keys 快捷入口与生成路由。
- 22 条内容不同，经过核查包含 HumanGrant 接线、微服务财务不可用状态保护、
  格式和更强的测试、协议页面替换、翻译及生成报告，不能全部算遗漏。
- 3 条只在 main 存在：HumanGrant 按钮、HumanGrant 设置区、RT16 浏览器测试入口。

上述分母是 **main 分叉后的 Web 增量**，不是 WIP 所有前端文件。两端整个 Web
树有 110 条不同路径；这些还包括 WIP 自己的品牌、API Key 格式、配置初始化、
商店及财务边界调整，不能直接回滚成 main。

### 确认缺失

main 提交 `5999dc656` 的 HumanGrant 登录页入口和设置区尚未进入 WIP：

- `apps/web/src/features/auth/components/humangrant-button.tsx` 缺失；
  `oauth-providers.tsx` 没有对应入口。
- `apps/web/src/features/system-settings/auth/humangrant-section.tsx` 缺失；
  `section-registry.tsx` 没有注册该设置区。
- en、fr、ja、ru、vi、zh-TW、zh 每种语言缺 9 条 HumanGrant 文案，共 63 条。
  比较的是 JSON 的 `translation` 叶子 key，不是文件顺序或根对象。

#866（Core）与 #880（A10 Web）是未合并候选，不等于默认分支已具备该功能。
接入前仍需冻结后端权限、撤销、会话和持久化合同；不能只补按钮就宣称完成。

RT16 的 `apps/web/scripts/rt16-markdown-entry.tsx` 和根目录
`scripts/security/rt16/` 不在 WIP；这是原安全浏览器复现入口的验收材料缺口，
不是已经证明 WIP 存在相同漏洞。旧入口依赖旧构建路径，需改为微服务可执行的反例。

### 已移植或 WIP 有意加强

- `2c53f147f` 缓存分类后重算估价：`tier-expr.ts`、原生计费相关测试与 main 一致。
- `7be6ee81a` 移动日志、双端分页、错误 toast：生产文件与 main 一致；分页测试
  仅有排版差异。
- `7695eb244` / `f34cfc9c5` 冻结币种历史展示：核心 money、payment 与账单对话框
  与 main 一致；WIP wallet 类型额外声明 `financial_projection_available`。
- `b4600b4cf` 历史金额展示：大多数图表、日志和金额 helper 与 main 一致。WIP
  `summary-cards.tsx` 额外在财务投影不可用时显示 Unavailable，不能替换回 main 的
  余额默认值；对应测试也更完整。
- `0474b42db` AI 品牌粒子：场景目录、轮廓和许可文件已在 WIP。marks 的括号、
  显式导入及动态注册表测试不同，不是少了品牌场景；定向测试通过。
- `f2be6436e` 首次进入设置时不自动展开：生产组件一致；测试排版不同。
- 公告组件一致，WIP 测试进一步验证最后一次确认完成前不能进入工作区。
- 公开协议 HTML 不同来自微服务协议快照/配置边界，不直接恢复旧站写死的政策。

## 后端：主要差距是完整能力没有接入，而非少几个同名文件

以下是 main 新增/修复行为与 **固定 WIP** 的对应核查，候选 PR 保持未合并状态。

1. **HumanGrant 完整链路未接入（A02/A10）。** main `5999dc656` 有签名请求、人工
   consent、原会话绑定、授权/撤销和隐私日志；WIP 当前 Core/Go 没有 HumanGrant
   实现或对应路由。需接入 Core 的真实持久身份，不恢复旧 Go 身份库。
2. **历史日志、统计与完整财务导出未接入（A03/A04）。** main `b4600b4cf` 有
   `quota_usd`、`quota_money_available`、`quota_money_basis`、同快照聚合和未知金额
   处理。WIP 已有 `financial_profile.rs` 的个人钱包读取，但 Core 实际装配中没有
   `/api/log/*`、`/api/data/*`、`/api/finance/export` 的完整接口。前端文件对齐不代表
   接口可用；#875 仍是候选。
3. **OAuth2 授权服务器容量与跨进程防护缺少对应实现（A02）。** main `f857702c1`
   给每 issuer 4096 条授权事务上限、事务锁、到期批量清理和 503。WIP 原生 Google
   登录只有自身的 flow 表及单用消费；这不是 OAuth2 授权服务器，不能抵扣该合同。
4. **OIDC 可信代理限流未实现等价能力（A02/A09）。** main `3591e0a15` 从可信代理
   链确定限流身份。WIP 状态声明 `oidc_enabled:false`，现有身份限流用连接 peer；
   没有 OIDC provider，不能把 peer 限流说成同一代理合同已完成。
5. **绘图/MCP 授权与异步任务能力未接入（A06/A07/A08）。** main `f9b133e37` 限定
   drawing OAuth grant 的 routing groups；`491aed2ee` 禁止 Ali 接受任务后重复提交。
   WIP 模型 HTTP 目前仅装配 models、Chat、Responses，没有图像生成/任务/MCP
   drawing 入口；不能因没有旧代码而忽略这些授权和防重复扣费的验收目标。
6. **OpenRouter 价格同步未接入（A07）。** main `648c58b03` / `55f3ea357` 把
   unsupported structured quote 隔离到单模型，禁止错误降级为便宜平价。WIP 有
   配置驱动的路由/价格快照，但没有对应上游价格拉取和分类流程。
7. **可付款订单的优惠码容量保护未对齐（A03/A05）。** main `67cada53c` 不再仅凭
   reservation TTL 释放优惠名额，包含可恢复的 Waffo timeout。WIP promotions 是
   窄奖励/优惠 RPC 合同，不能证明原充值订单、provider 晚到回调和优惠占位语义
   已接入；需要支付/账本联合验收。
8. **商城 Extore 接入未进入微服务宿主（A06）。** WIP 商店 PublicConfig 明确返回
   `extore_import_supported:false`。main 历史上的持久 merchant key 修复
   `a8d2e710a` 不能替代现在的长期 OAuth 连接。新的双仓候选及全部验收缺项见
   [独立交接](extore-shop-binding-followup.md)、#894；禁止部署。
9. **助手完整执行/上下文与旧工具合同未接入（A08）。** main `a3ff114f1` 要求短
   上下文也检查 tool exchanges；另有客户配置指引和 portal request schema 更新。
   WIP 有独立受限会话/工具处理，且 composition 给 `assistant.New` 传入 nil model，
   Chat 明确不可用。原工具和完整真实对话不能算对齐；不能把新窄工具循环视为旧
   Compact 协议已经验收。
10. **Signal 游戏增量没有对应微服务能力（A06）。** main `db740b1d2` 提前拒绝
    已到期/未到时间的提交，并减少 replay 分配。WIP 没有相应游戏模块/路由。
    应明确该产品能力的保留范围，不盲搬旧模型和奖励写入。
11. **边缘 IP/地域策略的可选 Key 查库预算未映射（A02/A09）。** main `bd622f786`
    对 bypass 查库作来源和全局预算。WIP 有模型 API Key 的 CIDR 检查，但这不是
    原边缘策略端点的可选 bypass 合同；需要明确代理职责与查库边界。
12. **公开 Relay 贡献/审核发布未对齐（A07）。** main `8da4f3994` 修正 SQL group
    列的 dialect 引号。WIP 有可信内部 Router 发布/存储，但不是用户提交贡献与
    审核流程；PostgreSQL-only 新实现也不需要照搬 GORM dialect 修复。

### 不能误报为缺失的安全替代

- main `5e7c3f1f4` 拒绝 JSON 路由歧义。WIP `relay/request.rs` 在生成 JSON Value
  前拒绝重复/大小写重复顶层 key，并限制允许字段。此安全目标有新实现；未声称
  音频/live 等不存在的协议已兼容或独立安全验收通过。
- main `d04990170` Bearer scheme 大小写修复。WIP `model_http.rs`、`legacy_auth.rs`
  已使用 `eq_ignore_ascii_case`；不需要为了补同一提交恢复旧 Go middleware。
- main `62e780a26` 旧个人 token 轮换要求 browser session。WIP 没有
  `/api/user/token` 旧入口，使用自己的 session/API Key 模型。不能报该旧入口仍有
  相同漏洞，也不能据此宣称旧 PAT 兼容完成。
- main `07f91bdca` Responses token 输入遍历重构对应旧 compaction 路径；WIP 没有
  该 compaction/token-count 入口，有 body、消息、工具及字段约束。功能合同仍要
  核定，但旧 DTO 算法不是直接移植目标。

## 部署、文档与验收材料

main 大量增量属于旧 Go schema-only worker、原生 guardian、pre-exec/dead-holder
恢复和签名部署引擎；之后 `19a12b0b2` 又移除了部署 CLI/controllers。WIP 已移除
旧单体和部署 CLI，不能把每个旧路径缺失都算回归，更不能重新引入部署常驻进程。

但是“不搬旧工具”不等于升级/恢复能力已齐备。WIP 目前是 fresh-install schema，
已有核心库在线升级、真实恢复和对应资源/签名分发验收仍需要 A09/S11 完成；普通
启动不得执行 DDL。旧 RT11/12/14/16 的反例须映射到当前路由和存储语义，不用旧
文件不存在证明新系统安全。已保留新的 source boundary、数据库和协议测试，不
代表覆盖全部旧反例。skills 快路径已经有微服务版本，也无需恢复传统安装命令。

## 本轮实际检查

- Git 固定 ref/共同祖先、非合并提交清单、main 增量路径及两端 blob 对比。
- 读取 main 业务增量 diff，检查 WIP 路由、配置、Core 财务读取和 Go composition。
- 7 个 Web 文件逐个执行以下命令，共 **92 通过、0 失败**；Bun 实际为 1.3.14：

```sh
cd apps/web
MISE_DISABLE_TOOLS=rust mise exec -- bun test \
  --preload ./scripts/test-preload.mjs --timeout 15000 <test-file>
```

实测文件：`src/lib/historical-quota-money.test.ts`、
`src/features/usage-logs/lib/money.test.ts`、`src/features/wallet/lib/payment.test.ts`、
`src/components/data-table/core/pagination-state.test.ts`、
`src/features/pricing/lib/native-billing.test.ts`、
`src/features/home/sculptures/marks.test.ts`、
`src/features/dashboard/components/overview/overview-data-states.test.tsx`。

这些是金额 helper、几何、分页及模拟 DOM 的定向回归，不是实际浏览器、后端接口、
数据库、供应商或整站验收。本轮未运行全量 Core/Go/Web/CLI、正式构建或远程 CI。
不输出无完整业务分母的“已对齐百分比”。
