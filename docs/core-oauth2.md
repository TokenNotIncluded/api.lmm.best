# Rust 客户端 OAuth2 授权

完整 Core 全新安装的可选客户端 issuer；Pi 模型委托是额外显式配置的候选能力。源码或本地验收不代表旧站点已启用；
既有数据库不能由启动、重跑 `init-db` 或修改指纹升级。
账号和 MFA 见 [身份指南](core-identity.md)，实现边界见
[Core OAuth 合同](../services/core/oauth-foundation.md)。

## 客户端与授权

`lmm` CLI 请求恰好 `catalog:read balance:read`，仅允许只读目录和个人余额。
真实登录和 MFA 后，用户显式 Continue，核对账号及 group 快照，再 Allow 或 Deny。
发行 scope 另含对应 `group:`；不签发 API Key，不授予开发者资格。

配置有限 `pi_max_credits` 且启用实际 ModelGateway 后，可请求 client_id=`lmm-pi`：
四项必需 scope 为 `catalog:read balance:read usage:read models:invoke`。
用户必须已有真实 `developer_access` 资格，管理员身份也不能代替；用户在同意页选择
不超过安装上限的有限整数 credits 预算。客户端可申请已知的可选 MCP/remote scope，
本候选不发行它们，不允许团队、管理员、支付、转账或市场权限。
Pi 的 scope 不等于实际模型协议支持，仍需客户端和模型能力验收。

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
# 可选：同时配置 [model] 后，才开放 Pi 有限预算同意。
# pi_max_credits = 1000000
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
受保护读接口是 `/api/oauth2/catalog` 与 `/api/oauth2/balance`；Pi 另有 `/api/oauth2/usage`。
目录来自实际 ModelGateway 的同一 serving/price 快照；缺失或失效返回 unavailable，
合法但无可表达模型时返回真实空目录，不凭 SQL 记录捏造可调用模型。
余额保留整数 credits、计量单位及开账来源，无美元换算证明时不可用，不猜汇率。

授权码一次消费，精确绑定 client/resource/loopback redirect/S256 verifier。
refresh 旋转只能保持或缩小权限；旧 refresh 重放持久撤销整个 family。
丢失成功响应不能盲重试。Core 重新检查当前用户、权限、epoch、consent 和到期。
普通浏览器 refresh 独立于 OAuth family；密码/权限生命周期仍按真实身份版本执行。
`/api/oauth2/revoke` 撤销当前 client 的整个 access/refresh family，未知合法 token 幂等成功。

## Pi 模型与财务来源

Pi 模型调用使用真实 Bearer access token，走同一 Core Chat/Responses JSON/SSE 网关。
`X-LMM-Group` 为规范的无 padding base64url group 选择器；重复或超出同意范围拒绝。
在预留、开始和追加预算的同一数据库事务中，Core 重检真实 family/access、本人账户、
当前身份版本、开发者资格、group、期限和有限预算，再允许新消费。
财务记录的 `key_id` 为 null，另存不可变 OAuth 来源；原生 Key 的整数与旧 JSON receipt 保持。
不创建假 Key，不把客户端提交的来源 JSON 当权威，不使用其他人或团队资金。

同一 family 的预算覆盖 refresh 前后全部 access、已消费净额和未结算预留；
本人/账户/套餐限制仍适用。撤销、禁用或标记阻止新消费，原合法预留仍可按原 payer 结算、
退款或恢复，不能因当前授权失效吞掉账单。开始失败或缺失供应商最终 usage 不猜零消费。
usage 只读同 family、本人与个人 payer 的真实来源账单：最近 31 天完整最多 1000 项，
超过边界返回 unavailable，不把截断结果伪装成完整汇总。Web 日志需接受真实 null Key ID。

## 未完成范围

本候选仅做源码与格式检查；编译、独立 PG/TCP、所有旧财务回归仍须执行。
真实 HTTPS CLI/Pi、系统凭据库、browser/MFA/多标签、实际 gateway 正反例、余额并发、
安装、独立升级、加密备份和空目标恢复均须验收；源码和未挂载路径的 404 不能代替。
当前目录仍只表达已验证的本机 loopback Chat/Responses、相同公开/上游模型名与受限价格能力，
不能把外部 HTTPS、模型名重映射、图片等未表达能力宣传为 Pi 可用。
Google 外部凭据、本地 MFA 续接、Passkey、市场 OAuth/MCP 与 marked L0 的 honey-only Pi
尚未完成。蜜罐授权需独立来源类型，不能让普通模型委托绕过标记。
