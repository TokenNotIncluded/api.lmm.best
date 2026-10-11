# 开发与测试

这是全新安装分支。不要连接现有站点数据库。服务分工见[架构与状态](core-migration.md)，创建数据库和密钥只使用 [Docker 指南](../deployment/docker/README.md)。

全新核心库通过 `lmm-core init-db` 显式安装一次；已有应用对象时拒绝。

## 工具与目录

本分支使用根目录 `mise.toml` 固定 Rust、Go、Bun、Node.js 和 just。Rust 版本与 `apps/lmm-core/rust-toolchain.toml` 保持一致，包含 rustfmt 与 clippy。CI 使用 Go 1.27.2、Bun 1.3.14。CLI 使用 `apps/lmm/Cargo.toml`，不与核心混为一个程序。

客户端由独立仓库 [TokenNotIncluded/lmm](https://github.com/TokenNotIncluded/lmm) 维护，通过 `apps/lmm` 子模块固定版本。首次检出先初始化客户端，再安装工具：

```sh
git submodule update --init --depth 1 -- apps/lmm
mise install
mise exec -- just setup
mise exec -- just --list
```

后续命令通过 `mise exec -- <命令>` 执行；just 的 recipe 也自动使用 mise。
例如 `mise exec -- bun run --filter @lmm/web typecheck`。先用
`mise exec -- rustc --version`、`mise exec -- go version` 和
`mise exec -- bun --version` 核对实际版本。mise 不读取测试数据库私有文件；
独立测试 DSN 仍由操作者注入对应子进程，不能放进 `mise.toml`。

CLI 可以单独克隆并在自己的根目录运行 `mise exec -- cargo test --locked`。修改 CLI 时先在子仓库验证、提交并推送，再在本仓库更新 `apps/lmm` 的提交指针；不要只提交子模块中的未推送版本。本地质量检查会拒绝未初始化、版本不符或有未提交源码的 CLI 子模块。

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
(cd apps/lmm-extensions && go test -mod=readonly -race ./...)
just test-core
```

核心数据库测试需要独立 PostgreSQL；Go 模块各自的 `pgtest` 目录有独立说明。完整核心检查由 `core-protocol.yml` 执行，包含真实数据库和进程测试。前端仍有未接入新后端的契约，不得将构建成功写成完整业务通过。

OpenUI 提示由前端组件定义生成。已移除写入旧 Go controller 的生成脚本，因为新宿主没有该文件的消费者；保留组件、输出边界与转义测试。未来接入真实助手时，应通过明确的组件契约传递提示，而不是恢复已删除的后端路径。

[协议模板](legal/README.md) · [构建预览包](release-architecture.md) · [贡献要求](../CONTRIBUTING.md)
