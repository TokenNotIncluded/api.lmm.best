# main 增量与微服务主线对齐核查

[文档目录](README.md) · [主线交接](development-branches.md) · [架构状态](core-migration.md)

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
