# Rust 原生账号与身份（全新安装 WIP）

核心数据库的安装入口只接受全新空库。旧用户、Key 或余额的迁移须走另行审核的迁移流程；本指南不提供旧库原地升级步骤。数据库定义位于 `services/core/schema/identity.sql`。Go 扩展不得连接这个数据库。

身份叶表由 `IdentityStore::init_database()` 显式安装，但该入口不会安装其他 Core 组件。本批新增的 Key 管理、浏览器用户投影及正常模型的新预留检查，还会读取 `services/core/schema/abuse.sql` 中的持久策略，要求完整 Core 安装。缺少该 schema、表或读取权限时必须报错，不能当作“未标记”继续授权。只安装旧身份叶表不再足以运行这些能力；隔离 PostgreSQL 测试须由安装者显式安装所需组件并授予运行时必要权限，服务启动不得自动补表。Core 仍不依赖 Go。

先按 [Docker 指南](../deployment/docker/README.md) 创建隔离开发栈，并在当前 Bash 会话中定义其中的 `dc` 和 `de`。`lmm-core init-db` 只安装空库，第二次执行拒绝；服务启动不会执行建表、改表或修复。版本和指纹检查是安装契约检查，不是对任意人工改表的完整检测。

## 账号与权限分开

`accounts.id` 是核心内部资源与资金归属标识。`users.id` 是登录人标识，`teams.id` 是公开团队标识，三者不能混用。用户指向自己的个人账号，团队指向独立团队账号。

平台角色单独保存为 `user`、`admin`、`superadmin`。账号服务等级单独保存为 L0–L4。兼容界面输出仍可显示 L5（管理员）、L6（超级管理员），但这不授予团队消费权，也不代表管理员的个人账号自动获得更高服务权益。

Key 保存 `owner_account_id`；每个付款候选保存于 `key_funding_rules` 的外键和顺序字段中，不再隐藏在 JSON 内。账号内部的订阅/钱包顺序单独保存于 `core_billing.account_policies`。这些是授权与付款规则，不是余额或扣费引擎。

个人 Key 默认只允许个人账号；明确配置后才能添加当前已获消费授权的团队。团队 Key 只能使用所属团队，不能回退到个人或其他团队。服务凭证和平台管理角色均不能代替用户的消费授权。

计费接入必须在接受请求时固定付款账号，校验当前团队成员身份和明确的消费许可。拒绝、数据库故障或未知结果不能触发换人付款；一笔费用不能拆分到多个账号。预算须包含已提交金额和待结算预留，不能通过退出重入、换 Key 或修改角色清零；资金操作还需独立授权。这些是接入约束，不表示团队付费流量已启用。

## Google 内置登录预设

Google 已归入内置 OAuth 预设，不再作为新建自定义提供者的模板。
已有自定义记录及绑定不删除、不自动迁移 Client ID、密钥或用户；内置浏览器入口实际可用时才替代对应登录按钮，未配置时保留客户端的自定义回退行为。

安装配置仍由 Core 的只读 TOML 和独立私有密钥文件提供，管理页展示固定端点、scope 和回调说明，不能通过未实现的 `/api/option` 保存来冒充配置成功。
浏览器安装的片段如下；须保留完整配置中的数据库、Web、RPC 等设置，并明确只读挂载密钥文件：

```toml
[identity.google]
client_id = "registered-client-id.apps.googleusercontent.com"
client_secret_file = "/run/secrets/google-client-secret"
redirect_uri = "https://api.example.test/api/oauth/google/callback"
```

该回调必须在 Google 应用中登记，并严格匹配 `[identity.web] origin` 加 `/api/oauth/google/callback`。
原生 API 回调 `/core/v1/auth/oauth/google/callback` 仍是另一种有效安装方式，但不启用浏览器预设，不能拿原生凭证 JSON 当网页登录完成。
公开状态仅在浏览器桥接配置匹配时提供 `google_oauth=true` 和公开 Client ID，不返回密钥。

浏览器先提交同源 `POST /api/oauth/google` 及法律协议确认，随后使用固定 Google 授权端点和 PKCE。
回调校验签名、issuer/audience、nonce、一次性 state 及 HttpOnly 浏览器绑定，再签发既有浏览器 cookie 家族，并固定跳转到 `/console`。
已有用户禁用、本地 MFA 或注册限制不绕过；当前 Google 浏览器路径对需要本地 MFA 的账号失败关闭，尚不提供该路径的第二因素续接。
合成签名供应商及隔离 PostgreSQL 测试不等于真实 Google 账号验收，也不代表旧站点已启用。

## 可选 TOTP 二次验证（MFA）

当前源码提供密码登录后的 TOTP 挑战、一次性恢复码及浏览器中的启用、停用和恢复码重建。
此节是配置合同；源码、自测或成功加载配置均不代表浏览器、CLI 和真实数据库的组合验收完成。

MFA 默认不配置。启用时，在已有 Core TOML 中加入下列片段；保留受限数据库、
`identity.web` 和其他已需配置。该片段不会替用户开启 MFA：用户仍须完成设置与验证码确认。

```toml
[identity.mfa]
keyring_file = "/etc/lmm.conf.d/secrets/core/mfa-keyring.toml"
```

`mfa-keyring.toml` 使用独立的 TOML 文档：

```toml
version = 1
active_key_id = "mfa-key-1"

[[keys]]
id = "mfa-key-1"
key_file = "/etc/lmm.conf.d/secrets/core/mfa-key-1.bin"
```

这里的名称和路径仅为示例，没有可用密钥。由安装操作者另行准备 MFA 专用的随机密钥；
每个 `key_file` 必须恰好包含 **32 个原始字节**，不能填十六进制、Base64 或带换行的文本，
也不要复用浏览器 refresh 或 API Key 加密密钥。私密材料不进入仓库、包、URL、命令行或日志。

解析要求与 [私密文件规则](filesystem-layout.md#权限与服务账户)一致：keyring 文档及每个
key 文件都必须是服务实际可读的普通文件，使用绝对路径，不包含 `.`、`..` 或符号链接；
Unix 权限只接受 `0400` 或 `0600`。keyring 文档上限为 16 KiB，`version` 只接受 `1`，
`keys` 须有 1–8 项。每个 `id` 须唯一，由 1–64 个 ASCII 字母、数字、`_` 或 `-` 组成，
`active_key_id` 必须指向其中一项；未知字段或格式错误会拒绝启动。
容器安装还须显式只读挂载 keyring 和其中引用的每个 key 文件，容器内路径须与 TOML 一致。

Core 在启动时加载 keyring。`active_key_id` 用于新秘密的加密，已保存的密文按其原 key ID
解密；改活动 ID 不会重加密历史记录。保留数据库恢复所需的全部旧密钥及 ID 映射，
并限制备份访问。当前没有密钥重加密或自动轮换工具，不能删掉仍被密文引用的 key。

公开状态 `two_factor_enabled` 只表示本进程已加载 MFA keyring，不能表示当前用户已启用
或已通过二次验证。对于已要求 MFA 的账号，未配置 keyring 或缺少可解密的本地因素时，
登录继续拒绝并返回 `MFA_UNAVAILABLE`，不会降级为仅验证密码。

本轮 identity schema 契约为 **v4**；主配置和 keyring 的 `version = 1` 是各自的配置格式，
不是数据库版本。v4 随完整 schema 只用于显式初始化全新空库。普通启动只校验版本、
指纹和运行权限，不把已有 v3 或更早数据库升级到 v4；不能修改版本记录或重跑 `init-db`
冒充升级。现有站点需要单独审查的备份、转换与恢复流程。

尚未完成的范围包括 Google 登录后的 MFA 续接、Passkey、管理员重置、旧 TOTP 秘密和
旧恢复码迁移，以及密钥重加密。已有 `second_factor_required` 策略必须保留，不能通过
关闭该策略、补造已验证会话或新建替代管理员来绕过这些缺项。

## 创建测试用户并验证 Key

`bootstrap-user` 是离线开发工具，不是注册、登录或 OAuth 接口。它只创建新用户，不覆盖已有用户；会话仅输出一次。不要将该命令公开为网页接口。

```sh
# 当前 shell 已设置 umask 077；拒绝覆盖现有凭证文件。
(set -C; printf '%s\n' '{"user_id":1,"platform_level":1}' |
  dc --profile tools run --rm -T core-init bootstrap-user \
  > deployment/docker/.secrets/identity-session.json)
```

以下示例不把真实会话和 Key 放进命令行参数、URL 或终端输出：

```sh
python3 - <<'PY'
import json
from pathlib import Path
from urllib.request import Request, urlopen
root = Path('deployment/docker/.secrets')
key_path = root / 'identity-key.json'
if key_path.exists():
    raise SystemExit('Test key file already exists.')
session = json.loads((root / 'identity-session.json').read_text())
request = Request('http://127.0.0.1:8080/core/v1/keys',
    data=json.dumps({'owner': {'kind': 'personal', 'id': 1}, 'ttl_seconds': 3600}).encode(),
    headers={'Authorization': 'Bearer ' + session['secret'], 'Content-Type': 'application/json'})
with urlopen(request, timeout=10) as response:
    issued = json.load(response)
with key_path.open('x') as file:
    file.write(json.dumps(issued))
key_path.chmod(0o600)
request = Request('http://127.0.0.1:8080/core/v1/identity',
    headers={'Authorization': 'Bearer ' + issued['secret']})
with urlopen(request, timeout=10) as response:
    identity = json.load(response)
assert identity['user_id'] == 1
assert identity['funding_account_ids'] == [identity['owner_account_id']]
print('Test key authorized with its personal account.')
PY
```

HTTP 的 `owner` 输入仍使用明确类型的公开标识：个人为用户 ID，团队为团队 ID。不要将内部 `owner_account_id` 填进旧 `owner.id`。HTTP 身份响应另外给出内部账号 ID；Protobuf `AccountRef.id` 的原有含义保持不变。

## 已接通接口

| 接口 | 规则 |
| --- | --- |
| `GET /core/v1/identity` | 会话或 API Key，读取身份与当前允许的付款顺序。 |
| `POST /core/v1/keys` | 会话；`owner`、`ttl_seconds`、可选 `funding_policy`；新凭证只返回一次。 |
| `DELETE /core/v1/credentials/{id}` | 会话；只能撤销自己的凭证，可重复执行。 |
| `POST /core/v1/teams` | 会话；每人最多创建一个团队。 |
| `GET /core/v1/teams?after=0` | 会话；按团队 ID 分页，每页最多 100 项。 |
| `POST /core/v1/teams/{id}/invites` | 会话；接收人 ID、角色、消费许可和有效期。 |
| `POST /core/v1/invites/accept` | 接收人会话；JSON `token`；重新检查双方权限与版本。 |
| `DELETE /core/v1/teams/{team}/members/{user}?version=1` | 会话及预期成员版本；旧版本被拒绝。 |

请求要求单个 `Authorization: Bearer ...`，响应禁止缓存。JSON 请求体上限 16 KiB，错误不暴露数据库详情。会话与邀请最长 7 天，Key 最长 365 天。数据库只保存凭证摘要，不保存明文密钥。

成员退出后保留授权版本，重新加入不会恢复旧 Key 或旧邀请。成员变更与审计在同一事务中提交，审计失败必须回滚。未来预算累计必须绑定稳定账号或团队—用户关系，不能因授权版本变化而清零。

## 验证和未完成项

将 `DATABASE_URL` 指向独立测试 PostgreSQL，测试角色需可创建临时数据库。SQLx 只负责建立隔离测试数据库；测试显式调用全新安装方法，不执行旧数据库升级。缺少数据库时测试失败，不跳过。

```sh
(cd services/core && cargo fmt --all --check)
(cd services/core && cargo clippy --locked --all-targets -- -D warnings)
(cd services/core && cargo test --locked --all-targets)
python3 -B scripts/test-core-rpc-docker.py
```

测试覆盖非空库保护、并发安装、结构版本不匹配、账号关系、付款规则、权限分离、并发邀请、退出重入、审计回滚、凭证过期/撤销及数据库故障。

当前源码已提供密码注册/登录、浏览器会话续期及上述 Google 桥接；具体安装仍需匹配配置和独立验收，不能据此声称管理员激活、MFA 全链、所有提供者或旧站点迁移已完成。身份响应只是当前权限快照，不是资金预留许可。`/health/ready` 取决于受限运行角色及启用组件；模型默认关闭，启用后的 readiness 也不证明实际供应商调用或扣费成功。其余业务及资金范围按[业务验收](microkernel-business-acceptance.md)记录。
