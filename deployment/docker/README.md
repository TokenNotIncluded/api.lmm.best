# Rust 核心与 Go 扩展：Docker 开发栈（WIP）

这不是当前 Go 后端的生产替代品。基础栈提供领域规则、健康检查和安全关闭。
可选身份栈已支持 PostgreSQL 用户、会话、Key、团队、邀请与权限验证。
`/health/live` 返回 200；`/health/ready` 和模型接口仍返回 503。
Go 扩展宿主支持显式模块注册、独立路径和服务凭证，**尚未迁入工具市场等业务模块**。
默认不读取生产用户、连接生产数据库、扣款或转发模型请求。

## 启用原生身份

需要持久化身份时，按 [身份开发指南](../../docs/core-identity.md) 组合
`compose.core.yml` 和 `compose.identity.yml`，显式创建隔离数据库并执行迁移。
身份栈使用独立数据卷、内部数据库网络和文件密钥，Go 扩展不加入数据库网络。
此后管理核心必须继续组合相同文件，不能用下面的纯基础栈命令重建身份核心。

## 启用 Protobuf 内部通信

按 [核心通信指南](../../docs/core-protocol.md) 分别叠加 `compose.rpc-core.yml`
和 `compose.rpc-extensions.yml`。Go 使用同一份 Protobuf 契约查询 Rust 的原生身份和团队，
通过私有 Unix socket 通信，不直接访问数据库。两个服务仍属于独立 Compose 项目。
Go 只读挂载 socket 目录，更新扩展不会删除该外部卷。
配置内部通信后，更新或停止服务必须继续使用通信指南中的完整文件组合。

## 只启动基础栈

在仓库根目录执行。需要 Docker Engine 和 Compose v2。
先生成仅用于这个开发栈的服务凭证。不要覆盖已有凭证，不要复制生产密钥：

```sh
python3 - <<'PYTHON'
from pathlib import Path
import os, secrets
p = Path('deployment/docker/.secrets/extensions-token')
p.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
with p.open('x') as f:
    f.write(secrets.token_hex(32) + '\n')
os.chmod(p, 0o444)
PYTHON

docker compose -f deployment/docker/compose.core.yml up -d --build core
docker compose -f deployment/docker/compose.extensions.yml up -d --build extensions
curl -f http://127.0.0.1:18080/health/live
curl -f http://127.0.0.1:18081/health/live
# 预期 503：未具备模型流量接入条件。
curl -i http://127.0.0.1:18080/health/ready
```

目录 0700 限制宿主机访问；只读文件使容器内非 root 用户能够读取。
Compose 本地文件型 secret 不是加密密钥库。生产应使用部署平台的密钥管理，
不得提交 `.secrets`、密钥、真实用户数据或 `.env`。服务间凭证不是用户消费授权。

## 只更新 Go 扩展

以下 `--build` 命令仅用于开发机。生产发布应在独立构建机生成镜像，再按固定摘要更新。
不要在资源紧张的核心宿主机编译 Go；运行时的资源上限不约束 Docker 构建过程。

```sh
docker compose -f deployment/docker/compose.extensions.yml up -d --build --no-deps extensions
```

扩展是独立 Compose 项目，配置中没有核心、数据库或核心数据卷，因此不会重建它们。
不要合并核心与扩展项目，不要为更新扩展执行核心项目的 `down`。
Go 有独立 CPU、内存和进程限制；核心不等待 Go 健康检查，也不向 Go 查询身份或账务。
示例内存值是开发默认值，未经过生产负载测试，应按实测调整。

两者共享 `lmm-core-runtime` 网络，先启动核心创建网络。共享网络不代表可信调用方，
核心身份接口仍需有效用户凭证。模块服务凭证不能代替用户会话。
主机端口默认只绑定 127.0.0.1。未接入生产反向代理或自动发布。

## 停止纯基础栈

```sh
docker compose -f deployment/docker/compose.extensions.yml down
docker compose -f deployment/docker/compose.core.yml down
```

开启身份栈的核心请用身份指南的 `dc down`，保持相同 Compose 文件组合。
不要添加 `--volumes`，除非明确要删除开发数据。

## 核心升级与长连接

单个容器重启不能保证正在输出的 token 不断。30 分钟 `stop_grace_period` 只是等待上限，
不是无损保证。生产切换前必须实现两个核心实例、真实业务就绪检查、停止旧实例接收新请求，
并等待已有 SSE/WebSocket 请求与结算完成，以及超时后的人工处置。
不能将“进程还活着”当作“可以接流量”。这份 Compose 不提供主机或数据库自动容错。

`python3 -B scripts/test-core-docker.py` 实际构建两个镜像，检查进程健康、模型请求拒绝、
模块服务认证，以及扩展崩溃、重启和重建前后的核心容器 ID、启动时间和运行状态。

`python3 -B scripts/test-core-identity-docker.py` 使用实际身份配置检查迁移、持久化凭证、
扩展故障隔离、数据库失效与恢复、Key 撤销。测试只清理随机命名的测试资源。

`python3 -B scripts/test-core-rpc-docker.py` 验证 Go 到 Rust 的实际 Protobuf 请求、
团队授权、扩展独立重建、核心重启后的重连，以及数据库故障恢复。
这些测试不是模型流、旧 API Key 迁移或真实账务验收。

完整阶段与上线门槛见 [迁移计划](../../docs/core-migration.md)。
