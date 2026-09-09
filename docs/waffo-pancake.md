# Waffo Pancake（测试环境）

当前 Go 后端使用官方 `github.com/waffo-com/waffo-pancake-sdk-go` 完成服务端集成。仓库根目录另外安装官方 `@waffo/pancake-ts`，仅供服务端 smoke runner 使用；不要把它放进 Web bundle：Waffo 的私钥只能留在服务端。

## 配置

只需要把这两个凭据注入 API 进程（不要提交到 Git）：

```sh
export WAFFO_MERCHANT_ID='从 Dashboard → API & Development 顶部复制的 Merchant ID'
export WAFFO_PRIVATE_KEY='API Key 对应的 RSA 私钥 PEM'
```

`WAFFO_MERCHANT_ID` 不是 `storeId`。Store/Product ID 是运行时配置：可以在管理员的 Waffo Pancake 配置中从 Dashboard 选择，或使用后端的 pair/catalog 路由创建/保存。

## 官方 TypeScript SDK smoke 流程

先在 Dashboard → API & Development 顶部复制 **Merchant ID**，再在 API Keys 创建并下载 **Test** API Key 的 RSA 私钥。两者只通过环境变量注入：

```sh
export WAFFO_PANCAKE_ENV=test
export WAFFO_MERCHANT_ID='MER_...'
export WAFFO_PRIVATE_KEY='-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----'
bun run waffo:pancake:smoke -- --webhook-url \
  'https://api.lmm.best/api/waffo-pancake/webhook/test' \
  --configure-webhook
```

Runner 使用 `@waffo/pancake-ts`，遵守多店铺/多商品不猜选的规则；没有店铺或只有一个店铺时才会自动创建/选择。它只创建测试收银台，不调用 `.publish()`，避免把测试商品提升到生产。命令会输出一次性 checkout URL、订单业务号和测试卡 `4576750000000110`（未来有效期、三位 CVC）。完成托管收银台后，Waffo 应向 `/api/waffo-pancake/webhook/test` 发送签名的 `order.completed`；Go 端会验签、按业务订单号结算并保持重复投递幂等。

如果 Dashboard 已有测试商品，可显式传入 `--store-id` 与 `--product-id`，不会创建临时资源：

```sh
bun run waffo:pancake:smoke -- \
  --store-id 'STO_...' --product-id 'PROD_...' --buyer-email 'you@example.com'
```

没有 Merchant ID/私钥时不能执行真实 checkout 或伪造 webhook 验证；请使用 Test API Key 注入环境后再运行上述命令，切勿把私钥提交到仓库或聊天记录。

## 端点

- 已登录用户创建钱包 checkout：`POST /api/user/self/waffo-pancake/pay`
- 测试 webhook：`POST /api/waffo-pancake/webhook/test`
- 正式 webhook：`POST /api/waffo-pancake/webhook/prod`

在 Pancake Dashboard 的 Webhooks 中注册测试 URL，并至少订阅：
`order.completed`、`subscription.activated`、`subscription.renewed`、`subscription.payment_succeeded`、
`refund.succeeded`、`refund.failed`。请求体会先验签，再按订单的 merchant
external ID 绑定本地订单；订阅事件只有 `WAFFO_PANCAKE_SUB-*` 订单会进入
订阅结算路径，普通钱包订单收到订阅事件只确认、不改余额。

验签后还会检查载荷中有值的状态字段：`order.completed` 要求
`orderStatus=completed`、`paymentStatus=succeeded`，退款事件的
`refundStatus` 必须与事件类型一致。字段缺失仍兼容旧载荷；签名有效但状态
自相矛盾的事件会记录错误并确认，不会入账，也不会因为同一份坏载荷反复重试。

## 2026-09-06 订阅 webhook 变更

`subscription.payment_succeeded.data` 已移除 `billingPeriod`、
`currentPeriodStart`、`currentPeriodEnd`、`orderStatus`。付款成功回调负责提供
付款凭据；计费周期由以下生命周期事件提供：

| 付款类型 | 周期事件 | 处理条件 |
| --- | --- | --- |
| 首次付款 | `subscription.activated` | 与同一订单的 `subscription.payment_succeeded` 关联后开通 |
| 续费付款 | `subscription.renewed` | 与同一订单对应账期的 `subscription.payment_succeeded` 关联后续期 |

**上线时必须在商户后台「Webhook 设置」勾选 `subscription.renewed`。**
仅部署代码不会开启商户后台的事件投递；未勾选时，新续费的周期信息无法到达。
首次付款不会触发 `subscription.renewed`。

使用 PostgreSQL 独立迁移流程的部署，需要先应用 contract 8 的
`apps/api-rust/migrations/0008_waffo_subscription_webhooks.sql`，再启动新版 API。
该迁移新增付款与周期凭据表及幂等索引；Go 的迁移与 `verify` 模式也要求这两张表。

后端持久化已经验签并绑定本地订单的付款凭据和生命周期周期记录，使用 `orderId`
关联订阅，并使用付款的 `paymentDate` 匹配对应周期。回调可以乱序到达；缺少配对
证据时保留待处理记录，另一事件到达后再结算。单独收到生命周期事件不会发放权益。
重复投递保持幂等；迟到的旧周期不会覆盖更新的订阅周期。历史周期独立保存，不依赖
订单表中滚动更新的当前周期。日期字段同时接受 RFC3339 时间戳和 `YYYY-MM-DD`；
仅日期格式按 UTC 零点解释，避免服务器时区改变账期。

9 月 6 日之后至启用 `subscription.renewed` 之前的存量付款，应先核对商户付款记录、
本地订单和已发放权益，优先补投原始付款及生命周期事件。无法取得历史周期时，公告
允许按「周期起始日 = `paymentDate`、周期长度 = 商品计费周期」近似回补；需要使用
付款时保存的商品计费周期，由运营审核后执行并留存近似计算依据。当前商品配置可能
已经变化，不能直接用它推算历史账期。

订单查询接口只返回当前最新的 `currentPeriodStart` / `currentPeriodEnd`，没有按
付款 ID 查询历史账期的接口；不要用当前周期覆盖历史付款。本次代码升级不会自动
修改生产历史订单，也不会自动执行近似回补。

## 测试卡与验收

测试模式使用 Visa `4576 7500 0000 0110`，任意未来有效期和三位 CVC。成功后应看到：

1. checkout session 返回 `checkout_url`；
2. 钱包充值处理 `order.completed` 后，本地订单变为成功；订阅首次付款需收到
   `subscription.activated` 和 `subscription.payment_succeeded` 后完成开通；
3. 退款成功事件写入幂等的财务收入冲销记录；退款失败只记录审计，不扣用户额度；
4. 重复投递不会重复记账或重复写入退款审计日志（包括 `refund.failed`）。

失败退款事件收据默认保留 48 小时，并在接收后按批次清理；保留期不会低于
SDK 默认的 45 分钟签名重放窗口。可通过
`WAFFO_PANCAKE_WEBHOOK_RECEIPT_RETENTION_SECONDS` 延长保留期。

成功退款会按比例冲销对应的钱包额度或订阅权益，并写入幂等财务记录。钱包余额不足以
冲销时，事务回滚并报错，需人工核对处理；失败退款事件只记录审计，不扣额度。

本地回归：

```sh
cd apps/api-go
go test ./model ./service ./controller -run 'WaffoPancake|PaymentWebhook' -count=1
```
