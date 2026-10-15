# Rust 客户端 OAuth2 授权

完整 Core 全新安装的可选只读 issuer。源码或本地验收不代表旧站点已启用；
既有数据库不能由启动、重跑 `init-db` 或修改指纹升级。
账号和 MFA 见 [身份指南](core-identity.md)，实现边界见
[Core OAuth 合同](../services/core/oauth-foundation.md)。

## 客户端与授权

当前只接受 client_id=`lmm`，请求恰好 `catalog:read balance:read`。
真实登录和 MFA 后，用户显式 Continue，核对账号及 group 快照，再 Allow 或 Deny。
发行 scope 另含对应 `group:`；不签发 API Key、不授予开发者资格，也不允许模型、
MCP、管理员、团队、支付或转账。Pi 四项必需 scope 另需真实委托消费和 usage 接入，
不能省略 scope、假造 Session 或把 OAuth access 当 API Key 来声称可用。

预备页 GET 不自动授权。每个不可变阶段有独立 HttpOnly cookie 与一次性 CSRF；
延迟响应只清自己的 cookie。Cookie 名是选择器，Core 仍同时验证持久化绑定、
原生 Session、当前权限和 MFA。Web refresh cookie 的 `/api/user/auth` 路径不扩大。
回调只有 code/state/iss 或 error/state/iss，access/refresh 不进入 URL。
真实浏览器的 cookie 路径和多标签行为仍须独立验收。

## 显式安装配置

在已有受限 Core TOML 中增加以下片段，保留数据库、Web origin、Web refresh 私钥。
issuer 必须与 HTTPS Web origin 完全相同，不接受 HTTP、IP、端口或代理头推断。

```toml
[identity.oauth2]
issuer = "https://api.example.invalid"
groups = ["default"]
access_ttl_seconds = 900
family_ttl_seconds = 2592000
```

`groups` 是安装上限，不给用户自动授权；仍需当前开发者许可和各 group 授权。
access TTL 为 1–86400 秒，family TTL 为 1–31536000 秒，refresh 不延长 family。
完整新库含 `oauth-grants` 组件，与 abuse 组合共 13 个。
运行者另用 `deployment/postgres/grant-core-runtime.sql` 的 `enable_oauth2=true`
准备最小 DML；启动拒绝缺少或过宽权限，不能使用安装 owner 凭证运行。
此例不是旧库升级步骤。恢复须保存整个 Core 数据库、消费与撤销 lineage，
以及身份指南规定的 Web/MFA 私钥，不能只恢复余额或删除已用 refresh 记录。

## 资源与撤销

发现端点为 `/.well-known/oauth-authorization-server` 和
`/.well-known/oauth-protected-resource/api/oauth2`。
受保护读接口是 `/api/oauth2/catalog` 与 `/api/oauth2/balance`。
目录来自实际 ModelGateway 的同一 serving/price 快照；缺失或失效返回 unavailable，
合法但无可表达模型时返回真实空目录，不凭 SQL 记录捏造可调用模型。
余额保留整数 credits、计量单位及开账来源，无美元换算证明时不可用，不猜汇率。

授权码一次消费，精确绑定 client/resource/loopback redirect/S256 verifier。
refresh 旋转只能保持或缩小权限；旧 refresh 重放持久撤销整个 family。
丢失成功响应不能盲重试。Core 重新检查当前用户、权限、epoch、consent 和到期。
普通浏览器 refresh 独立于 OAuth family；密码/权限生命周期仍按真实身份版本执行。
`/api/oauth2/revoke` 撤销当前 client 的整个 access/refresh family，未知合法 token 幂等成功。

## 未完成范围

真实 HTTPS CLI/系统凭据库、真实 browser/MFA/多标签、实际 gateway 正反例、余额并发、
独立 PG、安装/升级/恢复均须执行，作者源码和未挂载路径的 404 不能代替。
Google 外部凭据、本地 MFA 续接、Passkey、Pi 模型/usage、市场 OAuth/MCP 仍需完整实现。
