# 全新微内核 Docker 开发栈（WIP）

本栈只用于全新、隔离的本地安装，不升级或清空旧站点数据库，不部署生产。产品只有 `lmm`、`lmm-core`、`lmm-extensions` 三个可执行程序；`core-init` 调用同一个 Rust 核心的离线子命令，不是第四个产品。

Compose 已拆开安装 owner 与运行时凭据，并提供[显式离线授权步骤](../postgres/README.md)。**本轮实际 Docker 集成验收尚待执行，不能把配置或文档检查当成容器部署通过。** 旧的单一 owner URL 初始化和运行方案已移除；运行时仍拒绝 owner 或权限不足的账户。

真实快照的身份 ETL 曾在隔离克隆库与独立本地核心目标库之间验证，完成时覆盖 888 位旧用户；资金、API Key 和 session 未迁移，生产未写入。这是当时的身份迁移基线，不是当前用户总数或完整业务迁移。

## GitHub Container Registry 预览镜像

[手动发布入口](../../.github/workflows/microservice-images.yml)接受本仓库 WIP 分支的实际提交，
或精确的 `microservice-preview-<完整提交>` 标签；标签提交必须已在 WIP 历史中。
并行开发时使用已校验提交的预览标签，避免发布过程中分支继续前进导致来源变化。
Core 和 Go 各自构建现有运行镜像，首次支持 `linux/amd64`；两者均通过真实的非 root、无网络启动及许可证冒烟后，才发布同一组已测试字节：

- `ghcr.io/tokennotincluded/lmm-core:sha-<完整提交>`
- `ghcr.io/tokennotincluded/lmm-extensions:sha-<完整提交>`

另有对应的 `preview-<至少12位提交>` 标签；工作流拒绝覆盖已有标签，不写 `latest`。
以运行记录的 `publication.json` 中读回的摘要为准，拉取时优先使用 `IMAGE@sha256:...`。
两镜像的推送不是跨包原子事务；出现部分失败时先读回实际结果，不盲目重跑。
包首次创建可能默认私有；发布成功不代表匿名可拉取，访问权限需另外核对。
记录是未签名的源码/构建摘要，不宣称 SLSA 签名认证。

此入口不打包 Web 或客户端，不自动初始化数据库、启动 Compose、替换旧服务或部署生产。
Web 仍是独立静态文件，客户端仍由固定子模块构建。容器启动冒烟不是身份、订单、资金及供应商的完整验收。
当前 Compose 默认使用本地 `lmm-core` / `lmm-extensions` 镜像名；若使用已发布包，可先明确拉取选定摘要，
再为该摘要设置本次项目的本地标签，并通过 `LMM_DOCKER_CORE_IMAGE_TAG` / `LMM_DOCKER_EXTENSIONS_IMAGE_TAG` 选择。
保持原有只读 TOML、私有文件、owner/runtime 分离和 RPC 覆盖，不能把镜像发布当成已有站点的升级授权。

## 凭据与网络

本地 PostgreSQL 固定为已缓存的 18.6 镜像摘要。18 的数据卷挂载 `/var/lib/postgresql`，不要复用 17 的旧数据卷。数据库仅加入内部 `identity` 网络，不映射宿主端口；Go 不获得核心数据库网络、凭据或数据卷。核心网络随实际 Compose 项目命名为 `<项目名>-runtime`；扩展必须显式选择 `LMM_DOCKER_NETWORK`，RPC 两侧也必须明确选择同一个本次自有 `LMM_DOCKER_RPC_VOLUME`，不会默认连接一个固定共享卷。

| Compose 挂载路径变量 | 使用者 | 内容 |
| --- | --- | --- |
| `LMM_DOCKER_DATABASE_PASSWORD_SECRET_FILE` | PostgreSQL 容器 | `lmm_core_admin` 管理口令 |
| `LMM_DOCKER_DATABASE_OWNER_URL_SECRET_FILE` | 仅离线 `core-init` | 管理员认证并切换到 NOLOGIN `lmm_core_owner` 的安装 URL |
| `LMM_DOCKER_DATABASE_URL_SECRET_FILE` | 仅运行服务 `core` | `lmm_core_runtime` 的受限 URL |
| `LMM_DOCKER_RPC_TOKEN_SECRET_FILE` | 配置 RPC 的核心和扩展 | 独立 RPC 服务口令 |
| `LMM_DOCKER_EXTENSIONS_TOKEN_SECRET_FILE` | 仅扩展 | 独立扩展宿主口令 |

安装 URL 使用 `options=-c%20role%3Dlmm_core_owner`。runtime 不得拥有管理员或 owner 的成员资格，也不能读取安装 URL。核心镜像使用 UID/GID `65532:65532`，绑定挂载的核心 URL/RPC 文件须由该 UID/GID 持有且为 `0400`，宿主父目录保持 `0700`。文件从创建起保持私有；Compose 的 `uid`/`mode` 不能代替实际文件权限，不要用 `0444` 规避读取错误，也不挂载整个凭据目录。

## 只读 TOML 配置

镜像程序与 Linux 包统一安装到 `/usr/bin/lmm-core`、`/usr/bin/lmm-extensions`。运行进程显式使用 `--config /etc/lmm.conf.d/core.toml` 或 `--config /etc/lmm.conf.d/extensions.toml`。Compose `configs` 只读挂载非秘密 TOML；配置内仅引用单独挂载的秘密文件，不填写密码或 URL。文件模式拒绝旧业务环境变量，本栈不使用 `--legacy-env`。上表的 `LMM_DOCKER_*` 仅供 Compose 选择宿主文件，不传给核心或扩展。

覆盖文件选择 [config](config/) 中的完整版本 1 模板：基础核心没有数据库；identity 加受限数据库；RPC 版本再加 socket 与口令；standalone 保持低资源限制。`core-init.toml` 仅引用 owner URL，离线工具也显式选择此文件。RPC 核心覆盖必须同时使用 identity 或 standalone 数据库覆盖。

需要其他功能时，复制并审查完整 TOML，通过绝对路径 `LMM_DOCKER_CORE_CONFIG_FILE`、`LMM_DOCKER_INIT_CONFIG_FILE` 或 `LMM_DOCKER_EXTENSIONS_CONFIG_FILE` 选择；Compose 不拼接 TOML 段落。文件内路径必须是容器中的绝对路径，相关配置和秘密须另行只读挂载。修改普通 TOML 不会执行初始化或授权。

可选 legal 覆盖要求显式提供完整的 `LMM_DOCKER_EXTENSIONS_CONFIG_FILE`，其中 `[legal] config_file = "/etc/lmm.conf.d/legal.toml"`，以及 `LMM_DOCKER_LEGAL_CONFIG_FILE` 指向运营方法律文档 TOML。`config/extensions-legal.toml` 是未启用 RPC 的独立示例；同时使用 RPC 时须在完整配置中保留其模块和 `[core]` 设置，不能把添加法律文档当成关闭现有模块。覆盖文件只挂载法律配置本身；自定义模板目录需另加对应只读挂载，或使用镜像内置模板。

## 显式安装顺序

先为一个全新随机命名的本地项目准备上述绝对路径凭据文件，默认端口为核心 `18080`、扩展 `18081`；不得使用已有服务、卷或生产凭据。将 `LMM_LOCAL_CORE_PROJECT` 设置为本次随机项目名，再选择其网络和全新 RPC 卷。以下命令从仓库根目录执行：

```sh
export LMM_DOCKER_NETWORK="${LMM_LOCAL_CORE_PROJECT:?Set a new random project}-runtime"
export LMM_DOCKER_RPC_VOLUME="${LMM_LOCAL_CORE_PROJECT}-rpc"
dc() {
  mise exec -- docker --host unix:///var/run/docker.sock compose \
    -p "$LMM_LOCAL_CORE_PROJECT" \
    -f deployment/docker/compose.core.yml \
    -f deployment/docker/compose.identity.yml \
    -f deployment/docker/compose.rpc-core.yml "$@"
}
dc build core
dc up -d --wait core-db
# 显式创建角色；已有同名角色拒绝。
dc exec -T core-db psql -X -U lmm_core_admin -d lmm_core \
  -v ON_ERROR_STOP=1 < deployment/postgres/create-core-roles.sql
```

此时 runtime 尚无密码。使用交互式 psql 的 `\password lmm_core_runtime` 设置与 runtime URL 一致的口令，见[离线角色说明](../postgres/README.md)。不要把密码放进命令行或日志。随后才安装、显式授权、启动：

```sh
dc --profile tools run --rm -T core-init
# 启用 RPC 需要 events；其他模块仍默认不授权。
dc exec -T core-db psql -X -U lmm_core_admin -d lmm_core \
  -v ON_ERROR_STOP=1 -v enable_events=true \
  < deployment/postgres/grant-core-runtime.sql
dc up -d --no-deps core
curl --fail http://127.0.0.1:${LMM_DOCKER_CORE_PORT:-18080}/health/live
curl --fail http://127.0.0.1:${LMM_DOCKER_CORE_PORT:-18080}/health/ready
curl -i http://127.0.0.1:${LMM_DOCKER_CORE_PORT:-18080}/health/model-ready
```

`init-db` 一次事务安装当前完整契约的 11 个组件：identity、ledger、payments、charging、routing、events、charging-ledger、migration、console、model-gateway、commerce。已有应用对象或重复安装拒绝，不删表、不清库。普通启动校验运行账户权限、完整组件契约及实时 catalog 指纹，不创建角色、不 GRANT、不安装或升级 schema。安装组件不等于其 HTTP 业务已启用。

配置完整身份和数据库后，`/health/ready` 可以返回 200，即使 `models` 为 `false`；模型默认关闭时 `/health/model-ready` 返回 503。这不证明供应商健康、支付已启用或功能对齐 main。

## 独立核心与扩展

独立核心使用基础 Compose 加 `compose.core-standalone.yml`，不加 RPC 覆盖文件。默认服务只有 `core`；离线 `core-init` 仅在 `tools` profile 中可用。它不启动 Go 或本机数据库，独立数据库的角色创建及授权仍需显式离线执行。[低内存说明](../../docs/core-standalone.md)中的旧单 URL 命令不能替代这里的凭据分离。

扩展使用独立项目及 `compose.extensions.yml`、`compose.rpc-extensions.yml`。RPC 使用 socket 卷，Go 的 socket 挂载只读；两个项目通过相同的 `LMM_DOCKER_RPC_VOLUME` 选择本次自有卷。更新 Go 时保持完整覆盖文件，只重建扩展，不重建核心或数据库。接口见[身份指南](../../docs/core-identity.md)和[通信指南](../../docs/core-protocol.md)。

`--build` 只适用于开发机，运行资源限制不限制构建过程。核心 Compose 停止宽限为 30 分钟，扩展 35 秒，不保证单节点更新不断流；双核心切换、长流、排空、恢复和 1C1G 工作负载需另行验收。

## 验证与清理

```sh
mise exec -- python3 -B scripts/test-core-boundaries.py
mise exec -- python3 -B -m unittest discover -s scripts -p test_core_rpc_fixture.py
# 仅在最终源码冻结后运行，避免重复冷构建：
mise exec -- python3 -B scripts/test-core-rpc-docker.py
```

离线 fixture 验证私有文件创建、精确单文件挂载参数、原 inode 轮换及可捕获信号退出后恢复 `0400`。Compose 客户端检查另验证八种覆盖组合中的 TOML、命令和凭据隔离；这些检查不证明真实容器挂载或二进制能启动。Docker 脚本只连接本机 Docker socket，创建随机项目、合成凭据、隔离端口及自有卷，验证安装、owner/缺权限拒绝、受限身份 readiness、RPC 和恢复，并仅清理本次创建的资源。本指南尚不声称新增容器断言已经通过。

手工栈只停止对应随机项目，保留数据；不要顺手增加 `--volumes`，不清理其他人的项目或卷。可选协议默认关闭，按[模板指南](../../docs/legal/README.md)配置 `/etc/lmm.conf.d/legal.toml`、明确的绝对 `template_root` 与 `compose.legal.yml`，运营方 TOML 不打包。只读模板位于 `/usr/share/lmm/legal/`。进程运行设置使用 TOML，Compose 的路径插值变量只决定挂载；完整路径约定见[配置与文件路径](../../docs/filesystem-layout.md)。`--target binaries` 只导出程序，不是运行镜像；预览归档见[独立分发](../../docs/release-architecture.md)。本栈不接生产反向代理、自动发布或生产部署。
