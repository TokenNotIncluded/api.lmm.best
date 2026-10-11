# Waffo Pancake 客户门户

> 本文保留旧 Go 单体的门户接口约定；当前微服务扩展未提供下述门户端点和旧服务端实现。接入状态以[架构与状态](core-migration.md)及[扩展宿主](../apps/lmm-extensions/README.md)为准，不能据此认定登录、支付或退款链路已经接通。

本页对应 Go 后端的客户门户登录入口。使用已固定的官方 Go SDK `v0.17.0`，
不增加依赖、数据库迁移或新的商户密钥配置。本次仅完成服务端接入；钱包页面尚未增加按钮。

## 使用范围

`POST /api/user/self/waffo-pancake/portal` 无业务参数。必须通过现有的用户认证，
并具有有效的站内登录会话。匿名请求和个人 API 令牌不能换取门户登录链接。
浏览器应使用现有 API 客户端，以保留认证与 `X-LMM-Credit-Unit` 等公共请求头。

服务端读取保存的 `WaffoPancakeStoreID`，并复用结账时的
`WaffoPancakeBuyerIdentityFromUserID`。不接受调用方指定的邮箱、用户、店铺、环境或返回网址。
密钥仍由现有 `WaffoPancakeCredentials` 读取；测试或正式环境由该密钥决定。
仅创建门户链接，不创建付款、退款或换套餐操作。

成功响应包含 `success: true` 和 `data.portal_url`、`data.expires_at`。
未登录返回 401；缺少店铺或密钥返回 503；上游失败或返回无效链接时返回 502。
错误响应不包含上游原文、密钥或登录令牌。请求使用现有的同源检查、请求大小限制和限流，
上游调用最多等待 10 秒，并继承调用方的取消信号。

管理已有订单不依赖钱包商品 ID、支付方式开关或控制台开通状态。停止接受新充值，
不应阻断客户查看订单或管理已有订阅。配置中的店铺必须与已有订单所属店铺一致；
本入口不会猜选其他商户店铺，也不为外部商家的订单创建门户。

## 链接保密

后端调用 `Auth.CreateCustomerPortalLink`，每次请求生成新链接，不设置固定防重键。
链接中的 `#token=...` 是临时登录凭据，不是可以公开分享的普通网址。
后端检查 HTTPS、官方域名、当前店铺路径、令牌位置及过期时间。
当前只接受官方托管域名 `pancake.waffo.ai`；不声称支持自定义门户域名。

响应设置 `Cache-Control: no-store` 和 `Referrer-Policy: no-referrer`。
接口不注册为助手操作，不能把链接当作工具结果返回给模型。
后续前端按钮应仅在点击时请求，并立即跳转；不要把返回数据放进查询缓存、
localStorage、分析事件或错误日志。不要在页面初始化、预加载或自动重试时生成链接。

## 与更新公告的关系

新金额字段的选择及 SDK 升级已在此次补充接入之前进入主分支；本次不重复改写入账逻辑。
付款实收与商品标价仍分开处理，金额未知不能用标价代替。主接入文档中关于旧版付款与
账期配对的表述已按当前实现校正，这属于文档纠正，不是本次更改了权益发放规则。

Waffo 的本地货币收银台、提现账户和商户团队功能由供应商管理。
不能据此把本项目的结算币种名单扩大为供应商展示币种，也不能把商户后台团队当作站内团队。
本次不改变用户价格、兑换比例、退款金额或订阅开关。

本次未接入换套餐确认流程。不要仅因门户可登录就开启商品组的自助换套餐开关：
仍需核对目标商品与本地套餐的映射、差价、下周期生效及退款后的权益。

## 验证

无需密钥的链接校验测试，可从仓库根目录执行：

```sh
cd apps/api-go/service
go test -race -count=1 waffo_pancake_portal_link.go waffo_pancake_portal_link_test.go
```

该命令只编译列出的标准库文件，不证明 SDK、认证路由、数据库或完整应用可运行。
完整环境合并前还需执行：

```sh
cd apps/api-go
go test ./service ./controller ./router -run 'WaffoPancake|PaymentWebhook|MerchantStoreRefund' -count=1
```

控制器测试使用注入的模拟链接签发器，检查会话要求、用户绑定、敏感错误过滤及禁止缓存。
它不替代真实认证中间件或 Waffo 联调。使用 Test API Key 和已有测试账号验收：
确认进入对应店铺且只显示当前客户的订单；退出站内登录后不能创建链接；链接失效后重新
发起请求；取消/恢复订阅后核对已验签的事件及本地权益。测试中不要操作真实资金。
未执行这些步骤时，不得声称生产上线或端到端验证通过。

## 官方依据

- [Go SDK v0.17.0 变更记录](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.17.0/CHANGELOG.md)
- [官方门户链接实现与调用要求](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.17.0/auth.go)
- [官方门户响应测试](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.17.0/auth_test.go)
- [Webhook 字段与事件说明](https://docs.waffo.ai/api-reference/webhooks)
