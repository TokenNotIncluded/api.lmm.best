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

## 2026-10-10 SDK 与付款金额适配

Go 后端固定使用 SDK `v0.17.0`。本节核对范围为 2026-09-10 至 2026-10-10；
上文的 TypeScript 测试脚本不因 Go 依赖升级而自动改变。
依据为[官方版本记录](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.17.0/CHANGELOG.md)
及[付款回调文档](https://docs.waffo.ai/api-reference/webhooks)。

从 SDK `v0.14.0` 起，写请求不再自动带 `X-Idempotency-Key`。本项目显式绑定
钱包收银台的订单号，以及创建店铺、创建及发布商品的操作标识。商店退款使用原订单号
和退款号生成固定标识，不能使用每次查询都会变化的临时令牌。Waffo 的去重窗口为
24 小时；本地付款收据、退款状态和数据库唯一限制仍是重复记账的最终保护。
鉴权令牌和商品查询不能共用收银台的防重标识。退款超时后仍仅查询，不自动重提。

付款事件优先使用 `chargedAmount`，退款事件优先使用 `refundedAmount`。
`0.00` 表示明确的零金额，不能用商品原价替换。新格式有价格快照、但通道金额
缺失时，不得回退到旧 `amount` 字段，因为它可能使用原价兜底。
旧格式没有这些新字段时，继续保留原有金额处理和订单金额校验。

`listPrice`、`originalPayment`、`planPrice` 分别描述付款标价、被退款付款的
价格快照、订阅阶段价格。它们不证明实际收款或退款。
已知的 `originalChargedAmount` 是单笔退款上限的额外依据；累计退款还必须通过
原有本地流水校验，不能只检查一份回调。金额未知时不得入账或冲销余额。

保留可选的 `periodNumber`，不把它当成成功扣款次数或回调防重键。值为零的
授权阶段不能触发付款发放。现有账期关联仍按下节所述使用付款日期；本次没有
增加按 `orderId + periodNumber` 关联的数据库迁移。

SDK 新增的换套餐入口和免邮箱验证客户门户不在本次更新中启用。
收银台继续显式使用 45 分钟有效期，不采用新文档中的 24 小时默认值。
本次不改变币种、金额快照、计费倍率或其他支付商的行为。

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
go test ./model ./service ./controller -run 'WaffoPancake|PaymentWebhook|MerchantStoreRefund' -count=1
```



## 同一套餐的两种 Waffo 购买方式

套餐新增 `waffo_pancake_products`：每项包含 `product_type`（`one_time` 或
`subscription`）、`product_id` 和 `enabled`。两种商品共享同一套餐的额度和有效期。
新建套餐不会强制接入 Waffo；管理员可点击「同时启用两种购买方式」，也可只启用一种。
保存表单时按需调用现有的商品创建接口，已绑定且价格、币种、周期未变化的商品不重复创建。
此配置只属于 Waffo Pancake，不改变 Stripe、Creem、ePay 或余额支付。

一次性购买只发放一次套餐权益，到期不会再次扣费。自动续费沿用现有的付款与账期事件
配对流程。关闭某种购买方式只限制新订单，不取消已有自动续费合同。

购买接口 `POST /api/subscription/waffo-pancake/pay` 新增 `product_type`。
两种方式都启用时必须传入选择；只有一种方式时仍兼容旧客户端省略参数。
用户不能提交任意商品 ID。后端从最新套餐配置选择已启用商品，按该商品核对币种和报价，
再将所选商品和类型写入订单快照；不从后来的套餐配置推断旧订单类型。

`GET /api/subscription/plans` 返回 `waffo_pancake_options`，逐项提供
`product_type`、`auto_renew` 和服务端 `settlement` 报价。前端不得把一种商品的报价
用于另一种商品，也不得在一次性购买不可用时自动改为自动续费。

数据库新增可空 text 列 `subscription_plans.waffo_pancake_products`。
Go 自动迁移可创建该列；使用独立 PostgreSQL 迁移的环境，必须在启动新版本之前
执行 [SQL](sql/waffo-plan-products.sql) 并验证该列。此变更未修改 Rust 预览版的
支付实现或其迁移合同；不得据此宣称 Rust 支持本功能。
数据库 NULL/JSON null 保留旧单商品设置，JSON [] 表示明确关闭两种方式。
旧订单快照不回填、不重写。必须先更新全部 Go API 节点和前端，再启用双模式。
新旧 API 节点混用期间不得启用双模式：旧节点会忽略新请求中的商品类型。

### 商品创建失败

表单会立即保存每次成功返回的商品 ID，然后才创建下一种商品。任一请求失败后，
本次表单不会自动重试该种商品创建；应刷新 Waffo 目录，选择已经创建的商品后再保存，
或关闭失败的方式。超时不代表远端未创建。关闭/刷新表单可能丢失未保存的草稿 ID，
重新打开前应先在商户目录核对。这里没有宣称跨刷新、跨进程的商品创建幂等保证。
更改价格、币种或自动续费周期会创建替代商品；不要停用仍被旧订阅使用的远端商品。

### 官方资料（核对于 2026-10-10）

- [Payments](https://waffo.mintlify.app/dashboard/payments)：微信支付目前只用于
  符合条件的一次性付款，不用于自动续费；具体显示仍由收银台按币种、地区和渠道条件决定。
- [Products](https://waffo.mintlify.app/features/products)：一次性商品和订阅商品分开；
  CNY 仅支持一次性商品。当前应用仍只处理 CNY/USD，不代表供应商只支持这两种币种。

不要在通用支付层写死「微信一律不支持订阅」，也不要仅凭用户界面语言承诺微信可用。

### 验证

补丁提供独立的产品配置/选择和前端商品准备测试。它们不替代全应用、数据库和签名
回调测试。合并前还需运行原有 Go Waffo 支付/续费/退款测试、前端类型检查及购买弹窗测试，
并在测试环境验证实际收银台及事件投递。不得用测试卡模拟结果声称生产已部署。
