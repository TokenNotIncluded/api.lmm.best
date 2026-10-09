# Rust 原生身份开发指南

本功能属于 #675 的 P1 部分，只用于隔离开发环境。
不连接生产数据库，不导入真实用户，不替换现有 Go 登录和 API Key。
即使身份接口返回 200，模型接口和 `/health/ready` 仍返回 503。

## 启动独立身份栈

在仓库根目录执行。需要 Docker Engine 和 Compose v2。
只使用为此开发栈新建的密钥；命令不会覆盖已存在的文件。

```sh
umask 077
python3 - <<'PY'
from pathlib import Path
import secrets
root = Path('deployment/docker/.secrets')
root.mkdir(mode=0o700, parents=True, exist_ok=True)
password = secrets.token_hex(32)
values = {
    'identity-password': password,
    'identity-url': f'postgres://lmm_core:{password}@core-db:5432/lmm_core',
}
for name, value in values.items():
    path = root / name
    with path.open('x') as file:
        file.write(value + '\n')
    path.chmod(0o444)
PY
export LMM_CORE_DATABASE_PASSWORD_SECRET_FILE="$PWD/deployment/docker/.secrets/identity-password"
export LMM_CORE_DATABASE_URL_SECRET_FILE="$PWD/deployment/docker/.secrets/identity-url"

dc() {
  docker compose -f deployment/docker/compose.core.yml \
    -f deployment/docker/compose.identity.yml "$@"
}

dc build core
dc up -d --wait core-db
dc run --rm -T core-admin migrate
printf '%s\n' '{"user_id":1,"platform_level":1}' |
  dc run --rm -T core-admin bootstrap-user > deployment/docker/.secrets/identity-session.json
dc up -d core
```

`migrate` 是显式管理操作，可重复执行。普通服务启动只检查表版本，不执行迁移。
`bootstrap-user` 只创建新测试用户，不更新已有用户，凭证只输出一次。
它不是正式注册、密码登录或 OAuth 接口；不得公开成网页接口。
文件型 secret 不是加密密钥库。目录权限必须保持 0700，不要将 `.secrets` 加入版本控制。
Compose 的默认数据库账号只适合隔离开发。生产还需分离迁移账号与运行账号权限。

数据库不映射宿主端口，只加入内部 `identity` 网络。
核心与管理命令可访问该网络；Go 扩展没有数据库网络、凭证或数据卷。
身份数据库与现有 Go 数据库完全分开，不能通过直接复制表冒充兼容迁移。

## 签发并验证一个测试 Key

下面读取私有会话文件，不把会话或 Key 放进命令行参数、终端输出或 URL。

```sh
python3 - <<'PY'
import json
from pathlib import Path
from urllib.request import Request, urlopen
root = Path('deployment/docker/.secrets')
session = json.loads((root / 'identity-session.json').read_text())
body = json.dumps({'owner': {'kind': 'personal', 'id': 1}, 'ttl_seconds': 3600}).encode()
request = Request('http://127.0.0.1:18080/core/v1/keys', data=body, headers={
    'Authorization': 'Bearer ' + session['secret'], 'Content-Type': 'application/json',
})
with urlopen(request, timeout=10) as response:
    issued = json.load(response)
with (root / 'identity-key.json').open('x') as file:
    file.write(json.dumps(issued))
(root / 'identity-key.json').chmod(0o600)
request = Request('http://127.0.0.1:18080/core/v1/identity', headers={
    'Authorization': 'Bearer ' + issued['secret'],
})
with urlopen(request, timeout=10) as response:
    print(json.load(response))
PY
```

个人 Key 未指定付款顺序时，只能使用个人余额。
团队 Key 的 `owner` 为 `{"kind":"team","id":团队ID}`；不能回退到个人账号。
只有当前成员具有独立消费授权时，才可签发并使用团队资金 Key。

## 已实现接口

所有接口要求一个 `Authorization: Bearer ...` 请求头，返回 `Cache-Control: no-store`。
JSON 请求体上限 16 KiB。错误仅返回通用代码，不包含数据库连接串或凭证。

| 接口 | 凭证与参数 |
| --- | --- |
| `GET /core/v1/identity` | 会话或 API Key，返回当前身份与允许的付款账号顺序。 |
| `POST /core/v1/keys` | 会话；`owner`、`ttl_seconds`、可选 `funding_policy`；201，只返回一次新 Key。 |
| `DELETE /core/v1/credentials/{id}` | 会话；只撤销自己的凭证；成功 204，可重复执行。 |
| `POST /core/v1/teams` | 会话；创建团队，每人最多创建一个。 |
| `GET /core/v1/teams?after=0` | 会话；按 ID 分页，每页最多 100 个；下一页传最后一个 ID。 |
| `POST /core/v1/teams/{id}/invites` | 会话；`recipient_user_id`、`role`、`can_spend`、`ttl_seconds`；返回一次邀请令牌。 |
| `POST /core/v1/invites/accept` | 接收人会话；JSON `token`；重新检查邀请人与接收人权限。 |
| `DELETE /core/v1/teams/{team}/members/{user}?version=1` | 会话与预期成员版本；移除成功 204，旧版本 409。 |

会话最长 7 天，邀请有效期不超过 7 天，新 Key 有效期不超过 365 天。
本批没有续期/刷新、邀请拒绝/撤回、创始人转移、完整成员编辑、团队预算或等级接口。
修改这些规则必须同步增加事务和权限测试，不得仅增加前端按钮。

身份查询是当前一致快照，不是资金预留，也不能凭这个结果放行付费模型请求。
成员版本变化会让已保存的旧团队授权失效。重新加入团队不会恢复旧 Key。

## 更新与测试

扩展更新仍只用 `compose.extensions.yml`，不要把扩展合进核心身份栈。
操作身份栈始终组合相同的两个文件；不要用基础栈命令重建已开启身份功能的核心。
只停止开发服务时使用 `dc down`，**不要添加 `--volumes`**，除非明确要删除测试数据。

```sh
python3 -B scripts/test-core-docker.py
python3 -B scripts/test-core-identity-docker.py
```

脚本只操作随机命名的测试项目、网络和数据卷，结束后清理这些测试资源。
新增脚本实测显式迁移、重复迁移、重启后 Key 有效、扩展崩溃/重建期间身份可用、
数据库失效拒绝访问、数据库恢复、Key 撤销，以及模型接口仍然关闭。

Rust 集成测试使用真实 PostgreSQL。先将 `DATABASE_URL` 指向隔离测试服务器，
测试账号必须有创建测试数据库的权限。SQLx 为每个测试创建独立数据库并执行迁移。
缺少数据库会失败，不会跳过测试：

```sh
cd apps/core-rust
cargo fmt --all --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked --all-targets
```

数据库与 HTTP 测试覆盖权限分离、L0/L6 边界、并发接受邀请、旧邀请与旧 Key 失效、
审计失败事务回滚、停用/过期/撤销检查和存储故障。Docker 检查不替代真实账务或长流验收。
