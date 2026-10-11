# 打包与分发（预览）

本分支只有 `lmm`、`lmm-core`、`lmm-extensions` 三个可执行程序。核心、扩展、客户端和静态 Web 分别形成四种独立分发产物；Web 包不包含第四个程序。分发包定义集中在 `packaging/distribution.json`，构建器是 `scripts/build-distribution.py`。组件 tar.gz 不含安装后执行脚本；构建和校验不修改数据库，也不启动或重启服务。

| 组件 | 内容 | 当前支持的包格式 |
| --- | --- | --- |
| `core` | `lmm-core`，含 `init-db`、`bootstrap-user` 子命令 | Linux amd64 / arm64 的 tar.gz |
| `extensions` | `lmm-extensions`、外部协议草稿 | Linux amd64 / arm64 的 tar.gz |
| `cli` | 用户客户端 `lmm` | Linux / macOS / Windows 的 amd64 / arm64 tar.gz |
| `web` | 独立静态 `dist/`，无可执行程序 | 与 CPU 无关的 tar.gz |

支持包格式不等于每个平台已经构建验证。分发工作流在 Linux amd64 上构建四种产物；[原生 Linux 打包](../packaging/linux/README.md)为三个程序生成 DEB、RPM、APK 和 Arch 包。CLI 的多系统检查另见 `lmm.yml`。核心管理操作使用同一个 `lmm-core`；旧单体程序、自动数据库升级和旧站点安装钩子不进入新包。

## 构建

从干净的源码提交构建。`--revision` 必须记录实际源码完整 SHA，不能填某次旧测试的提交。核心及扩展可直接复用 Docker 编译阶段，保持与镜像的运行环境一致：

```sh
docker buildx build -f deployment/docker/core.Dockerfile   --target binaries --output type=local,dest=out/core .
docker buildx build -f deployment/docker/extensions.Dockerfile   --target binaries --output type=local,dest=out/extensions .
# 分别为 core / extensions 打包；输入目录必须包含该组件的真实程序。
python3 scripts/build-distribution.py build --component core   --version 0.1.0-preview --revision "$(git rev-parse HEAD)"   --platform linux-amd64 --input out/core
```

上面的核心与扩展归档输入来自 Docker 的 Debian bookworm 构建环境；原生 Linux 工作流另行构建 musl 目标。本地 CLI 的运行库要求由其构建系统决定，不要假设所有 Rust 产物都采用相同的链接方式。

CLI 使用 `cargo build --manifest-path apps/cli/Cargo.toml --locked --release`，输入目录为 `apps/cli/target/release`。Windows 程序名为 `lmm.exe`。前端使用 `bun run --filter @lmm/web build`，输入目录为 `apps/web/dist`，平台为 `any`。构建器检查二进制格式与架构，不会把脚本或错误架构包装成成功产物。

## 校验与复现

每个包带 `MANIFEST.json`，记录组件、版本、源码 SHA、平台、预览状态，以及每个文件的大小、权限和 SHA-256。包外另有 `.sha256`。默认固定时间戳；也可用 `SOURCE_DATE_EPOCH` 指定。相同输入、身份与时间戳生成相同字节。

```sh
(cd out/distributions && sha256sum --check ./*.sha256)
python3 scripts/build-distribution.py verify   out/distributions/lmm-core-0.1.0-preview-linux-amd64.tar.gz   --revision "$(git rev-parse HEAD)"
```

校验器检查内容清单，不解包、不执行程序。拒绝路径越界、链接、重复条目、额外文件及校验不符。构建拒绝覆盖已有产物。私有配置、密钥和构建缓存不得进入分发包；服务配置只收集 `config/core.example.toml`、`config/extensions.example.toml`，协议资源只按清单收集 `site.example.toml` 及指定模板，不收集运营方的 `legal.toml` 或旧 `site.json`。

原生包将服务示例安装为 `/etc/lmm.conf.d/{core,extensions}.toml`，升级使用 `config|noreplace`；Web 和协议只读资源分别进入 `/usr/share/lmm/web/`、`/usr/share/lmm/legal/`。私密文件、持久状态、临时 IPC、日志及客户端个人路径统一见[配置与文件路径](filesystem-layout.md)。旧预览包不自动移动数据或转换配置；新配置仍须通过真实程序解析和部署验收。

SHA-256 不是发布者签名。只有与可信构建记录核对后，才能确认来源；不要把校验成功当成正式发行或生产验收。

## CI 与旧分发链路

`microservice-distribution.yml` 手动运行或由只读验证工作流调用，构建、检查并上传预览包。没有 Release、注册表推送、AUR 发布或部署权限。`core-protocol.yml` 保留业务模块检查，并校验文档和分发脚本。

旧站点的 Web 发布、部署工作流、AUR recipe、服务资源与部署工具已从本分支退役。当前维护的打包入口只有组件归档构建器和原生 Linux 包构建器；旧操作文档保留为[固定版本的历史入口](archive.md)。已发布的历史包及其签名不因此改变。正式发布、版本命名与部署仍需另行审查和授权。
