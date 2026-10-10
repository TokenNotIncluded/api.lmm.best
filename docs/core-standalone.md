# 独立运行 Rust 核心（低内存 WIP）

此部署不包含 Go、前端、工具市场或扩展宿主。核心可独立启动身份、团队和 API Key 服务；模型路由、实际扣费、充值、退款尚未接通，正式模型接口始终返回 503。不要将它替换当前生产网关，也不要连接生产数据库。

## 内存策略

低内存配置面向一台 1 核 / 1 GiB 的节点，**不是已测得的 RSS 或吞吐量**。运行服务的配置如下：

| 项目 | 默认核心配置 | 调整范围 |
| --- | --- | --- |
| Tokio I/O 工作者线程 | 1 | `LMM_CORE_WORKER_THREADS=1..4` |
| 身份 PostgreSQL 连接池上限 | 2 | `LMM_CORE_DB_CONNECTIONS=1..8` |
| 同时运行的密码哈希任务 | 1 | `LMM_CORE_PASSWORD_SLOTS=1..2` |
| 已接纳的 HTTP 请求及后台完成任务 | 16 | `LMM_CORE_MAX_INFLIGHT=1..64` |
| Tokio 阻塞线程上限 | 4 | 固定 |
| 身份及可选 RPC 池的空闲连接回收 | 30 秒 | 固定 |
| 独立核心容器内存硬限制 | 256 MiB | Compose 覆盖参数 |

Argon2id 继续使用 19,456 KiB、两次迭代和原有校验规则。减少并发不等于降低密码安全。内存硬上限是安全预算，不是实际消耗；低于上限并不能证明无 OOM。

RPC 未配置时，不创建额外 RPC 监听器及其数据库池。只有需要扩展的机器才配置 `compose.rpc-core.yml`。不要把所有模块和 PostgreSQL 都塞入同一台 1 核 / 1 GiB 主机后直接套用这些限额。

## 单独部署核心

准备一个**全新、隔离的 PostgreSQL 数据库**，为核心提供独立账户和最小必要权限，不复用现有站点数据库。此处假定 PostgreSQL 运行在另一台机器上，已有经过验证的私有网络或 TLS 保护。

把连接地址存入仅本机管理员可读的绝对路径文件（例如 `/secure/lmm-core/database-url`），文件里只放完整 PostgreSQL URL，一行即可。不要把凭据粘贴到命令行、日志或仓库中。

```sh
export LMM_CORE_DATABASE_URL_SECRET_FILE=/secure/lmm-core/database-url
dc() {
  docker compose -f deployment/docker/compose.core.yml \
    -f deployment/docker/compose.core-standalone.yml "$@"
}
dc config --services
# 预期只有 core；不能列出 extensions、core-db。
dc build core
# 仅新空库一次；绝不能对生产库执行。
dc run --rm -T --entrypoint /usr/local/bin/lmm-core-admin core init-db
dc up -d --no-deps core
curl --fail http://127.0.0.1:18080/health/live
curl -i http://127.0.0.1:18080/health/ready
```

最后一条预期为 503：这是尚未连接计费模型的安全行为，不是核心进程崩溃。若初始化失败，检查数据库网络、文件权限和 schema；不要执行清空数据库或跳过 schema 检查。

也可只安装已经发布并验证签名的对应架构 `lmm-core` 和 `lmm-core-admin` 二进制，用 systemd 管理。核心自身无需 Docker 或 Go，但仍必须配置数据库、运行用户、监听地址、重启及资源限制；不要把未签名的预览包当正式发行包。

## 真实测量后再调小

先在隔离测试机采集：空闲 5 分钟 RSS/PSS、8 和 16 并发、长流、失败登录、数据库断线恢复、请求完成后的资源回收、OOM 次数和端到端延迟。分开测量 core 和 PostgreSQL；数据库若在远端，不能把 DB 的内存写成核心的 RSS。

```sh
dc ps -q core
docker stats --no-stream "$(dc ps -q core)"
docker inspect --format '{{.State.OOMKilled}} {{.State.RestartCount}}' "$(dc ps -q core)"
```

建议继续跟踪单进程内存和 cgroup 内存（包含页缓存）；Docker `mem_limit` 不是 RSS。压缩容器镜像主要节省磁盘和下载量，不能直接当作常驻内存的改善。与研究目录 `experiments/deploy/dp-25`、`dp-26` 的实验结果应分开存证。

## 安全边界

- 内存不足时拒绝额外请求，不省略权限、预留、结算或持久化。
- 密码哈希参数、SQL 事务和资金证据不因低内存配置而降低安全等级。
- 扩展宕机不能阻止核心身份功能；但核心本身仍依赖 PostgreSQL。
- 独立部署不等于整站业务已迁移，也不保证单节点滚动更新不断流。
