# Rust 核心与 Go 扩展：Docker 开发栈（WIP）

这不是当前 Go 后端的生产替代品。Rust 核心现在只有领域规则、健康检查和安全关闭。
`/health/live` 返回 200；`/health/ready` 和所有业务接口返回 503。
Go 扩展宿主支持显式模块注册、独立路径和服务凭证，**尚未迁入工具市场等业务模块**。
不会读取生产用户、连接生产数据库、扣款或转发模型请求。

## 本地启动

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
# 预期 503：尚未具备业务接流条件，不能用于负载均衡的业务就绪检查。
curl -i http://127.0.0.1:18080/health/ready
```

目录 0700 限制宿主机访问；只读文件使容器内非 root 用户能够读取。
Compose 本地文件型 secret 不是加密密钥库。生产应使用部署平台的密钥管理，
不得提交 `.secrets`、密钥、真实用户数据或 `.env`。服务间凭证不是用户消费授权。

## 只更新 Go 扩展

以下 `--build` 命令仅用于开发机。生产发布应在独立构建机生成镜像，再按固定摘要拉取和更新。
不要在资源紧张的核心宿主机编译 Go；容器运行时的资源上限不约束 Docker 构建过程。

```sh
docker compose -f deployment/docker/compose.extensions.yml up -d --build --no-deps extensions
```

这是独立 Compose 项目。配置中没有 Rust 核心、数据库或其数据卷，因此该命令
不会重建它们。不要合并两个 Compose 文件，不要执行核心项目的 `down`。
Go 有单独的 CPU、内存、进程上限；核心不等待 Go 健康检查，也不向 Go 查询鉴权或账务。
示例内存值是开发默认值，未经过生产负载测试，应按实测调整。

两者暂时共享 `lmm-core-runtime` 网络，先启动核心创建网络。该网络不是权限边界；
后续核心内部接口必须独立认证、按模块限制权限，不能仅因来自 Docker 网络就信任请求。
主机端口默认仅绑定 127.0.0.1。尚未接入生产反向代理或自动发布。

## 停止开发栈

```sh
docker compose -f deployment/docker/compose.extensions.yml down
docker compose -f deployment/docker/compose.core.yml down
```

## 核心升级与长连接

单个容器重启不能保证正在输出的 token 不断。30 分钟 `stop_grace_period` 只是
关闭等待上限，不是无损保证。生产切换前必须实现：两个核心实例、真正的业务就绪检查、
旧实例停止接收新请求、等待已接收的 SSE/WebSocket 请求结算后退出，以及超时后的人工处置。
不能将“进程还活着”当作“可以接流量”。主机、数据库故障也不由这份 Compose 自动容错。

`python3 -B scripts/test-core-docker.py` 会实际构建两个镜像，检查进程健康、业务拒绝、
模块服务认证，以及扩展崩溃、重启和重建前后的核心容器 ID、启动时间和运行状态。
这只是容器隔离验收，不是模型流、旧 API Key 或真实账务验收。

完整阶段与上线门槛见 [迁移计划](../../docs/core-migration.md)。
