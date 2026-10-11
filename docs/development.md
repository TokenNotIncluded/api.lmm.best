# 开发与测试

这是全新安装分支。不要连接现有站点数据库。服务分工见[架构与状态](core-migration.md)，隔离数据库与密钥的要求见 [Docker 指南](../deployment/docker/README.md)。当前 Docker 的单一数据库账号流程仍待拆分，指南中的历史示例不可直接执行；本机原生验收使用独立安装账号与受限运行账号。

全新核心库通过 `lmm-core init-db` 显式安装一次；已有应用对象时拒绝。

安装使用独立的 owner 账号，网络服务使用受限的 runtime 账号；不要用 owner 启动服务。`init-db` 原子安装身份、账本、支付、计费、路由、事件、计费账本适配和迁移记录共 8 个组件，普通启动只校验结构和账号权限。

本地控制台身份接入使用 `LMM_CORE_DATABASE_URL_FILE`、`LMM_CORE_WEB_ORIGIN` 和 `LMM_CORE_WEB_REFRESH_KEY_FILE`。私有文件须为普通文件、权限 `0600`；本地 HTTP 须显式设置 `LMM_CORE_WEB_LOCAL_HTTP=true`。同时明确配置 `LMM_CORE_WEB_USER_AGREEMENT_ENABLED` 和 `LMM_CORE_WEB_PRIVACY_POLICY_ENABLED` 为 `true` 或 `false`，与实际公布的协议一致。注册开关为 `LMM_CORE_REGISTRATION_ENABLED`。模型和支付默认关闭；核心就绪不代表这些功能已就绪。

## 工具与目录

本分支使用根目录 `mise.toml` 固定 Rust、Go、Bun、Node.js、just 和 pgroll。Rust 版本与 `services/core/rust-toolchain.toml` 保持一致，包含 rustfmt 与 clippy。CI 使用 Go 1.27.2、Bun 1.3.14。CLI 使用 `apps/cli/Cargo.toml`，不与核心混为一个程序。

客户端由独立仓库 [TokenNotIncluded/lmm](https://github.com/TokenNotIncluded/lmm) 维护，通过 `apps/cli` 子模块固定版本。首次检出先初始化客户端，再安装工具：

```sh
git submodule update --init --depth 1 -- apps/cli
mise install
mise exec -- just setup
mise exec -- just --list
```

后续命令通过 `mise exec -- <命令>` 执行；just 的 recipe 也自动使用 mise。
例如 `mise exec -- bun run --filter @lmm/web typecheck`。先用
`mise exec -- rustc --version`、`mise exec -- go version` 和
`mise exec -- bun --version` 核对实际版本。mise 不读取测试数据库私有文件；
独立测试 DSN 仍由操作者注入对应子进程，不能放进 `mise.toml`。

CLI 可以单独克隆并在自己的根目录运行 `mise exec -- cargo test --locked`。修改 CLI 时先在子仓库验证、提交并推送，再在本仓库更新 `apps/cli` 的提交指针；不要只提交子模块中的未推送版本。本地质量检查会拒绝未初始化、版本不符或有未提交源码的 CLI 子模块。

## pgroll 迁移实验

[pgroll 0.16.3](https://github.com/xataio/pgroll/releases/tag/v0.16.3) 是外部 PostgreSQL 迁移工具，使用 [Apache-2.0](https://github.com/xataio/pgroll/blob/v0.16.3/LICENSE) 许可。mise 使用官方预编译程序；它不加入 LMM 分发包、运行镜像或服务启动流程，项目仍只有三个可执行产品。

下面的测试仅在本机 `/var/run/docker.sock` 创建随机命名的空 PostgreSQL 18.6 容器，不接受外部 DSN，也不读取已有开发库凭证。先显式准备固定摘要的镜像，测试本身不会下载镜像：

```sh
mise install aqua:xataio/pgroll@0.16.3
mise exec -- just pgroll-version
mise exec -- docker --host unix:///var/run/docker.sock pull postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
mise exec -- just test-pgroll
```

[两个迁移文件](../tests/pgroll/001_create_items.json)演示建表和[列重命名](../tests/pgroll/002_rename_label.json)。[真实数据库测试](../tests/pgroll/test_pgroll.py)验证 `init`、`validate`、`start`、`rollback`、`complete`，以及旧、新版本视图共存期间的读写和数据保留。连接只绑定本地随机端口；临时数据放在内存文件系统，结束时核对所有权并只删除自己的容器。

这不是核心数据库迁移方案。当前限定 schema 的 SQL、安全函数及安装校验仍需[专项适配](../services/core/schema/README.md#pgroll-evaluation-boundary)；不要对现有核心库运行 `init`、`baseline`、`convert` 或迁移命令。`validate` 需要真实数据库，`status` 等命令连接旧 pgroll 元数据时也可能升级内部结构，不能当作离线或保证只读的检查。不得将 pgroll 自动加入核心启动或空库初始化。

## 启动扩展与前端

Go 只读取已导出的环境变量，不自动加载 `.env`。`.env.example` 是字段说明，不含可直接使用的密钥。`LMM_EXTENSION_TOKEN_FILE` 指向新建的私有服务凭证；`LMM_EXTENSION_MODULES=none` 关闭所有受保护业务模块。启用 `identity` 还必须提供 RPC socket 和 RPC 凭证。

```sh
# 先按 Docker 指南创建隔离凭证。不要复用生产环境。
just dev-go
bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

前端启动不等于后端业务已接通。调试公开协议页面时，将前端的 `VITE_REACT_APP_SERVER_URL` 指向本地扩展服务；只有[协议文档](legal/README.md)列出的公开 API 不需要核心。

Go 拒绝旧单体及核心数据库变量，包括 `SQL_DSN`、`DATABASE_URL` 和 `LMM_CORE_DATABASE_URL`。数据库测试环境变量不要传给实际扩展进程。

## 验证

```sh
python3 -B scripts/check-docs-brand.py
python3 -B scripts/test-distribution.py
python3 -B scripts/test-core-boundaries.py
node --test scripts/workflow-topology.test.mjs
(cd services/extensions && go test -mod=readonly -race ./...)
just test-core
```

核心数据库测试需要独立 PostgreSQL；Go 模块各自的 `pgtest` 目录有独立说明。完整核心检查由 `core-protocol.yml` 执行，包含真实数据库和进程测试。前端仍有未接入新后端的契约，不得将构建成功写成完整业务通过。

OpenUI 提示由前端组件定义生成。已移除写入旧 Go controller 的生成脚本，因为新宿主没有该文件的消费者；保留组件、输出边界与转义测试。未来接入真实助手时，应通过明确的组件契约传递提示，而不是恢复已删除的后端路径。

[协议模板](legal/README.md) · [构建预览包](release-architecture.md) · [贡献要求](contributing.md)
