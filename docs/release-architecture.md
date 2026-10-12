# 打包与分发（预览）

[部署技能](../.agents/skills/deployment/SKILL.md) · [原生 Linux 包](../packaging/linux/README.md) · [Docker 安装](../deployment/docker/README.md)

只有三个可执行程序：`lmm-core`、`lmm-extensions`、客户端 `lmm`。Web 是独立静态
产物。组件归档由 `packaging/distribution.json` 定义，构建器为
`scripts/build-distribution.py`。构建、校验和安装包文件不等于启动服务或部署生产。

## 先选目标

| 目标 | 执行路径 | 不需要做 |
| --- | --- | --- |
| 只改文档 | 文档检查和示例审查 | 构建、服务器预检、数据库、发版 |
| 校验已有组件归档 | 核对可信构建记录，用 `verify` 校验 | 新建版本、重编译或重新打包 |
| 新建组件归档 | 只构建所需组件，再使用 `build` | 构建其他组件、重复构建匹配的程序 |
| 同一程序转为不同 Linux 格式 | 复用已验证归档，执行原生包构建器 | 每种格式重编译一次 |
| 隔离安装或更新 | 选择 Docker 或原生安装的对应步骤 | 初始化已有库、重启未变化组件 |
| 正式发行或现有站点变更 | 单独审核目标、来源、授权和恢复办法 | 使用退役部署脚本作为捷径 |

固定一次组件、源码、架构、格式和输入来源。已有符合要求的构建记录和产物可复用，
但保留它们原来的源码 SHA。无关文档合并不要求重编译旧产物，也不能给旧程序换上
新 HEAD 的标签。来源、配置、工具链或打包输入变化时，重新核对相应证据。

## 组件与平台

| 组件 | 输入 | 归档平台 |
| --- | --- | --- |
| `core` | `lmm-core`，含离线管理子命令 | Linux amd64 / arm64 |
| `extensions` | `lmm-extensions`、协议模板与示例配置 | Linux amd64 / arm64 |
| `cli` | 子模块中的 `lmm`，Windows 名为 `lmm.exe` | Linux / macOS / Windows，amd64 / arm64 |
| `web` | `apps/web/dist`，不含服务程序 | `any` |

支持一种格式不等于该平台已通过实际验收。分发工作流在 Linux amd64 构建四种归档。
原生构建器处理 core、extensions、cli 和 web，提供 DEB/RPM/APK/Arch；CLI 多系统
检查另见 `lmm.yml`。记录真实平台和运行 ID，不从工作流存在推断矩阵通过。

## 只构建所需组件

在干净、固定的源码提交上构建，记录真实完整 SHA。以下为核心归档示例，未要求
同时构建扩展；输出目录不得包含上次遗留程序：

```sh
SOURCE_SHA="$(git rev-parse HEAD)"
docker buildx build -f deployment/docker/core.Dockerfile \
  --target binaries --output type=local,dest=out/core .
python3 scripts/build-distribution.py build --component core \
  --version 0.1.0-preview --revision "$SOURCE_SHA" \
  --platform linux-amd64 --input out/core
```

示例中的平台必须与实际编译结果一致，版本使用本次选择的预览版本；不要直接
重复运行并覆盖已有产物。构建器检查程序格式和 CPU 架构，不接受脚本或错误架构。

扩展需要时才选择 `deployment/docker/extensions.Dockerfile` 的 `binaries` 目标，
输入目录使用独立的 `out/extensions`。这两个 Docker 构建环境为 Debian bookworm；
导出的程序不是完整运行镜像。需要镜像时使用对应运行目标，不把归档当作镜像。

CLI 先初始化并核对子模块，再用 `mise exec -- cargo build --manifest-path apps/cli/Cargo.toml --locked --release`，输入为 `apps/cli/target/release`。Web 使用
`mise exec -- just build-web`，已包含 bundle 检查；不再单独重复该检查。
原生核心开发可直接用 Cargo，无需为了它安装 Bun 或构建前端。

Alpine/APK 只接受验证过的静态 ELF；不能把 glibc 程序改名后包装成兼容版本。
原生工作流另构建 musl 目标；不要假定所有 Rust 产物采用相同链接方式。

## 校验或复用已有归档

从可信构建记录取得原始源码 SHA 和包摘要，而不是只信任与下载包一起提供的文本。
下面只读取归档，不构建、不解包、不执行程序：

```sh
python3 scripts/build-distribution.py verify \
  out/distributions/lmm-core-0.1.0-preview-linux-amd64.tar.gz \
  --revision "${VERIFIED_SOURCE_SHA:?Use the source SHA from the trusted build record}"
```

每包带 `MANIFEST.json`：组件、版本、源码、平台、预览状态，以及文件大小、权限和
SHA-256；包外另有 `.sha256`。相同输入、身份和时间戳产生相同字节，时间戳可用
`SOURCE_DATE_EPOCH` 指定。构建拒绝覆盖，验证拒绝越界、链接、重复/额外条目及摘要不符。

服务配置只收集指定示例，法律资源只收集清单中的模板和 `site.example.toml`。
运营方配置、密码、密钥、缓存和旧 `site.json` 不得入包。SHA-256 不是发布者签名，
清单自洽也不是来源或运行功能的独立证明。下载失败先处理下载，不重编译成功的组件。

原生转换复用这个归档与可信摘要。`packaging/linux/build.py` 要求真实来源 SHA；
更换包格式或包装修订不意味着重新编译程序。具体命令及输出见原生指南，避免复制
四套步骤。包安装保留运营方 TOML，不自动创建数据库、初始化、授权或重启服务。
配置、秘密、状态和日志位置统一见[文件路径](filesystem-layout.md)。

## 安装与更新

构建在开发机完成，尤其不要在 1C1G 服务节点上运行冷构建。安装只选择需要的组件；
独立核心不需要 Go、CLI 或本地 Web 包。实际数据库及受限运行账户仍须正确配置。

Docker 更新保持已有完整覆盖文件、项目名、私有凭据和 RPC 绑定，只更新目标服务。
不要把更新扩展变成重建核心或数据库。普通启动只验证 schema 与权限；`init-db` 只在
确认空库的首次安装执行。具体顺序由当前 Docker/原生指南维护，不从历史命令推断。

包文件已替换不代表进程已加载；授权重启后检查实际程序和受影响业务。单节点重启
可能断流，现有包不证明无中断更新。健康 200 不等于模型或资金业务可用。

## 验证与发布状态

构建器代码变化运行 `python3 -B scripts/test-distribution.py`；原生构建器变化执行
其对应测试，涉及真实格式/安装时增加隔离安装检查。文档变化只执行
[文档检查](agent-workflows.md)，不为验证示例启动环境或发布。

`microservice-distribution.yml`、`linux-packages.yml`、`lmm.yml` 保留预览产物，
不是正式 Release 或生产部署。不要为普通文档修改触发它们。正式发布另需来源、
签名、目标平台与实际业务验收。旧单体发布、AUR、operator 与旧站点操作链路不再
作为当前入口；历史证据保留在[归档索引](archive.md)。

交付区分源码修改、程序构建、归档校验、安装、发布和部署。只报告实际完成的部分，
不将较短指南或更少命令说成已测得的部署提速。
