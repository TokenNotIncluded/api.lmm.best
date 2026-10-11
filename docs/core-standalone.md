# 独立运行 Rust 核心（低内存 WIP）

独立核心不需要 Go、前端、工具市场或扩展宿主。模型及可选业务入口默认关闭；身份 readiness 不代表模型、支付或整站已完成迁移。不要将它替换当前生产网关，也不要连接生产数据库。运行配置统一使用 TOML，文件位置和保留规则见[配置与文件路径](filesystem-layout.md)。

真实快照的身份 ETL 已在隔离克隆库与独立本地核心目标库之间验证，完成时覆盖 888 位旧用户；资金记录、API Key 和 session 未迁移，生产未写入。这是一次身份迁移基线，不是当前用户总数、全部账号可登录或完整业务迁移的证明。

## 内存策略

低内存配置面向一台 1 核 / 1 GiB 的节点，**不是已测得的 RSS 或吞吐量**。运行服务的配置如下：

| 项目 | 默认核心配置 | 调整范围 |
| --- | --- | --- |
| Tokio I/O 工作者线程 | 1 | `server.worker_threads=1..4` |
| 身份 PostgreSQL 连接池上限 | 2 | `database.max_connections=1..8` |
| 同时运行的密码哈希任务 | 1 | `identity.password_slots=1..2` |
| 已接纳的 HTTP 请求及后台完成任务 | 16 | `server.max_inflight=1..64` |
| Tokio 阻塞线程上限 | 4 | 固定 |
| 身份及可选 RPC 池的空闲连接回收 | 30 秒 | 固定 |
| 独立核心容器内存硬限制 | 256 MiB | Compose 覆盖参数 |

Argon2id 继续使用 19,456 KiB、两次迭代和原有校验规则。减少并发不等于降低密码安全。内存硬上限是安全预算，不是实际消耗；低于上限并不能证明无 OOM。

RPC 未配置时，不创建额外 RPC 监听器及其数据库池。只有需要扩展的机器才配置 `compose.rpc-core.yml`。不要把所有模块和 PostgreSQL 都塞入同一台 1 核 / 1 GiB 主机后直接套用这些限额。

## 配置与独立数据库权限

准备一个**全新、隔离的 PostgreSQL 数据库**，不复用现有站点数据库。初始化 owner 与网络服务的受限 runtime 必须使用分开的凭据，并离线显式授予 runtime 所需权限。当前 Docker 角色与挂载契约以 [Docker 指南](../deployment/docker/README.md)及[离线角色说明](../deployment/postgres/README.md)为准；旧的单 URL 流程不能代替它们。

运行服务的 URL 应保存受限 runtime 凭据，owner 凭据只用于离线管理，不挂载给运行服务。凭据必须通过受控的绝对路径文件提供，不能粘贴到命令行、日志或仓库中。具体授权约束见[运行时权限检查](../services/core/src/runtime_database.rs)；普通启动不会自动创建角色或执行 GRANT。

原生服务使用 `/etc/lmm.conf.d/core.toml`，参考 [core.example.toml](../config/core.example.toml)。`database.url_file` 指向 `/etc/lmm.conf.d/secrets/core/database-url` 中的受限 runtime URL，私密文件由服务账户持有、权限 `0600`；状态目录为 `/var/lib/lmm/core`，临时目录为 `/run/lmm/core`。配置只读固定文件或显式绝对路径，不扫描目录，不从环境变量混合覆盖。

离线初始化使用另一个操作者私有 TOML，例如 `lmm-core --config /absolute/private/core-owner.toml init-db`。这个命令仅对已明确选定的全新隔离库执行一次。它安装[完整 schema 契约](../services/core/schema/README.md)，普通启动先检查受限账户权限和完整 catalog 指纹，不自动安装或升级。将 owner 凭据交给网络服务会在监听前失败。配置正确并通过身份与数据库检查后，`/health/ready` 可以返回 200；模型默认关闭时 `/health/model-ready` 仍为 503。若初始化失败，检查数据库网络、文件权限和 schema；不要清空数据库或跳过检查。

使用[原生 Linux 预览包](../packaging/linux/README.md)时，systemd 或 OpenRC 模板传入 `--config /etc/lmm.conf.d/core.toml`。安装包不自动初始化或授权，不自动启动服务；操作者须明确准备服务账户、秘密、数据库权限和资源限制。核心自身无需 Docker 或 Go。预览包及其校验和不代表正式签名发行或生产验收。

默认日志进入 journal；软件包不预置文件日志内容。默认独立核心没有 RPC，共享 socket 只在明确启用 `rpc.shared_group_id` 并完成 `lmm-rpc` 账户、IPC 目录和 token 权限配置后使用。核心私密目录与持久状态始终不向扩展共享。可恢复 API Key 使用独立、持久的 32 字节 `identity.api_key_encryption_key_file`，不能借用 Web refresh key；当前尚无 keyring 迁移，换主密钥会使旧密文揭示失败。

## 真实测量后再调小

先在隔离测试机采集：空闲 5 分钟 RSS/PSS、8 和 16 并发、长流、失败登录、数据库断线恢复、请求完成后的资源回收、OOM 次数和端到端延迟。分开测量 core 和 PostgreSQL；数据库若在远端，不能把 DB 的内存写成核心的 RSS。

以下测量仅在账户分离、授权和启动已独立完成并验证后执行；历史安装示例不提供这个前提。

```sh
mise exec -- docker --host unix:///var/run/docker.sock stats --no-stream "$LMM_LOCAL_CORE_CONTAINER"
mise exec -- docker --host unix:///var/run/docker.sock inspect --format '{{.State.OOMKilled}} {{.State.RestartCount}}' "$LMM_LOCAL_CORE_CONTAINER"
```

建议继续跟踪单进程内存和 cgroup 内存（包含页缓存）；Docker `mem_limit` 不是 RSS。压缩容器镜像主要节省磁盘和下载量，不能直接当作常驻内存的改善。与[部署研究](deployment-research.md)中的 DP-25、DP-26 历史组件结果应分开存证。

## 安全边界

- 内存不足时拒绝额外请求，不省略权限、预留、结算或持久化。
- 密码哈希参数、SQL 事务和资金证据不因低内存配置而降低安全等级。
- 扩展宕机不能阻止核心身份功能；但核心本身仍依赖 PostgreSQL。
- 独立部署不等于整站业务已迁移，也不保证单节点滚动更新不断流。
