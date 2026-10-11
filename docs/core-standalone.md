# 独立运行 Rust 核心（低内存 WIP）

此部署不包含 Go、前端、工具市场或扩展宿主；专用容器网络也不与扩展共享。核心可独立启动身份、团队和 API Key 服务。模型入口默认关闭；当前可选模型配置仅支持显式开启的数字回环地址合成上游，流式和外部供应商尚未开放，支付 HTTP 业务也未挂载。身份 readiness 不代表整站或线上计费网关已完成迁移。不要将它替换当前生产网关，也不要连接生产数据库。

真实快照的身份 ETL 已在隔离克隆库与独立本地核心目标库之间验证，完成时覆盖 888 位旧用户；资金记录、API Key 和 session 未迁移，生产未写入。这是一次身份迁移基线，不是当前用户总数、全部账号可登录或完整业务迁移的证明。

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

## 独立部署前的账户权限阻断

准备一个**全新、隔离的 PostgreSQL 数据库**，不复用现有站点数据库。初始化 owner 与网络服务的受限 runtime 必须使用分开的凭据，并离线显式授予 runtime 所需权限。现有 `compose.core-standalone.yml` 只接收一个 URL；以下旧流程用它同时初始化和运行，会因 owner 账户被运行时安全检查拒绝，不能直接照抄。角色分离、授权步骤和此 Compose 流程的实际部署验收仍待完成。

运行服务的 URL 应保存受限 runtime 凭据，owner 凭据只用于离线管理，不挂载给运行服务。凭据必须通过受控的绝对路径文件提供，不能粘贴到命令行、日志或仓库中。具体授权约束见[运行时权限检查](../services/core/src/runtime_database.rs)；普通启动不会自动创建角色或执行 GRANT。

```text
# 历史记录：同一 URL 不能兼任初始化 owner 和运行账户，不可直接执行。
export LMM_CORE_DATABASE_URL_SECRET_FILE=/secure/lmm-core/database-url
dc() {
  docker compose -f deployment/docker/compose.core.yml \
    -f deployment/docker/compose.core-standalone.yml "$@"
}
dc config --services
# 预期只有 core；不能列出 extensions、core-db。
dc build core
# 仅新空库一次；绝不能对生产库执行。
dc run --rm -T core init-db
dc up -d --no-deps core
curl --fail http://127.0.0.1:18080/health/live
curl -i http://127.0.0.1:18080/health/ready
```

`lmm-core init-db` 现在显式安装 8 个组件，见[schema 说明](../services/core/schema/README.md)。普通启动先检查受限账户权限和完整 catalog 指纹，不自动安装或升级。上面的 owner URL 会使运行进程在监听前退出，不能把它解释为模型未启用导致 readiness 503。正确配置并通过身份与数据库检查后，`/health/ready` 可以返回 200；模型默认关闭时 `/health/model-ready` 仍为 503。若初始化失败，检查数据库网络、文件权限和 schema；不要清空数据库或跳过检查。

也可使用[原生 Linux 预览包](../packaging/linux/README.md)安装对应架构的 `lmm-core`，用 systemd 或 OpenRC 管理。数据库初始化使用同一程序的 `lmm-core init-db`；没有独立管理程序。原生服务只应读取受限 runtime URL，安装包钩子不自动初始化或授权。核心自身无需 Docker 或 Go，但仍必须配置数据库、运行用户、监听地址、重启及资源限制。预览包及其校验和不代表正式签名发行或生产验收。

## 真实测量后再调小

先在隔离测试机采集：空闲 5 分钟 RSS/PSS、8 和 16 并发、长流、失败登录、数据库断线恢复、请求完成后的资源回收、OOM 次数和端到端延迟。分开测量 core 和 PostgreSQL；数据库若在远端，不能把 DB 的内存写成核心的 RSS。

以下测量仅在账户分离、授权和启动已独立完成并验证后执行；历史安装示例不提供这个前提。

```sh
dc ps -q core
docker stats --no-stream "$(dc ps -q core)"
docker inspect --format '{{.State.OOMKilled}} {{.State.RestartCount}}' "$(dc ps -q core)"
```

建议继续跟踪单进程内存和 cgroup 内存（包含页缓存）；Docker `mem_limit` 不是 RSS。压缩容器镜像主要节省磁盘和下载量，不能直接当作常驻内存的改善。与[部署研究](deployment-research.md)中的 DP-25、DP-26 历史组件结果应分开存证。

## 安全边界

- 内存不足时拒绝额外请求，不省略权限、预留、结算或持久化。
- 密码哈希参数、SQL 事务和资金证据不因低内存配置而降低安全等级。
- 扩展宕机不能阻止核心身份功能；但核心本身仍依赖 PostgreSQL。
- 独立部署不等于整站业务已迁移，也不保证单节点滚动更新不断流。
