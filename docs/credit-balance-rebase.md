# 一次性历史余额纠正

credit（数据库整数 quota）是唯一余额真相源。1 USD 永远对应 500000 credit。
纠正历史「充值 1 人民币当成 1 美元」约定，应按确认的精确除数缩减指定用户的当前 credit 余额；不得从显示金额反算钱包，不得修改模型价格或 USD/credit 换算锚点。

用户已确认迁移除数采用维护冻结时生产配置 `USDExchangeRate`，必须重新读取并锁定该原始字符串；`6.8` 仅是文档示例。快照 options.USDExchangeRate 必须与显式 --divisor 精确数值相同，SQL 事务再次核对并保留该配置，审计记录 fx_source。绝不能从旧 CreditsPerUSD / 500000 反推除数。

## 离线预览

工具只读取明确指定的 JSON 文件，输出 JSON 预览或 PostgreSQL SQL 文件；没有数据库连接、写库、自动部署或启动时迁移功能。

快照使用真正的数据库 quota 整数，**不要使用界面格式化后的点数或美元值**：

```json
{
  "version": 1,
  "target": {"database": "实际数据库名", "schema": "实际schema名", "system_identifier": "实际PostgreSQL集群整数标识"},
  "options": {"USDExchangeRate": "6.8"},
  "applied_migration_ids": [],
  "users": [{"id": 1, "quota": 500000000, "aff_quota": 680}],
  "topups": [],
  "tokens": [{"id": 10, "user_id": 1, "remain_quota": 680, "unlimited_quota": false}]
}
```

`target` 必须来自生产只读核验（`current_database()`、目标 schema、`pg_control_system().system_identifier`），没有默认 public schema。生成的 SQL 在任何表修改前校验数据库与集群标识，所有表名都明确带目标 schema，不依赖 search_path。若执行角色无权读取集群标识，校验失败并回滚，不能删除校验绕过。

`applied_migration_ids` 必须来自已核对的迁移审计，而不是为重跑清空；首次迁移为 `[]`。完整快照应同时包含选中用户的全部 token（即使保留 token 限额），并包含软删除用户的余额，以便明确决定是否纳入。

```sh
python scripts/preview-credit-balance-rebase.py \
  --snapshot /private/reviewed-snapshot.json \
  --migration-id rmb-balance-v1 \
  --divisor 6.8 --user-id 1 --rounding half-away-from-zero \
  > /private/credit-rebase-preview.json
```

用户范围必须逐个指定 `--user-id`，不支持隐式全员。除数和舍入策略均没有默认值。
整数运算不会浮点漂移：500000000 / 6.8 得到 73529412 credit（四舍五入）。

负余额同样按比例纠正：-86911 / 6.8 得到 -12781 credit，不能变成零来掩盖债务。
`half-away-from-zero` 是正负对称的四舍五入；`toward-zero` 是向零截断。
选定策略应用于每条余额，不是先汇总后舍入。低于半个 credit 的小额余额在四舍五入后可归零，向零截断会把绝对值低于 1 credit 的余额归零，必须在预览时审核。

默认只调整 `users.quota`。

- `users.aff_quota` 是尚可转入钱包的邀请权益，需明确选择 `--include-affiliate`。
- `tokens.remain_quota` 是使用限额，不是额外的钱包，不应和钱包相加。若确认需要同比调整限额，选择 `--include-token-limits`；无限额 token 保持不动。
- `used_quota`、邀请历史额度、已完成充值的 `credited_quota`、历史账单/退款金额均保持原始事实。通过新增纠正审计解释余额变化，不能修改历史再让后台回填扣第二次。

## 必须解决的在途权益

在所有 Go/Rust/API 节点、后台任务、支付回调和消费写入停下前，不能执行迁移。应在停写后重新导出快照生成最终预览。

1. 排空 Redis 和数据库中的预占、工具调用结算、异步任务结算、订阅预消费、待退款；这些若在迁移后回流旧 credit，会把已经减少的余额补回来。
2. 核对未完成充值订单的已锁定 credit、后续支付回调及赠送/签到/邀请参数。选择结清、取消并重新报价，或另写明确的订单权益迁移，不能让旧报价继续入账。
3. 核对红包剩余池、悬赏 escrow、待提现/待领取奖励、订阅余额退款权益。价格字段与可兑付权益应分开判断，不能简单除所有含 quota 的字段。
4. 对订阅 `AmountTotal/AmountUsed`、token、工具预算和消费计数，明确是否保留产品额度/限额，不能把累计消费历史当余额一起扣。
5. 若系统有基于充值美元账面值的余额回填或对账修复，应先关闭或改成 credit 余额为准。

SQL 只覆盖明确选择、具有完整快照和数量/CAS 校验的权益；所有未覆盖的在途预占和结算仍须先排空，不能把脚本生成成功当作全部义务已解决。

## 固定锚点与污染价格必须一起恢复

旧上游同步可能使用错误的 CreditsPerUSD 生成 ModelRatio/ModelPrice；直接把锚点固定回 500000 却保留这些污染价格，会放大美元价格。不能机械按汇率缩放全部价格：手工价格和正确的旧配置可能没有污染；CompletionRatio、缓存倍率、GroupRatio 等相对倍率也不应按汇率换算。

生成可执行 SQL 时必须带 `--restore-fixed-anchors`，快照还必须增加以下内容（这里的旧值仅为示例，必须用实际停写后的精确字符串）：

```json
{
  "options": {
    "USDExchangeRate": "6.8",
    "CreditsPerUSD": "3359744",
    "PublicCreditsPerUSD": "100000",
    "LegacyPricingQuotaPerUnit": "500000",
    "QuotaPerUnit": "500000"
  },
  "price_review": {
    "status": "verified",
    "evidence": "经核验的上游价与手工价来源证据路径或说明",
    "option_corrections": [
      {"key": "ModelRatio", "before": "完整原始 JSON 字符串", "after": "完整已核验恢复 JSON 字符串"}
    ]
  }
}
```

支持恢复的完整 option 键是 `ModelRatio`、`ModelPrice`、`billing_setting.billing_expr`、`tool_price_setting.prices`。逐条模型/工具核验后构造完整 JSON 字符串，保留未污染项。仅在核验确无污染时才允许空 `option_corrections`，不能用空数组跳过调查。工具要求记录证据，但不能自动证明人工核验已经真实完成。

锚点、公共点数显示单位、兼容锚点和 QuotaPerUnit 均在同一事务切为 500000；价格配置同时逐项比较旧字符串后恢复。价格与锚点任意旧值不符，余额调整也一起回滚。SQL 不会修改历史充值或消费事实。

## SQL 生成与真正执行边界

用 `--restore-fixed-anchors` 重新生成包含选项的 JSON 预览；审核每条 before/after、合计、用户范围、价格恢复、除数及 `plan_sha256` 后，才能生成 SQL：

```sh
python scripts/preview-credit-balance-rebase.py \
  --snapshot /private/reviewed-snapshot.json \
  --migration-id rmb-balance-v1 \
  --divisor 6.8 --user-id 1 --rounding half-away-from-zero \
  --restore-fixed-anchors --emit-postgres-sql --reviewed-plan-sha256 EXACT_REVIEWED_HASH \
  > /private/reviewed-credit-rebase.sql
```

仍然只是文件生成，不执行 SQL。不提供默认生产连接。生成文件必须保密，其中包含用户 id 和真实余额。

实际执行前必须确认生产目标、完成停写/结算、创建完整可恢复备份，并在隔离恢复副本上验证计划。SQL 在一个事务中锁住 users/tokens/options，按每项原始余额作比较后更新，token 同时核对 user_id 归属；任意不符整笔回滚，禁止根据新余额再次自动除汇率。

SQL 新增有明确用途的持久审计表 `wallet_credit_rebases`，记录 migration id、完整原始/目标余额计划、计划摘要和时间；另一个 `wallet_topup_credit_rebases` 保存旧成功充值的独立剩余退款池。两个表不是临时垃圾，不能清理掉。同 id 和同 hash 重跑无修改；同 id 换计划失败；即使换 id，也拒绝再次迁移已经调整过的用户。

事务写锁只覆盖执行期间，不能替代全平台停写；缓存里的旧余额必须在恢复服务前清除/重建。执行后按计划核对用户与审计、验证总变化、确认固定 USD/credit 锚点和上游模型价格，再恢复写入。分组倍率调整是单独的后续价格决策，不在余额迁移里自动修改。

验证：

```sh
python scripts/test-credit-balance-rebase.py
python scripts/test-credit-balance-rebase-postgres.py
```

第二条仅建立新本地 PostgreSQL 集群，禁用 TCP、使用私有 Unix socket，测试结束删除自己的 fixture；不读取生产 DSN，不连接现有数据库。

SQL 设置 standard_conforming_strings，DO 使用不出现在嵌入内容中的动态 delimiter，来源证据和价格字符串中的引号、反斜杠或 `$credit_rebase$` 文本不能截断 SQL。

## 在途余额阻碍的具体来源

可先使用 `scripts/inspect-credit-rebase-obligations.sql` 只读统计（显式传入目标 schema）。脚本开启 READ ONLY 事务，只返回状态、计数和点数合计，不返回用户、订单或凭证。表/列缺失会报错，不能把缺失当成零。不同模块权益有重叠，不能把合计全部加起来当总钱包。

| 来源 | 活跃或未结算状态和字段 | 最小处理政策 |
| --- | --- | --- |
| `wallet_transfers` | `pending` 的 `quota` 已从发送方钱包扣除 | 优先走原有取消流程先退回发送方，再对停写快照缩减；若保留待领取，则单独缩减该待转权益并审计，不能又退回又缩减两次 |
| `top_ups` | `pending` 的 `credited_quota`，旧订单可能由 `amount` 等字段兜底计算；`success` 的退款仍会用历史 credited/refunded quota | 与支付商核实已付/未付后结清或取消并重新报价；已支付却未回调不能直接取消当作没钱。保留已完成历史事实，后续退款必须关联独立纠正后的退款基准 |
| `redemptions` / 红包 | 可用且未过期 `redemptions.quota`；红包仅引用兑换码，已领取但未兑换也可入钱包 | 所有可兑换点数权益都需同比调整或按确认政策取消重新发行；不能只查红包未领取条目；`reset_voucher` 与折扣码不按credit余额缩减 |
| `open_source_bounty_projects` / challenges | 项目 `escrow_quota` 可退款/支付；项目和未完成挑战 `reward_quota`、`net_reward_quota` 约束支付 | 优先在业务许可下结清或走关闭退款流程，再缩减钱包；仍有效的悬赏合同与奖金额不能私自取消，需明确同步重定价 escrow 和未来支付权益，保留 ledgers 历史 |
| `tool_market_calls` | `settlement_status=held`；`price_quota` 已被预扣，执行为 reserved/running/awaiting_confirmation 等 | 用原有执行/失败/确认/超时流程结算后再缩减余额；`grants/budgets.reserved_quota` 同步归零或解释，不能直接改历史 `spent_quota` |
| `tasks` | NOT_START/SUBMITTED/QUEUED/IN_PROGRESS/UNKNOWN 及退款 `PENDING` 的 `quota/refund_quota`；private_data 存 wallet/subscription/token 来源 | 完成、实际失败退款或明确撤销后再缩减；不要直接取消上游已执行工作。若不得不保留则要迁移待退权益和结算快照，不能只缩减钱包 |
| `subscription_pre_consume_records` | `consumed/settling`，`pre_consumed/token_consumed/wallet_consumed/actual_quota` 及 recovery_state | 通过既有恢复结算流程排空；不能机械除已 settled/refunded 的历史值 |
| `user_subscriptions` / orders | active 套餐 `amount_total-amount_used`；余额购订阅 charged_quota；provider退款主要撤销订阅权益 | 套餐额度不是钱包，是否缩减剩余套餐须单独确认；禁止缩减 amount_used 历史。provider退款测试保证不直接扣钱包，不能把它和钱包TopUp退款混用 |
| `hero_sms_sms_orders` | 未终结或投诉未结的 `reserved_quota/charge_quota/refunded_quota` | 核对上游订单和投诉后通过已有业务流程退还/结算；已终结 ledger 保留，不能仅看钱包余额 |

仅排空当前在途订单还不够：`payment_refund.go` 对旧成功充值的**以后新发起退款**仍以 `normalizedTopUpCreditedQuota` 及历史 `refunded_quota` 作比例扣点。迁移后不能继续扣旧额度，也不能篡改历史到账数解决。需要增加订单对应的独立纠正基准/迁移关联，并从该基准计算未来实际扣点与新增退款审计；SQL 生成器现在会在同一事务写入独立退款基准，运行时退款处理必须同步发布使用这些基准，不能只执行余额 SQL 后继续旧退款代码。

未来入账来源还包括 `QuotaForNewUser/QuotaForInviter/QuotaForInvitee`、签到策略、管理员调整、兑换码、支付回调和工具/悬赏收入。它们应以确认的整数 credit 政策配置，不能再从错误美元账面值恢复用户余额。分组倍率属于消费价格政策，单独调整。

## 已付充值的退款快照

联合计划必须包含 `topups` 数组。覆盖选中用户全部 `success` 且 credited_quota 或 amount 非零的充值，哪怕已全额退款也保留其零剩余池；SQL 比较总行数和每条事实，拒绝漏订单。非钱包订阅镜像的零 credited/amount 行不纳入。若出现无法明确计算权益的老数据，须先人工核实，不能忽略当作已处理。

每条快照包含原始 `id/user_id/status/credited_quota/amount/platform_amount_micros/money/settled_amount_micros/expected_amount_micros/refunded_quota/refunded_amount_micros/payment_provider/payment_method/settlement_currency`；`money` 用数据库精确文本，其他金额字段用整数。额外的 `effective_credited_quota` 和 `paid_amount_micros` 必须由现有权威充值归一化逻辑读出：这是历史点数事实和实际支付金额，不能从当前显示美元值重新推钱包。

独立表记录原始到账、原始已退点数/金额、原始支付金额，以及按同一确认除数/舍入规则计算的剩余可退款池；新 `rebased_debited_quota` 起始为零。旧 TopUp 历史字段不重写；未来退款分别维护旧单位事实和纠正后实际扣点。二次迁移同用户当前一律拒绝，不会重置已有退款基准。

`price_review.unchanged_option_values` 可以记录要求保留的完整 ModelRatio/ModelPrice/mode/locks 字符串；`absent_unchanged_options` 可记录原本不存在的工具价配置。联合事务先核对这些保留项，任意变化整笔失败，不会创建本来不存在的无关配置。


## 本轮只读发现对应的政策

当前生产只读统计由协调 agent 单独保存，下面只约定处理方式，不把动态计数写成永久配置：

- 未完成充值：区分确实未付与已付但回调迟到。确实未付可取消重新报价；已付应先按已约定事实结算进旧钱包，再纳入停写后的余额纠正，或者明确迁移订单待到账权益。旧订单直接改 status 不是退款，也不能借此吞掉真实支付。
- 可用兑换码：对仍能入钱包的 `redemptions.quota` 同比缩减并逐条审计；已兑换的 quota 保留历史。红包 claimed_by 只表示拿到了码，尚可用的码仍要处理。折扣比例/reset券不缩减。
- 已发布悬赏：保留参与者工作与状态；迁移剩余 escrow 和未支付 reward 权益，而不是为了清理全部关掉。校验每个项目剩余池足够其有效未支付承诺，分配整数舍入尾差；project 的 net_reward/gross_reward 是未来报价配置，需要明确改；已收 platform_fee、已付挑战 reward、tip_quota 和 ledgers 是历史，不改。`TipOpenSourceBounty` 已即时扣发双方钱包，tip_quota 不是待发奖金。
- 已售订阅：用户已确认尚未使用的套餐点数一起纠正。保留 amount_used，设置 amount_total = 原 amount_used + round(max(原 amount_total-amount_used,0) / divisor)，以双 nullable 的 reset_amount/renewal_amount 分别记录本期 grant 和原完整售出 grant，防止重置或续费恢复旧额度。
- 邀请权益：ReferralReward quota/revoked/penalty 是旧单位历史，会在退款追索或误封恢复中再次影响 aff_quota。联合 SQL 的 `--include-affiliate` 保存完整 earned/revoked `referrals` 快照，在 `wallet_referral_credit_rebases` 写入同比纠正后的撤销/处罚/恢复基准；历史 Reward 不改。必须同步部署消费这些基准的运行时代码。

## 显式兑换码与悬赏扩展

用户已确认尚未使用的点数权益也一起纠正。生成对应预览时必须明确传 `--include-redemptions --include-bounties`；快照增加停写捕获的 `snapshot_at` 与 `entities.redemptions/bounty_projects/bounty_challenges` 数组。字段清单以 `scripts/credit_rebase_entitlements.py` 的 `SPECS` 为准，原始权利人、状态、时间戳和所有相关点数事实均必须齐全。

只允许更新可用兑换码 `quota`、published/paused 悬赏的剩余 `escrow_quota` 和未来 gross/net reward、仍有效的未支付挑战 `reward_quota`。已付挑战不能混入；累计 tip 和发布时已付 platform fee 永远不在写入白名单。所有旧状态、归属、时间戳和金额在同一加锁事务核验，完整数量校验拒绝遗漏仍可兑现权益。

每个悬赏迁移后的 escrow 必须足以覆盖计划中尚未支付的挑战承诺；整数舍入若造成不足，预览会拒绝并要求明确尾差分配方案，不会静默削减任意参与者权益。订阅和 pending 支付采用独立扩展，不用这一白名单冒充已覆盖。

悬赏有效承诺按现有 `AcceptOpenSourceBounty/CloseOpenSourceBounty` 源码计算：accepted/submitted；rejected 且 `rejected_at > snapshot_at - 7*24*60*60`、没有 resolved_paid/resolved_denied；或者存在任何 open dispute。恰好到七天边界为过期。完整捕获 active 项目的全部关联 dispute 财务事实并锁定/CAS/count，不把 appealable 或 open claim 剪掉。

过期或已 resolved 的 rejected 没有当前可兑现承诺，保存 `historical_rejection_guard_only` 和完整原事实，`updates` 为空，原 reward_quota 不改；它们不重复占用 escrow。有效挑战标记 `active_future_reward` 并同比纠正其待付额度。dispute 的旧 RewardQuotaSnapshot/ProjectEscrowQuotaSnapshot/tip、角色与时间都是证据，不更新；真正 open 且满足原 pay 角色/状态约束的纠纷使用父审计里的独立 `bounty_dispute_reward` 基准，不能用旧 snapshot 再发旧单位奖励。

悬赏 scope 同时要求 `--include-other-rights` 来保存争议独立付款基准。包含参与者发起、针对项目所有者、未付且 accepted/submitted/rejected 的 open claim；accepted 之后仍可提交工作并获得判付，不能遗漏。所有者发起的争议不能授权参与者领款，仅保存原事实守卫；已解决争议也保留原证据，不要求其已付历史 challenge 混入未付权益快照。

## 保留待支付订单与非现金事实

`--include-pending-topups` 要求完整 `pending_topups` 数组，包含与成功订单相同的原始报价事实、Go 权威 `effective_credited_quota`，以及原本为空/零的三个 pending rebase 字段。原报价与实际支付金额不改，只保存独立的纠正后入账额，未来 callback 必须使用它并保持幂等。正的有限报价若舍入为零仍拒绝，不允许靠零触发旧 fallback。

现有 Go authority 已经返回零、当前 callback 本就不能入账的旧来源，不推测其过去报价。完整保存在 `blocked_pending_bases`，记录 `reason`、原事实和权威分类；metadata 写入迁移 key、original/effective 均为零。SQL 对正常与 blocked 的全部行一起做数量、状态、归属、分类和事实 CAS，原 status/fiat 保留。部署后的 callback 必须按父审计持续拒绝，即使三列 metadata 丢失也不能猜出到账额。以后只有独立核实支付事实的审计调解才能解锁，余额迁移本身不冒充支付争议结论。

非现金旧 LinuxDO 订单必须显式保留在 `noncash_topups`，不能删掉零有效到账行。记录原始事实、Go 的 `is_legacy_linuxdo_credit_topup` 和分类原因；生成器独立复核其事实分类，SQL 同时校验原字段、分类 predicate 及完整数量。它们不建立现金退款池，完整父审计供运行时拒绝错误的现金退款请求。

有限 token 的归属、余额、`unlimited_quota=false` 与完整数量均受事务保护。无限额 token 不写入。

`scripts/export-credit-rebase-rights-private.sql` 只读导出缺失权益字段，不含凭证或 provider payload。输出须保存在私有文件，不向终端打印。待支付 topup 的派生事实仍须用现有 Go authority enrich；补导结果不能替代停写后同一快照的最终全量导出。

待入账 scope 还包含 Waffo Pancake `failed` 且 `failure_reason_code=checkout_timeout` 的可恢复钱包报价；原运行时允许它们被迟到支付回调恢复，必须同步保存纠正基准。其他 failed 钱包订单不混入。订阅的 failed checkout 是终态，仍只迁移 pending 报价。

## 已售套餐、本期重置、完整续费与退款

`--include-subscriptions` 要求 `subscriptions/subscription_orders/subscription_plans/subscription_payment_events/subscription_payment_refunds` 完整数组，字段清单以 `credit_rebase_subscriptions.py` 常量为准。已售套餐和仍 active 的旧余额购套餐一起保留在父审计。双 nullable 原值必须均为 NULL，发现任意已有值即拒绝不完整或重复纠正。

有限套餐本期上限设为 `old_used + round(max(old_total-old_used,0)/divisor)`；`reset_amount=round(old_total/divisor)`；`renewal_amount=round(original sold plan_snapshot.total_amount/divisor)`。原余额购或管理员绑定且没有支付订单的合同，完整 grant 来自自身原 `amount_total`。已用消费事实不改；递增 quota_version 并更新 updated_at 使旧预览失效。明确的非 NULL 零 grant 表示有限套餐已耗尽；原无限套餐双 NULL 保持无限。

未来 catalog 的 `total_amount` 和有冻结快照的 pending 订单 `plan_snapshot.total_amount` 同比纠正，真实价格、支付金额、已完成订单快照不改。有限未来报价若舍入为零则拒绝，因为旧创建入口把零解释为无限。pending 空快照保留原字符串，并在审计注明 `runtime_current_catalog_fallback`、绑定完整当前 catalog 原事实与纠正后 grant；这是现有 complete 路径的明确 fallback，通过同事务修正 catalog 获得新 grant，不补造历史报价。非空但无法解析的快照仍拒绝。

有未退实付金额的已售合同在同事务写入 `subscription_order_credit_rebases`，绑定订单、合同、用户、支付周期/到期、纠正后的 version、原始付款与退款事实。本期剩余额和重置 grant 独立记录；未来退款使用此基准，实际撤回剩余额与名义削减本期 grant 分开累计，完整续费 grant 保持。原本期额度加历史 refunded_quota 必须等于冻结售出快照 grant，不一致时拒绝。必须同步发布使用父/子审计的退款代码，审计丢失不能默默恢复旧单位退款。

SQL 明确锁定全部套餐、订单、catalog、付款和退款表，核对全量数量及每条归属、状态、时间戳、quota_version、原始快照和实付事实；任意不符整个钱包/权益/价格事务回滚。父审计保存全部原 facts 和新 grants，已付奖励及历史使用量不在写入白名单。

## 单事务冻结快照与完整历史守恒

正式快照使用 `scripts/export-credit-rebase-frozen-private.sql`：同一个 REPEATABLE READ READ ONLY 事务导出目标 cluster/database/schema/OID、全部用户（含软删）、全部 token（含无限额）、成功/可恢复待支付 topup、全部权益/套餐/邀请、金融定价原字符串及审计 id。审计表尚不存在时只读探测返回空 id 列表，不创建表。所有金融来源列明确列出，排除身份凭证和 provider payload。

只有明确的 `snapshot_state=frozen_writers_stopped` 才支持执行联合 SQL。生产仍写入的 `provisional_live_not_frozen` 和未声明的 `unspecified` 可生成供审阅的 SQL，但事务第一条业务前检查直接抛错，任何余额、DDL 或审计均不执行。真实隔离克隆演练也须先确认克隆无写入，再明确声明冻结状态并重新生成计划和摘要；不能靠改 SQL 注释或目标身份解除这一门槛。

从 api-go 模块目录运行 `go run -p 1 /absolute/path/scripts/enrich-credit-rebase-facts-private.go --input PRIVATE_RAW --output PRIVATE_NEW`，只对文件中的成功与待支付 topup 调用现有 Go authority，并保留原始 facts。工具没有数据库初始化、连接或执行 SQL，输出新文件以 0600 创建并 fsync，拒绝覆盖旧文件。原始 `QuotaPerUnit` 仅用于还原旧 authority 的归一化行为，不能改变迁移的永久 500000 USD anchor 或冻结 FX 除数。

`user_sources/token_sources` 记录完整钱包与 token 的金融/使用历史、归属、状态和软删标记。联合 SQL 要求两份数组完整；只读 CAS/count 比较 used_quota、request_count、aff_history、时间戳及无限额 flag，不更新任何历史字段。快照选项中所有不写入的金融定价原字符串均受保留保护，恢复候选的 before 必须与同一冻结快照精确一致。

全量运行同时选择 `--include-affiliate --include-token-limits --include-redemptions --include-bounties --include-pending-topups --include-subscriptions --include-other-rights --restore-fixed-anchors`。其它未来点数的 `other_credit_bases` 保存在父审计，原始 tip/withdrawn、礼包、广告、违规扣费、邮件退款历史字段不改；运行时使用独立纠正基准。邮件 ledger 只增加 nullable `original_amount_quota`，无 default 或历史回填。

`obligations` 的九个只读计数必须精确齐全且均为零。SQL 无条件再次核验匹配的在途/待退款任务并加锁，不能通过关闭 `--include-other-rights` 绕过；包括 FAILED 空退款 marker 的近期旧任务，以及 progress 仍未达到 100% 的 Midjourney。缺表或缺列是失败，不是零。

`business_source_sha256` 是去掉 target 的快照摘要；`business_plan_sha256` 是去掉 target/source_sha256/plan_sha256/business_plan_sha256 的计划摘要。均使用排序 key、紧凑 JSON 与 SHA-256。克隆演练只允许替换 target，并保持两个业务摘要一致；目标数据库/集群/schema/OID 的检查仍必须指向真实克隆。异时补导只能用于现状预演，不能签封成正式生产计划。
