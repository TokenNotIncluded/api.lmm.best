# Rust 核心与 Go 扩展通信（WIP）

本实现使用 **Protocol Buffers（Protobuf）定义二进制消息，gRPC 负责请求与响应**。
不是共享内存协议，也不是消息队列。当前只提供只读控制接口，不传输模型 token。
核心模型入口和业务就绪检查仍返回 503；不能把本功能视为完整迁移或生产切换。

## 通信方向与权限

Go 模块 → 单个复用的 gRPC 客户端 → 私有 Unix socket → Rust → 独立只读数据库连接池。
Rust 不反向调用 Go。已有核心 HTTP 处理和身份校验不经过扩展。
Go 不持有核心数据库地址、密码或数据卷。它不能上传用户 ID、角色或付款方来获得授权。

契约位于 `contracts/proto/lmm/core/v1/control.proto`，服务为 `lmm.core.v1.CoreControl`：

| 方法 | 输入与作用 | 用户凭证 |
| --- | --- | --- |
| `Capabilities` | 协议主版本、已配置的身份功能 | 不需要；仍需服务凭证 |
| `Authorize` | 返回实际用户、凭证类型、归属账号和有序付款候选 | 必须；会话或 API Key |
| `ListTeams` | 按 `after_id` 分页，最多 100 个团队 | 必须是会话 |

`identity_available` 表示已配置身份存储，不是数据库实时健康或模型就绪证明。
付款候选只是当前权限快照，不是已预留的余额；将来计费必须在自己的事务内重新校验。
团队 Key 的付款方只可能是所属团队，平台 L5/L6 不自动获得团队资金权限。

每次调用有三类独立信息：

- `authorization: Bearer <core-service-token>`：调用内部接口的服务凭证。
- `x-lmm-protocol: 1`：协议主版本。
- `x-lmm-user-credential: <session-or-api-key>`：具体用户凭证，仅用于需要身份的接口。

服务凭证不等于用户授权。Rust 拒绝缺失、重复或无效的凭证以及不支持的主版本。
Go 客户端替换调用方传入的同名元数据，不允许调用方覆盖服务身份。
凭证不写进 Protobuf 消息、响应或日志。身份结果不缓存，撤销状态由 Rust 每次读取。

## 限额与失败处理

单条消息上限 64 KiB，HTTP/2 请求头上限 8 KiB。Rust 最多接入 8 条内部连接，
每连接最多 8 个请求流；实际业务查询还受全局 8 个并发名额限制。
Go 客户端最多保留 32 个正在调用的请求。超额时立即返回繁忙错误，不无限排队。
HTTP/2 的窗口和应用并发分别限制，不能把传输窗口当作业务队列。

Go 调用最多等待 2 秒，调用方更短的期限仍有效。Rust 也设置 2 秒上限。
取消和超时会释放调用名额。应用层不自动重试；不得在以后新增的写接口上盲目重放。
数据库连接池独立限制为 2 条，取连接最多等待 400 ms，SQL 语句最多执行 1500 ms，
并设置只读事务默认值。这个池不占用已有核心 HTTP 身份池的名额。
这并不意味着 CPU、数据库或宿主机故障已经完全隔离，生产仍需负载和故障演练。

内部错误使用明确的 gRPC 状态。Go 的 HTTP 模块将其映射为 401/403/400/409/429/503/504，
对外不返回数据库错误细节。Go 在 Rust 暂时离线时仍能启动，连接由客户端后续恢复。
`/health/live` 只证明进程存活；它不替代功能探测。

## 同机 Docker 开发部署

本版本仅支持同机 Unix socket。没有内部 TCP 监听，也没有明文跨主机退路。
跨主机部署需另行实现双向 TLS 和服务身份验证，不要通过转发工具公开本 socket。
核心进程是 socket 目录唯一写入方。目录为 0700，socket 为 0600，两个容器使用相同的专用 UID。
Go 只读挂载目录，能连接但不能创建、替换或删除文件。核心持有独占锁，重启时只清理旧 socket，
不会覆盖普通文件，也不会移除另一个持锁核心的监听文件。

先按 [身份指南](core-identity.md) 创建开发数据库凭证，按
[Docker 基础指南](../deployment/docker/README.md) 创建 `extensions-token`。
再在仓库根目录创建一份**不同的**内部服务凭证；以下命令拒绝覆盖已有文件：

```sh
python3 - <<'PY'
from pathlib import Path
import os, secrets
p = Path('deployment/docker/.secrets/core-rpc-token')
p.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
with p.open('x') as f:
    f.write(secrets.token_hex(32) + '\n')
os.chmod(p, 0o444)
PY

dc() {
  docker compose -f deployment/docker/compose.core.yml \
    -f deployment/docker/compose.identity.yml \
    -f deployment/docker/compose.rpc-core.yml "$@"
}
de() {
  docker compose -f deployment/docker/compose.extensions.yml \
    -f deployment/docker/compose.rpc-extensions.yml "$@"
}
# 仅用于开发机。先构建核心，创建数据库，显式迁移，再开始服务。
dc build core
dc up -d --wait core-db
dc --profile tools run --rm -T core-admin migrate
dc up -d core
de up -d --build extensions
```

`dc` 必须始终使用相同文件组合，避免不小心去掉身份或通信配置。
不使用身份数据库时可省略 `compose.identity.yml`，这时仅 `Capabilities` 可用。
核心首次启动会创建 socket 卷；扩展将该卷视为外部资源，不能负责删除它。

只更新扩展：`de up -d --build --no-deps extensions`。生产应使用预先构建、固定摘要的镜像，
不要在核心所在的小内存主机上编译。不要为了更新扩展运行 `dc down`，不要使用 `down --volumes`。
`de down` 不删除核心或外部 socket 卷。服务凭证目前在进程启动时读取，没有热轮换。
更换凭证必须协调两端，不能假设只重启扩展就能更新核心保存的凭证。

两端可用 `LMM_CORE_RPC_SOCKET` 和 `LMM_CORE_RPC_TOKEN_FILE` 配置；必须同时提供。
Compose 文件中的 `LMM_CORE_RPC_TOKEN_SECRET_FILE` 指定宿主机凭证文件，
`LMM_CORE_RPC_VOLUME` 指定 socket 卷名。开发栈不使用生产数据。

## 接入 Go 模块

模块只通过 `internal/coreclient.Client` 调用核心。构建客户端不会同步等待核心在线。
启动时按配置注册 `identity` 模块，提供：

```text
GET /extensions/v1/identity/capabilities
GET /extensions/v1/identity/self
GET /extensions/v1/identity/teams?after_id=0
```

这是内部调试/模块接口，不是新的公共登录入口。宿主检查 `Authorization` 中的
`extensions-token`；后两个接口还检查 `X-LMM-User-Credential`，再由 Rust 验证该用户。
返回使用 Protobuf JSON 规则，64 位 ID 是字符串，防止浏览器把大整数取整。
不得把 Go 模块返回的角色或 `can_spend` 当作另一条扣款通道。

## 生成、兼容与验证

Rust 在构建时从同一份 `.proto` 生成代码，编译器随构建依赖固定，不依赖系统 `protoc`。
Go 生成文件提交入库，普通 Go 构建不需要编译器。更新契约后执行：

```sh
# 生成操作需已安装 protoc；Go 生成器版本由脚本固定。
bash scripts/generate-core-protocol.sh
# 检查生成文件与契约完全一致，包括缺失文件。
bash scripts/generate-core-protocol.sh --check
(cd apps/extensions-go && go mod verify && go vet ./... && go test -race ./... -count=1)
# Rust 数据库测试需 DATABASE_URL 指向可创建测试数据库的独立 PostgreSQL。
(cd apps/core-rust && cargo fmt --all --check && cargo clippy --locked --all-targets -- -D warnings && cargo test --locked --all-targets)
python3 -B scripts/test-core-rpc-docker.py
```

不要修改已有字段编号、类型、角色枚举编号或 RPC 名称。兼容性测试固定现有 v1 字段和服务定义，
允许增加字段；新增字段不能悄悄变成旧调用者的必填项。删除字段时保留其编号，破坏性修改使用新主版本。
Rust/Go 共用大整数编码样例，分别检查未知字段和损坏数据。
手动工作流 `core-protocol.yml` 检查生成一致性、编译、并发测试和实际 Docker 通信。

Docker 测试会创建随机命名的独立测试栈，验证真实身份/团队查询、两类服务凭证、只读挂载、
扩展被终止和重建、核心重启后的自动重连、数据库失效/恢复和 Key 撤销。
这些测试不证明真实模型输出不中断，不测试旧用户导入，也不进行真实扣费。

## 后续边界

不在模型输出链路加入同步 Go 回调。未来通知、邮件或统计事件应在核心事务内记录可恢复事件，
再由独立工作进程交付给 Go，并处理重复投递。Protobuf 只解决消息格式，本批没有实现持久消息队列、
确认、重投或恰好一次处理，也没有预先开放余额修改接口。
