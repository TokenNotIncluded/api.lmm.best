# 打包与分发（预览）

核心、扩展、CLI 和前端是四个独立产物。分发包定义集中在 `packaging/distribution.json`，构建器是 `scripts/build-distribution.py`。没有安装后执行脚本，不修改数据库，也不自动重启服务。

| 组件 | 内容 | 当前支持的包格式 |
| --- | --- | --- |
| `core` | `lmm-core`、`lmm-core-admin` | Linux amd64 / arm64 的 tar.gz |
| `extensions` | `lmm-extensions`、外部协议草稿 | Linux amd64 / arm64 的 tar.gz |
| `cli` | `lmm` | Linux / macOS / Windows 的 amd64 / arm64 tar.gz |
| `web` | 独立 `dist/` | 与 CPU 无关的 tar.gz |

支持包格式不等于每个平台已经构建验证。新增分发工作流在 Linux amd64 上构建四个组件。CLI 的多系统检查另见 `lmm.yml`。不将旧 `lmm-api-go`、数据库升级命令或 `lmm-api-web.install` 混入新包。

## 构建

从干净的源码提交构建。`--revision` 必须记录实际源码完整 SHA，不能填某次旧测试的提交。核心及扩展可直接复用 Docker 编译阶段，保持与镜像的运行环境一致：

```sh
docker buildx build -f deployment/docker/core.Dockerfile   --target binaries --output type=local,dest=out/core .
docker buildx build -f deployment/docker/extensions.Dockerfile   --target binaries --output type=local,dest=out/extensions .
# 分别为 core / extensions 打包；输入目录必须包含该组件的真实程序。
python3 scripts/build-distribution.py build --component core   --version 0.1.0-preview --revision "$(git rev-parse HEAD)"   --platform linux-amd64 --input out/core
```

核心和扩展 Linux 包以 Docker 的 Debian bookworm 构建环境为准。本地 CLI 的运行库要求由其构建系统决定；不要假设 Rust 程序都是静态链接。

CLI 使用 `cargo build --manifest-path apps/lmm/Cargo.toml --locked --release`，输入目录为 `apps/lmm/target/release`。Windows 程序名为 `lmm.exe`。前端使用 `bun run --filter @lmm/web build`，输入目录为 `apps/web/dist`，平台为 `any`。构建器检查二进制格式与架构，不会把脚本或错误架构包装成成功产物。

## 校验与复现

每个包带 `MANIFEST.json`，记录组件、版本、源码 SHA、平台、预览状态，以及每个文件的大小、权限和 SHA-256。包外另有 `.sha256`。默认固定时间戳；也可用 `SOURCE_DATE_EPOCH` 指定。相同输入、身份与时间戳生成相同字节。

```sh
(cd out/distributions && sha256sum --check ./*.sha256)
python3 scripts/build-distribution.py verify   out/distributions/lmm-core-0.1.0-preview-linux-amd64.tar.gz   --revision "$(git rev-parse HEAD)"
```

校验器检查内容清单，不解包、不执行程序。拒绝路径越界、链接、重复条目、额外文件及校验不符。构建拒绝覆盖已有产物。私有配置、密钥和构建缓存不得进入分发包；协议资源只按清单收集示例及指定模板，绝不收集 `site.json`。

SHA-256 不是发布者签名。只有与可信构建记录核对后，才能确认来源；不要把校验成功当成正式发行或生产验收。

## CI 与旧分发链路

`microservice-distribution.yml` 手动运行或由只读验证工作流调用，构建、检查并上传预览包。没有 Release、注册表推送、AUR 发布或部署权限。`core-protocol.yml` 保留业务模块检查，并校验文档和分发脚本。

旧 `release-web.yml`、AUR recipe 和前端部署工具仍用于已有线上版本的签名包处理，不能当作本分支新服务的安装器；本轮不改动既有签名版本与公开包。旧操作文档已改为[历史入口](archive.md)。正式发布、版本命名与部署仍需另行审查和授权。
