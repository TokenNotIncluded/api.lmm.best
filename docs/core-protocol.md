# Rust 核心与 Go 扩展通信（WIP）

使用 Protobuf 定义二进制消息，gRPC 发送请求和接收结果。同机容器通过私有 Unix socket 连接。这不是共享内存或持久消息队列，也不承载模型 token 输出。

Go 唯一源码目录为 `apps/api-go`。核心只有一个身份与账号权威来源，不从 Go 的旧用户或余额表回退。数据库只面向全新安装；部署命令见 [Docker 指南](../deployment/docker/README.md)。

## 方向与权限

Go 模块 → 复用的 gRPC 客户端 → 私有 socket → Rust → 独立只读数据库连接池。

Rust 不反向等待 Go，核心 HTTP 身份检查不经过扩展。Go 不持有核心数据库地址、密码或数据卷。用户身份不能由 Go 上传的角色、用户 ID 或付款账号代替。

契约为 `contracts/proto/lmm/core/v1/control.proto`，服务为 `lmm.core.v1.CoreControl`：

| 方法 | 作用 | 用户凭证 |
| --- | --- | --- |
| `Capabilities` | 查询协议主版本和已配置功能。 | 不需要，但仍需服务凭证。 |
| `Authorize` | 查询真实身份、Key 归属和有序付款候选。 | 会话或 API Key。 |
| `ListTeams` | 按 `after_id` 分页，最多 100 个团队。 | 必须是会话。 |

`identity_available` 只表示身份存储已配置，不保证数据库此刻可用，也不保证模型就绪。付款候选是权限快照，不是已预留余额。未来资金操作必须在自己的事务中重新校验。

`AccountRef.id` 仍是该 `kind` 下的公开用户或团队 ID，不因增加内部 `accounts.id` 而改变含义。新增需要内部账号标识的接口应使用明确的新字段，不能暗中改写已有字段语义。

内部调用分别携带服务凭证、协议主版本和用户凭证：`authorization`、`x-lmm-protocol`、`x-lmm-user-credential`。Rust 拒绝重复、缺失、无效凭证以及不支持的协议主版本。Go 客户端不允许外部元数据覆盖内部服务凭证。凭证不进入 Protobuf 消息正文、响应或日志，授权结果不缓存。

## 有界请求与失败处理

单条消息上限 64 KiB。Rust 最多接入 8 条内部连接，每连接最多 8 个请求流，实际查询还受全局 8 个名额限制。Go 客户端最多保留 32 个正在调用的请求；Go 模块入口另外限制每模块 8 个请求。超限立即失败，不无限排队。HTTP/2 流量窗口不是持久业务队列。

两端调用最多等待 2 秒，更短的调用期限仍有效。取消和超时释放名额，应用层不自动重试。以后添加资金写接口时，不得简单重放未知结果。

内部数据库池只使用 2 条连接，取连接最多等待 400 ms，语句最多执行 1500 ms，并设为只读。它不占用核心 HTTP 身份池的连接名额，但仍共享数据库和宿主机资源，不是完整故障隔离。

Go 将 gRPC 失败映射为明确 HTTP 状态，不返回底层 SQL 或密钥。核心离线时 Go 仍可启动；用户查询失败关闭，核心恢复后复用客户端重新连接。进程健康不等于业务就绪。

## 同机私有连接

没有内部 TCP 监听或明文跨主机回退。跨机器部署需要另外设计双向 TLS 与服务身份，不能把 socket 通过公开转发工具暴露。

核心是 socket 目录唯一写入方。目录 0700，socket 0600，两端使用相同专用 UID。Go 只读挂载目录，可以连接，但不能创建、替换或删除文件。核心启动时使用独占锁，避免覆盖其他实例持有的监听文件。

`compose.rpc-core.yml` 和 `compose.rpc-extensions.yml` 分别叠加到独立核心与扩展项目。更新扩展只操作扩展项目，不删除核心或外部 socket 卷。宿主文件用 `LMM_CORE_RPC_TOKEN_SECRET_FILE` 配置，卷名可用 `LMM_CORE_RPC_VOLUME` 配置。

进程配置要求同时设置 `LMM_CORE_RPC_SOCKET` 和 `LMM_CORE_RPC_TOKEN_FILE`。凭证当前在启动时读取，没有热轮换；更换内部凭证需要协调两端，不能只改 Go 文件就假设 Rust 已更新。

## 接入 Go 模块

模块只收到自己需要的接口和依赖，不获得全局数据库或任意 SQL 通道。`internal/modules/identity` 使用一个仅含三个只读方法的接口，由 `internal/coreclient.Client` 实现。

```text
GET /extensions/v1/identity/capabilities
GET /extensions/v1/identity/self
GET /extensions/v1/identity/teams?after_id=0
```

宿主先检查自己的 `extensions-token`，然后在进入模块前移除该 `Authorization` 请求头。需要用户身份的操作另外读取 `X-LMM-User-Credential` 并交给 Rust 验证。不能把宿主服务密钥发给浏览器，也不能将这些内部接口当成新的公共登录入口。

返回使用 Protobuf JSON，64 位 ID 为字符串。`LMM_EXTENSION_MODULES` 控制显式启用列表；`none` 关闭全部模块。默认只在 RPC 已配置时启用 identity。未知或重复名称使启动失败，新模块不会自动对所有部署启用。

这些模块是可信的编译期 Go 代码，不是不可信插件沙箱。需要进程级隔离时，使用独立扩展容器。当前只有 identity 模块，商店、工具市场、助手等业务尚未接回。

## 生成与验证

Rust 构建时从同一 `.proto` 生成绑定。Go 的生成文件提交入库，普通构建不需要 protoc。

```sh
bash scripts/generate-core-protocol.sh
bash scripts/generate-core-protocol.sh --check
(cd apps/api-go && go mod verify && go vet ./... && go test -race ./... -count=1)
# DATABASE_URL 必须指向可创建临时测试数据库的独立 PostgreSQL。
(cd apps/core-rust && cargo fmt --all --check && cargo clippy --locked --all-targets -- -D warnings && cargo test --locked --all-targets)
python3 -B scripts/test-core-boundaries.py
python3 -B scripts/test-core-rpc-docker.py
```

不得复用已有字段编号，不能悄悄改变类型、枚举或 RPC 名称。增加字段不能让旧调用者无故失效；破坏性改动应使用新主版本。契约测试覆盖已有字段、大整数、未知字段和损坏消息。

`core-protocol.yml` 在重构分支代码更新时检查生成一致性、编译、并发测试、真实数据库和 Docker 通信，也保留手动入口。Docker 使用随机临时项目验证凭证隔离、团队查询、扩展重建、核心重启重连、数据库故障恢复和即时撤销。

后续事件必须与核心变更一起持久化，再独立交付，并处理重复、确认、重试和积压上限。本批尚未实现可靠事件交付或资金写接口。核心模型路径不应新增同步 Go 回调；模型 API 和业务就绪检查继续返回 503。
