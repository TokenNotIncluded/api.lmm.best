# 全新微内核 Docker 开发栈（WIP）

本分支只面向新安装，不升级、导入或转换旧数据库。不要连接生产数据，不要把本栈部署成现有网站的替代品。

Rust 位于 `apps/core-rust`。Go 扩展的唯一目录为 `apps/api-go`，不再有 `apps/extensions-go` 或旧单体运行入口。目前可用的是身份、账号、团队与只读内部查询；商店、工具市场、助手、客服、支付等业务尚未接回。账本、预算、模型转发和协议转换也尚未完成。

核心 `/health/live` 返回 200；`/health/ready` 和模型接口仍返回 503。身份校验成功不等于允许收费或接入模型流量。扩展健康检查只证明宿主可响应，不保证每项业务已经实现。

## 在隔离开发机创建新栈

以下命令从仓库根目录执行，要求 Docker Engine、Compose v2、Bash 和 Python 3。使用空闲的本地 8080/8081 端口。本指南不使用现有生产 Compose 项目、数据库或密钥。

生成四份新凭证，已有文件时拒绝覆盖。宿主目录限制为 0700，文件只读以供容器专用 UID 读取。文件型 secret 不是加密密钥库；不要将此目录提交到 Git。

```sh
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
dc --profile tools run --rm -T core-admin init-db
dc up -d core
de up -d --build extensions
curl -f http://127.0.0.1:8080/health/live
curl -f http://127.0.0.1:8081/health/live
# 预期为 503。
curl -i http://127.0.0.1:8080/health/ready
```

`init-db` 在一个事务中安装 `schema/identity.sql`。数据库已有应用对象时拒绝，第二次执行同样拒绝，不会删表或清空数据。普通启动只检查结构版本和定义指纹，不自动安装、升级或修复。

数据库只加入内部 `identity` 网络，不映射宿主端口。只有核心和离线管理命令可以访问。Go 不获得数据库网络、密码或数据卷。开发 Compose 使用单一数据库角色；生产权限拆分仍需专门实现和验证，不能把此配置当成已经完成最小权限部署。

核心与扩展是两个独立 Compose 项目。共同网络名称是 `lmm-core-backplane`；核心先创建网络与 socket 卷，扩展只引用外部资源。socket 目录在 Go 容器中只读。详细接口见 [身份指南](../../docs/core-identity.md) 和 [通信指南](../../docs/core-protocol.md)。

## 只更新扩展

保持上面 `dc`、`de` 的完整文件组合。不要为了更新 Go 重建核心或数据库。

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
python3 -B scripts/test-core-rpc-docker.py
```

Docker 脚本只操作随机命名的临时测试项目，检查新库安装、重复安装拒绝、实际 Protobuf 查询、团队权限、扩展独立重建、核心重启重连、数据库故障与恢复、Key 撤销。脚本最后仅清理自己创建的测试数据卷。

单容器重启无法保证正在输出的 token 不断。当前核心停止等待上限为 120 秒，扩展为 30 秒；这些只是等待上限，不是无损承诺。双核心切换、真实长流、结算排空、数据库故障恢复和性能测试仍是上线前的独立验收项目。本栈未接入生产反向代理或自动发布。
