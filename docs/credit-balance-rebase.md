# 一次性历史余额纠正

credit（数据库整数 quota）是唯一余额真相源。1 USD 永远对应 500000 credit。
纠正历史「充值 1 人民币当成 1 美元」约定，应按确认的精确除数缩减指定用户的当前 credit 余额；不得从显示金额反算钱包，不得修改模型价格或 USD/credit 换算锚点。

`6.8` 只是说明例子，不是已批准的执行参数。当前汇率、旧 CreditsPerUSD 等设置也不能擅自替代确认的迁移除数。

## 离线预览

工具只读取明确指定的 JSON 文件，输出 JSON 预览或 PostgreSQL SQL 文件；没有数据库连接、写库、自动部署或启动时迁移功能。

快照使用真正的数据库 quota 整数，**不要使用界面格式化后的点数或美元值**：

```json
{
  "version": 1,
  "target": {"database": "实际数据库名", "schema": "实际schema名", "system_identifier": "实际PostgreSQL集群整数标识"},
  "applied_migration_ids": [],
  "users": [{"id": 1, "quota": 500000000, "aff_quota": 680}],
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

这个版本的 SQL 只覆盖经过明确选择的钱包、邀请权益和有限额 token，**不会声称已经处理上面所有其他权益**。

## 固定锚点与污染价格必须一起恢复

旧上游同步可能使用错误的 CreditsPerUSD 生成 ModelRatio/ModelPrice；直接把锚点固定回 500000 却保留这些污染价格，会放大美元价格。不能机械按汇率缩放全部价格：手工价格和正确的旧配置可能没有污染；CompletionRatio、缓存倍率、GroupRatio 等相对倍率也不应按汇率换算。

生成可执行 SQL 时必须带 `--restore-fixed-anchors`，快照还必须增加以下内容（这里的旧值仅为示例，必须用实际停写后的精确字符串）：

```json
{
  "options": {
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

SQL 只新增一个有明确用途的持久审计表 `wallet_credit_rebases`，记录 migration id、完整原始/目标余额计划、计划摘要和时间。这个表不是临时垃圾，不能清理掉。同 id 和同 hash 重跑无修改；同 id 换计划失败；即使换 id，也拒绝再次迁移已经调整过的用户。

事务写锁只覆盖执行期间，不能替代全平台停写；缓存里的旧余额必须在恢复服务前清除/重建。执行后按计划核对用户与审计、验证总变化、确认固定 USD/credit 锚点和上游模型价格，再恢复写入。分组倍率调整是单独的后续价格决策，不在余额迁移里自动修改。

验证：

```sh
python scripts/test-credit-balance-rebase.py
python scripts/test-credit-balance-rebase-postgres.py
```

第二条仅建立新本地 PostgreSQL 集群，禁用 TCP、使用私有 Unix socket，测试结束删除自己的 fixture；不读取生产 DSN，不连接现有数据库。

SQL 设置 standard_conforming_strings，DO 使用不出现在嵌入内容中的动态 delimiter，来源证据和价格字符串中的引号、反斜杠或 `$credit_rebase$` 文本不能截断 SQL。
