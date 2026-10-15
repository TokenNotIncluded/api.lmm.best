# 开发与测试

[工作指南](agent-workflows.md) · [架构](core-migration.md) · [配置路径](filesystem-layout.md)

本分支面向全新隔离安装。核心、扩展、客户端和前端分开开发；不要把一个组件的
准备步骤变成所有任务的前置条件。已有站点不使用这里的初始化命令。

## 只准备所需工具

版本以根目录 [mise.toml](../mise.toml) 为准，不在各指南重复固定版本号。已有工具
和依赖符合锁定版本时无需重装。后续工具命令使用 `mise exec --`；just 的 recipe
也通过 mise 执行。不需要 recipe 时可以直接使用已准备好的固定版本 Cargo/Go。

| 任务 | 准备内容 |
| --- | --- |
| 文档或技能正文 | Python、Git；不需要运行应用 |
| Rust 核心 | 固定 Rust、rustfmt/clippy；只有数据库测试需要隔离 PostgreSQL |
| Go 扩展 | 固定 Go；按模块准备其测试库，不连接核心数据库 |
| Web | 固定 Bun/Node.js 和锁定依赖；首次需要时执行 `mise exec -- just setup` |
| CLI | 初始化 `apps/cli` 子模块，并使用其固定工具链 |
| Docker 或原生打包 | 对应[安装指南](../deployment/docker/README.md)或[打包指南](../packaging/linux/README.md)列出的工具 |
| pgroll 实验 | 只在该实验任务中准备 pgroll 和隔离 Docker |

`mise install` 是整个工具集的准备方式，不是每轮必跑命令。`just setup` 安装 Web
工作区依赖，不是 Rust/Go 开发的必要前置步骤。已知命令无需每次再执行 `just --list`
或检查所有语言版本。

客户端由 [TokenNotIncluded/lmm](https://github.com/TokenNotIncluded/lmm) 独立维护。
只有涉及 CLI 或质量工具要求完整子模块状态时才初始化：

```sh
git submodule update --init --depth 1 -- apps/cli
```

修改 CLI 时先在子仓库验证、提交并推送，再更新本仓库指针；不要引用未推送提交。
质量工具会拒绝未初始化、版本不符或有未提交源码的 CLI 子模块。普通文档检查不
依赖它。测试 DSN 仅注入所需子进程，不写入 mise 配置，不传给实际扩展服务。

## 核心与数据库

核心位于 `services/core`，不依赖 Go 才能运行。完整 schema 使用同一个
`lmm-core init-db` 显式安装一次；已有应用对象时拒绝。普通启动只校验结构和权限，
不创建角色、不安装或升级数据库。完整契约见 [schema](../services/core/schema/README.md)。当前 identity 契约为 v4；
已有库不因更新源码或安装程序而升级，也不能修改契约记录绕过检查。

安装 owner 和受限 runtime 必须分开。原生或 Docker 安装只读所选路径，按
[离线角色与授权](../deployment/postgres/README.md)准备；不把 owner URL 放进运行服务。
不要因为重复初始化被拒绝就清库、删除卷或使用旧环境变量模式。
历史期初余额导入仍需独立审核，进展见 [A03 / PR #884](https://github.com/TokenNotIncluded/lmm-api/pull/884)；它不属于本页的全新安装步骤，不能作为现有站点的通用升级或自动资金迁移。

程序使用 TOML，可显式选择 `lmm-core --config /absolute/private/core.toml`。
`database.url_file` 指向受限数据库凭据；`identity.web.origin` 和
`identity.web.refresh_key_file` 配置控制台身份。私密文件权限为 `0600` 或可读的
`0400`，归属必须符合实际服务账户。

只读检查已安装数据库和所选运行配置时，使用：

```sh
lmm-core --config /absolute/private/core.toml check-db
```

命令输出 JSON，检查已安装结构和该配置所需的运行权限；不初始化或升级数据库，
不启动 HTTP、RPC、worker 或模型流量。退出码 `0` 仅表示这次有限检查通过，
不证明备份完整、物理数据库身份或恢复就绪。

[`scripts/core-ops.py`](../scripts/core-ops.py) 提供配套的 `plan`、`status` 和
`reconcile`。先用 `python3 -B scripts/core-ops.py --help` 查看入口；它要求显式的
安装记录、文件摘要和私有输入，`plan` 仅在 Backups 中独占创建新计划文件。
`status` 与 `reconcile` 只读检查，UNKNOWN 不重放；物理身份尚未验证时会明确返回
`PHYSICAL_IDENTITY_UNVERIFIED` 和退出码 `3`。它不授予维护、接管或恢复权限。

本地 HTTP 须显式设 `identity.web.local_http=true`；`user_agreement_enabled` 和
`privacy_policy_enabled` 与实际公开协议一致。注册使用 `identity.registration_enabled`。
TOTP 二次验证需另行显式配置 [MFA keyring](core-identity.md#可选-totp-二次验证mfa)，
不配置时不会绕过已有账号的 MFA 要求。模型和支付默认关闭；就绪不代表这些功能已启用。原生路径见
[配置与文件路径](filesystem-layout.md)，容器路径见 [Docker 栈](../deployment/docker/README.md)。

## 启动扩展与前端

扩展入口是 `services/extensions/cmd/extensions`。先准备独立的完整 TOML 和私有
凭据，再从仓库根目录分别运行：

```sh
(cd services/extensions && mise exec -- go run ./cmd/extensions --config /absolute/private/extensions.toml)
mise exec -- bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

参考 [extensions.example.toml](../config/extensions.example.toml)。Go 不加载 `.env`；
文件模式拒绝旧运行变量。`host.token_file` 是独立宿主凭据，`host.modules=["none"]`
关闭受保护模块。启用 identity 还需 RPC socket、独立口令和跨用户文件权限。

Go 拒绝 `SQL_DSN`、`DATABASE_URL`、`LMM_CORE_DATABASE_URL` 等旧单体/核心库变量。
两服务的 `--legacy-env` 只用于既有测试兼容，与 `--config` 互斥，不能作为新部署模板。

调试公开协议页面时，将 `VITE_REACT_APP_SERVER_URL` 指向本地扩展；只有
[协议指南](legal/README.md)列出的公开 API 不需要核心。前端启动或构建成功不表示
业务已接通。OpenUI 不再向删除的 Go controller 生成文件；新接入使用明确组件契约。

## 验证与分发

按[检查选择表](agent-workflows.md)执行受影响检查。鉴权、资金、持久化和通信变化
需要对应真实进程或隔离数据库；Go 的嵌套 `pgtest` 模块需单独测试。不要把示例替身
通过写成真实资金链路通过。

文档正文使用 `python3 -B scripts/check-docs-brand.py --docs-only`。广泛跨组件验收
才使用[质量工具](quality.md)，不顺序叠加它已包含的各层。发布状态见实际源码和
[Actions 说明](ci-workflow-layout.md)，不能从 PR 标题或旧运行推断当前验收。

构建归档见[独立分发](release-architecture.md)。核心-only 工作直接使用核心工具链，
不运行同时构建核心、Go、Web 的 `just build`。构建缓存不作例行清理。

## 按需：pgroll 实验

pgroll 是 mise 固定的外部工具，不进入运行镜像、分发包、核心启动或 `init-db`。
它不是现有核心库的升级方案。协议限制见
[schema 中的适配说明](../services/core/schema/README.md#pgroll-evaluation-boundary)。

[实验测试](../tests/pgroll/test_pgroll.py)只接受本地 `/var/run/docker.sock`，不接收
外部 DSN，不读取已有库凭据。它创建随机命名的空 PostgreSQL 容器，先核对所有权，
结束时只清理自有容器。需预先准备测试指定摘要的镜像；测试不会下载镜像。

```sh
mise install aqua:xataio/pgroll@0.16.3
mise exec -- just pgroll-version
mise exec -- docker --host unix:///var/run/docker.sock pull postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
mise exec -- just test-pgroll
```

实验验证 init、validate、start、rollback、complete 和两版本视图读写。`validate`
需要真实数据库；`status` 等命令也可能升级旧元数据，不能当成离线或保证只读检查。
不得对现有核心库运行 init、baseline、convert 或迁移命令。
