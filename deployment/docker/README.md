# 全新微内核 Docker 开发栈（WIP）

本分支的部署默认面向新安装，不自动升级或转换旧数据库。真实快照的身份 ETL 已在隔离克隆库与独立本地核心目标库之间验证，完成时覆盖 888 位旧用户；资金记录、API Key 和 session 均未迁移，也未写入生产。该结果不是整站迁移或 Docker 部署验收。不要连接生产数据，不要把本栈部署成现有网站的替代品。

Rust 位于 `services/core`。Go 扩展的唯一目录为 `services/extensions`，不再有 `apps/extensions-go` 或旧单体运行入口。身份、账号、团队和只读内部查询已有宿主入口。账本、计费、路由、转发及业务扩展需要按实际装配和测试分别验收，不能从模块存在推导整站已迁移。模块状态见[架构指南](../../docs/core-migration.md)。

核心成功启动后，`/health/live` 返回 200。配置受限数据库账户、完整 schema 和身份服务并通过检查后，`/health/ready` 可以返回 200，即使 `models` 为 `false`；模型入口默认关闭，`/health/model-ready` 此时返回 503。当前可选模型配置只支持显式开启的数字回环地址合成上游，流式和外部供应商尚未开放。readiness 不代表模型供应商健康、支付已挂载或功能已对齐 main。扩展健康检查只证明宿主可响应，不保证每项业务已经实现。

**当前 Docker 配置存在启动阻断。** `compose.identity.yml` 把同一数据库 URL 提供给 `core-init` 和运行服务；下面历史示例生成的是初始化 owner 账户。新的运行时权限检查会在监听前拒绝该账户，而不是启动后仅返回 readiness 503。初始化 owner 与受限 runtime 的凭据拆分、显式离线授权和 Docker 验收仍待完成。

## 仅部署 Rust 核心

核心可以不部署 Go 扩展；无需配置内部 RPC。若使用独立 PostgreSQL，请使用
[单核低内存部署说明](../../docs/core-standalone.md) 和
`compose.core-standalone.yml` 覆盖文件。该模式的服务清单只有 `core`，
不会启动 `extensions` 或本机 PostgreSQL；数据库仍是必要的独立依赖。
它只能运行已接通的身份/账户功能，不能替代线上计费网关。

## 历史新栈示例（当前不可直接执行）

以下保留旧流程供修复时核对，**不是当前已验收可用的安装方案，不要整段照抄执行**。它缺少 owner/runtime 账户分离及 GRANT 步骤。原流程从仓库根目录执行，要求 Docker Engine、Compose v2、Bash 和 Python 3；当前 Compose 默认宿主端口实际为 18080/18081，旧示例中的 8080/8081 也需同步修正。本指南不使用现有生产 Compose 项目、数据库或密钥。

生成四份新凭证，已有文件时拒绝覆盖。宿主目录限制为 0700，文件只读以供容器专用 UID 读取。文件型 secret 不是加密密钥库；不要将此目录提交到 Git。

```text
# 历史记录：运行账户权限尚未配置，以下流程不能完成当前核心启动。
umask 077
python3 - <<'PY'
from pathlib import Path
import secrets
root = Path('deployment/docker/.secrets')
root.mkdir(mode=0o700, parents=True, exist_ok=True)
root.chmod(0o700)
password = secrets.token_hex(32)
values = {
    'identity-password': password,
    'identity-url': f'postgres://lmm_core:{password}@core-db:5432/lmm_core',
    'core-rpc-token': secrets.token_hex(32),
    'extensions-token': secrets.token_hex(32),
}
if any((root / name).exists() for name in values):
    raise SystemExit('Credential file already exists; nothing was overwritten.')
for name, value in values.items():
    path = root / name
    with path.open('x') as file:
        file.write(value + '\n')
    path.chmod(0o444)
PY
export LMM_CORE_DATABASE_PASSWORD_SECRET_FILE="$PWD/deployment/docker/.secrets/identity-password"
export LMM_CORE_DATABASE_URL_SECRET_FILE="$PWD/deployment/docker/.secrets/identity-url"
export LMM_CORE_RPC_TOKEN_SECRET_FILE="$PWD/deployment/docker/.secrets/core-rpc-token"

dc() {
  docker compose -f deployment/docker/compose.core.yml \
    -f deployment/docker/compose.identity.yml \
    -f deployment/docker/compose.rpc-core.yml "$@"
}
de() {
  docker compose -f deployment/docker/compose.extensions.yml \
    -f deployment/docker/compose.rpc-extensions.yml "$@"
}

dc build core
dc up -d --wait core-db
# 一次性空库安装；不是可重复执行的升级命令。
dc --profile tools run --rm -T core-init
dc up -d core
de up -d --build extensions
curl -f http://127.0.0.1:8080/health/live
curl -f http://127.0.0.1:8081/health/live
# 历史预期，已过时；当前 owner 账户会使 core 在监听前退出。
curl -i http://127.0.0.1:8080/health/ready
```

`core-init` 是一次性 Compose 工具服务，调用同一个 `lmm-core init-db`，不是第四个程序。`init-db` 在一个事务中安装 8 个组件：identity、ledger、payments、charging、routing、events、charging-ledger 和 migration，并记录组件契约与完整 catalog 指纹。数据库已有应用对象时拒绝，第二次执行同样拒绝，不会删表或清空数据。普通启动检查运行账户权限、完整结构契约及实时 catalog 指纹，不自动安装、升级或修复。安装某个 schema 不等于对应 HTTP 业务已经挂载。

数据库只加入内部 `identity` 网络，不映射宿主端口。只有核心和离线管理命令可以访问。Go 不获得数据库网络、密码或数据卷。修复部署流程时，应只给离线初始化提供 owner 凭据，运行服务只挂载受限 runtime URL；显式授权由离线管理步骤完成，不能改成普通启动自动授权。权限要求见[运行时检查](../../services/core/src/runtime_database.rs)，[隔离测试中的授权设置](../../services/core/tests/common/runtime.rs)仅作为参考；启用 RPC 还需要 `core_events` 权限。runtime 不能拥有应用对象、切换到 owner，或直接写不可变账本表。当前 Compose 尚未实现这些步骤。

核心与扩展是两个独立 Compose 项目。共同网络名称是 `lmm-core-backplane`；核心先创建网络与 socket 卷，扩展只引用外部资源。socket 目录在 Go 容器中只读。详细接口见 [身份指南](../../docs/core-identity.md) 和 [通信指南](../../docs/core-protocol.md)。

## 只更新扩展

以下维护命令仅适用于已完成账户分离、授权并通过验收的隔离栈。保持 `dc`、`de` 的完整文件组合；历史示例中的函数定义不代表核心已可启动。不要为了更新 Go 重建核心或数据库。

```sh
de up -d --build --no-deps extensions
```

`--build` 只用于开发机。之后的发布应使用独立构建机和固定镜像摘要，不在资源紧张的核心主机编译。运行容器的资源限制不限制镜像构建过程。

Go 的 CPU、内存和进程名额独立配置。单个模块最多同时处理 8 个请求，满额立即返回 503；其他模块和健康检查不排在它后面。这不是不可信代码沙箱，也不能隔离宿主机或共享数据库的全部故障。

只停止开发服务、保留数据：

```sh
de down
dc down
```

不要添加 `--volumes`。不要用缺少身份或 RPC 覆盖文件的基础命令重建已经启用这些功能的核心。

## 验证与剩余边界

```sh
python3 -B scripts/test-core-boundaries.py
# 先修复账户分离、授权步骤及脚本，再执行 Docker 集成验收：
# python3 -B scripts/test-core-rpc-docker.py
```

现有 Docker 脚本仍使用同一 owner URL，尚未适配新的权限检查，不能作为当前部署已通过的证据。修复后应在随机命名的临时测试项目验证新库安装、重复安装拒绝、owner 运行拒绝、受限账户启动、实际 Protobuf 查询、团队权限、扩展独立重建、核心重启重连、数据库故障与恢复、Key 撤销，并仅清理本次创建的测试数据卷。文档检查或本机原生进程验收不能代替这项 Docker 验证。

单容器重启无法保证正在输出的 token 不断。当前 Compose 停止宽限为核心 30 分钟、扩展 35 秒；这不是无损承诺，也不能代替核心内部排空和持久化恢复验证。双核心切换、真实长流、结算排空、数据库故障恢复和性能测试仍是上线前的独立验收项目。本栈未接入生产反向代理或自动发布。

## 可选协议配置与独立分发

协议默认关闭。按[模板指南](../../docs/legal/README.md)准备 `site.json` 及模板目录后，给扩展的完整文件组合追加 `-f deployment/docker/compose.legal.yml`，并导出 `LMM_LEGAL_DIRECTORY`。该目录只读挂载；每次扩展更新必须保留相同覆盖文件。核心无需这些文件，也不因协议更新而重启。

镜像默认仍生成运行时镜像；`--target binaries` 只导出构建好的程序。不要把该空基础导出阶段当作可运行镜像。归档、校验和预览工作流见[打包指南](../../docs/release-architecture.md)。
