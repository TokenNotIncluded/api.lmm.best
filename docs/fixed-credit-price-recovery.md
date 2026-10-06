# 固定点数尺度与上游价格恢复

1 USD 始终等于 500,000 credit。钱包余额是整数点数账本；人民币汇率可以用于人民币充值、显示和一次性余额修正，不能改变模型、工具的美元价格与点数换算。分组倍率独立调整。

## 为什么价格需要单独核查

旧代码的上游同步目标来自 `GetUSDPriceConfig().CreditsPerUSD`。如果当时目标为 3,359,744、旧价格单位为 500,000，同步 USD 10 / 百万 token 会写入 `ModelRatio = 33.59744`，固定尺度应为 `5`；USD 10 / 次会写入 `ModelPrice = 67.19488`，固定尺度应为 `10`。表达式可能被整体乘以 `6.719488`。

仅修正 USD anchor 并不会自动恢复这些持久价格。原始点数报价和手工价格未必经历过错误同步，所以不能对全部价格统一除汇率。工具价格不在上游同步字段中，但旧 USD 编辑接口也可能通过同一错误尺度污染它。

价格读写检查会拒绝非 500,000 的持久 anchor、旧价格基准或 `QuotaPerUnit`，包括旧节点缓存仍与错误数据库一致的情况。修正这些基准不改价格数据。价格缓存及请求快照也不会从 FX 派生价格。

## 只读核查 SQL

以下只读取价格、单位和锁定策略；不读取渠道密钥或其他配置。SQLite/PostgreSQL 用双引号引用 `key`；MySQL 换成反引号。

```sql
SELECT "key", value
FROM options
WHERE "key" IN (
  'CreditsPerUSD', 'LegacyPricingQuotaPerUnit', 'QuotaPerUnit',
  'PublicCreditsPerUSD', 'ModelRatio', 'ModelPrice',
  'CompletionRatio', 'CacheRatio', 'CreateCacheRatio',
  'ImageRatio', 'AudioRatio', 'AudioCompletionRatio',
  'billing_setting.billing_mode', 'billing_setting.billing_expr',
  'tool_price_setting.prices', 'ModelPriceLock'
)
ORDER BY "key";
```

取值是否污染必须结合当时的同步来源、价格编辑记录、备份和上游报价，不能仅凭一个小数就判断。`ModelRatio` 存每 token 点数，USD 10 / 百万 token 的 `5` 是正确值；按百万 token 计的 5,000,000 点数应保持整数。其他合法低价也可能是每 token 小数。

## 恢复步骤

1. 保存只读价格快照和钱包迁移前的审计结果。核对所有写节点已经停止或使用固定尺度。
2. 从价格来源重新获取报价，按固定 500,000 将报价转换为本地点数；不根据用户美元显示值反推价格。
3. 逐模型列出当前值、来源报价、预期值、是否锁定，以及是否由错误同步产生。保持手工报价和锁定策略，由管理员逐项选择需要恢复的模型。
4. 对明确受到污染的字段生成精确旧值与新值的比较后更新计划。`CompletionRatio`、缓存、图像、音频相对倍率与分组倍率不可机械缩放。
5. 在维护窗口内完成余额点数修正、固定单位配置及已确认的价格恢复。通过更新入口失效价格缓存，重启旧节点，避免未完成请求仍携带旧价格快照。
6. 核对实际扣费：USD 10 的模型或工具，在倍率 1 下扣 5,000,000 credit；汇率从 6.7 改为 6.8 不改变此扣费。分组倍率变化只改变最终扣费。

如果无法证明历史价格来自错误同步，应先保留它并完成比对，不能将整个价格库直接除以 `6.719488`。重新同步是恢复已污染上游报价的方法，不是覆盖全部手工配置的一键操作。
