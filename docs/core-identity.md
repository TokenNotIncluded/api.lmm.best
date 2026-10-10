# Rust 原生账号与身份（全新安装 WIP）

核心数据库只接受全新安装，不导入旧用户、Key 或余额，不维护旧表的升级路径。数据库定义位于 `apps/lmm-core/schema/identity.sql`。Go 扩展不得连接这个数据库。

先按 [Docker 指南](../deployment/docker/README.md) 创建隔离开发栈，并在当前 Bash 会话中定义其中的 `dc` 和 `de`。`core-admin init-db` 只安装空库，第二次执行拒绝；服务启动不会执行建表、改表或修复。版本和指纹检查是安装契约检查，不是对任意人工改表的完整检测。

## 账号与权限分开

`accounts.id` 是核心内部资源与资金归属标识。`users.id` 是登录人标识，`teams.id` 是公开团队标识，三者不能混用。用户指向自己的个人账号，团队指向独立团队账号。

平台角色单独保存为 `user`、`admin`、`superadmin`。账号服务等级单独保存为 L0–L4。兼容界面输出仍可显示 L5（管理员）、L6（超级管理员），但这不授予团队消费权，也不代表管理员的个人账号自动获得更高服务权益。

Key 保存 `owner_account_id`；每个付款候选保存于 `key_funding_rules` 的外键和顺序字段中，不再隐藏在 JSON 内。账号内部的订阅/钱包顺序单独保存于 `core_billing.account_policies`。这些是授权与付款规则，不是余额或扣费引擎。

个人 Key 默认只允许个人账号；明确配置后才能添加当前已获消费授权的团队。团队 Key 只能使用所属团队，不能回退到个人或其他团队。服务凭证和平台管理角色均不能代替用户的消费授权。

## 创建测试用户并验证 Key

`bootstrap-user` 是离线开发工具，不是注册、登录或 OAuth 接口。它只创建新用户，不覆盖已有用户；会话仅输出一次。不要将该命令公开为网页接口。

```sh
# 当前 shell 已设置 umask 077；拒绝覆盖现有凭证文件。
(set -C; printf '%s\n' '{"user_id":1,"platform_level":1}' |
  dc --profile tools run --rm -T core-admin bootstrap-user \
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
(cd apps/lmm-core && cargo fmt --all --check)
(cd apps/lmm-core && cargo clippy --locked --all-targets -- -D warnings)
(cd apps/lmm-core && cargo test --locked --all-targets)
python3 -B scripts/test-core-rpc-docker.py
```

测试覆盖非空库保护、并发安装、结构版本不匹配、账号关系、付款规则、权限分离、并发邀请、退出重入、审计回滚、凭证过期/撤销及数据库故障。

正式注册和登录/OAuth、会话续期、邀请拒绝/撤回、创始人转移、完整成员编辑、预算、账本、订阅权益和真实计费尚未完成。身份响应只是当前权限快照，不是资金预留许可。核心 `/health/ready` 和模型接口继续返回 503。
