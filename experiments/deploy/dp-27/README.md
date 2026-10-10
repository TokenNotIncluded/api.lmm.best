# DP-27：小产物与本地分发实验

关联 #675。目标分支 `wip/rust-core-go-extensions`；起点
`72667564c0431754d4856dc2e0db55f360bd2745`。所有修改只在本目录。
不改变正式 Dockerfile、部署入口、数据库、CLI 或工作流。

**本轮是部分验收，不是业务镜像优化通过。** 本机没有 Docker、Rust、Bun；
Go 是 1.23.2，低于扩展要求。Git 网络解析失败。通过 GitHub 只读连接检查了
指定版本的构建文件；未完整克隆、未构建业务后端。没有拉取正式镜像、推送、
创建远程 PR、触发 CI、部署或合并。交付本地提交和补丁。

## 已实现

`impact.py` 输出重建组件与验证任务。Go 普通变更不重建 Rust；前端普通变更
不重建后端；协议和已生成绑定变化会覆盖两端。删除和改名同时检查旧、新路径。
未知共享路径会阻止自动构建，不能悄悄忽略。验证另一组件不等于重建另一组件。

`docker/` 是只复制预编译产物的候选。core 与 core-admin 分开，共用固定摘要
的运行基础镜像。Go 可以测试静态基础镜像；Rust musl 必须先验证静态链接和
真实业务，再使用该候选，不能推定它更快。`build_images.py` 必须明确架构和
基础镜像摘要，默认只输出命令；即使选择执行，也仅导出本地 OCI 包。

`probe/` 是独立、预编译的本机健康探针候选。只允许 8080/8081；状态行和单个头部行最多
1024 字节，完整头部最多 8192 字节，不跟随重定向、不使用代理、DNS、TLS 或用户 CLI。
它用于现有 `/health/live` 检查，不证明模型、账本或就绪状态正确。
本地 Go 1.23.2 生成的包仅供实验，不得作为正式服务镜像发布。正式产物必须
用批准的工具链重建并完成安全与功能检查。
`probe/full/` 是用于同条件比较的标准 HTTP 客户端版本，不是业务服务。

`distribution.py` 是构建端的 Python 实验，不是服务机依赖。对清单作 Ed25519
签名校验，绑定组件、版本、源提交、平台、包摘要和最终文件摘要。
差分还校验旧文件摘要，并从私有副本还原。使用私有目录、互斥锁、有界解压和
不可覆盖的就绪文件发布。**没有切换运行版本的接口。** 交由 DP-29 在校验
兼容范围及真实就绪后使用，不能把暂存成功当成发布成功。采用暂存文件时必须再次核对
受信清单和实际内容，不得只相信 ready 文件名。

`oci_cost.py` 校验本地 OCI 内容摘要、长度及解压后的 DiffID，按唯一完整 blob
计算冷拉取、组件更新和预拉取后的下载预算；包含配置和 manifest 字节。
同时区分压缩层、未压缩 tar、文件逻辑大小和估算磁盘块。
快照复用按完整父层链计算，不能只看单层摘要。当前接受单平台 raw/gzip OCI
层；未知编码和多平台索引显式拒绝。它是构建端分析器，会读取层到内存，
不是低内存节点下载器。

## 已核实的源代码

指定基线的 `deployment/docker/core.Dockerfile` 构建全部 Rust 二进制；
运行层包含 core、core-admin、CA、curl。扩展镜像使用 `CGO_ENABLED=0`、
`-trimpath -s -w`，运行层也装 CA、curl。Rust 发布配置已有 thin LTO、单 codegen
unit 和 strip，不能把这些既有配置再计为新增收益。

Rust `build.rs` 直接编译五个协议；Go 绑定位于 `internal/corepb/`，由
`scripts/generate-core-protocol.sh` 生成并检查。Bun 工作区仅有 `apps/web`。
用户 CLI `apps/lmm` 是独立 Rust 包，无核心包路径依赖；不是服务节点依赖。
详细源路径和 Git blob 摘要见 `evidence/source-audit.json`。

| 变化 | 重建 | 至少验证 |
|---|---|---|
| Go 扩展源码、go.mod、go.sum | extensions | Go、协议旧新版本、核心边界 |
| Rust 核心源码、锁文件、schema、build.rs | core、core-admin | Rust、资金持久化、协议 |
| 前端源码、前端配置、bun.lock | web | 前端测试、类型、HTTP 约定 |
| Protobuf、生成器、已生成 Go 绑定 | core、core-admin、extensions | 生成结果、两端、旧新协议、前端约定 |
| 用户 CLI | cli | CLI 自身；不重建服务 |
| 根 package.json、未知 packages/scripts | 停止自动选择并要求检查 | 构建入口/共享依赖审查 |

Rust 两个二进制仍共享同一 crate。此轮不冒险按单个 Rust 文件猜测依赖，
也不复制核心源码另建 admin crate。发布时可以只分发需要的运行镜像。

## 运行实验

本目录的 Python 测试需要 `pytest`、`cryptography`；差分需要 `zstd`。
这些工具在构建端使用，不把它们加到服务节点镜像。

```sh
python3 -m pytest -q tests
(cd probe && GOTOOLCHAIN=local GOWORK=off GOPROXY=off go test -race ./...)
python3 impact.py apps/lmm-extensions/internal/app/run.go
python3 impact.py --base BASE_COMMIT --head HEAD
python3 run_lab.py --source-commit FULL_EXPERIMENT_CODE_COMMIT
```

`run_lab.py` 实际构建独立 Go 探针；比较两种实现，gzip、zstd、xz 和 zstd
基准差分；每个解压/启动项目重复七次，绑到一个可用 CPU，记录原始时间和
子进程最大 RSS（GNU time 读取被测命令；耗时包括启动测量工具的开销）。编译时间只作本机已缓存数据，不作跨机器结论。
内存仍是整个实验容器限制，**不是 1c1g 服务器实测**。

仅版本字串不同的探针 v1/v2 是差分样本。这可能特别有利于差分；不是一般
Go 代码变更，更不是 Rust 核心更新。签名探针包与本地 OCI 探针布局在 `out/`。
结果见 `evidence/measurements.json` 和 `RESULTS.md`。`local_pull.py` 对签名的
探针 OCI 索引和完整 blob 做真实回环传输；不使用 Docker，也不解压业务镜像。

`out/signed/TEST-ONLY-public-key.raw` 是实验生成的信任锚；私钥不落盘。
**随包带来的公钥不是生产来源证明。** 生产必须从独立可信配置取发布公钥，
或验证批准构建工作流的签名身份。摘要只能说明内容一致，不能证明构建者可信。
本轮没有得到生产构建者证明、正式镜像签名、SBOM 或漏洞扫描结果。

## 正式业务镜像的构建端接线候选

`build_components.sh` 检查干净源树、明确提交和基线固定工具版本。编译缓存、
完整调试二进制及独立符号保留在构建端。运行构建上下文只给 `bin/` 或 `web/`。
不得把仓库、调试文件、包管理器缓存或用户 CLI 传给服务节点。
Rust 调试构建参数不同于现有发布参数；实际功能、大小及性能须重新对照，
不能把本轮探针数字代入 Rust。

每个组件使用独立缓存键：组件源树、锁文件、协议输入、工具链、目标架构、
编译参数和 Dockerfile 摘要。其他组件的提交不得进入本组件二进制版本字段，
否则会人为制造全量二进制层更新。全局发布清单可以另行记录仓库提交。

`runtime-assets/` 在构建端准备并校验 CA、完整时区库及探针。
生产 CA 不能直接采纳本实验机器的信任库。glibc 基础镜像必须覆盖实际 ELF
解释器及所有动态库；静态候选必须实际检查链接结果。Docker 提供的
`/etc/resolv.conf`、`/etc/hosts` 和 `/etc/hostname` 不应烘焙进镜像。
只读容器需要显式 `/tmp` 临时挂载和受控 RPC 目录；本轮未验证这些候选启动。

发布清单必须以 `component-version-platform@sha256:...` 标识最终 OCI 清单，
并记录源提交、构建身份、协议及新架构 schema 范围、资产摘要和签名策略。
禁止仅按 latest 判断版本。磁盘预算包括旧版本、新压缩层、解压内容、临时包、
数据库/日志余量与明确的回滚保留。先预拉取只移动成本发生时间，不消除下载。
OCI 的共享层能复用，但二进制改变后通常仍下载整个新层；二进制差分是另外
一种明确校验基准与结果的协议，不是 Docker 默认能力。

差分必须在节点 1 CPU 下比较完整下载和还原的 CPU、RSS、峰值空间、失败回退。
基准不匹配必须拒绝，再重新评估完整包所需空间；本原型不会悄悄回退或切换。
UPX 不在默认方案：本机没有 UPX，也没有其启动、内存、栈回溯、签名稳定性
对照，不能只因压缩率高就启用。

## 明确未通过的验收项

实际 core/extensions 镜像及 glibc/musl 对照；core-admin 分离的真实节省；
业务首次拉取、单 Go 更新、单 Rust 更新；Docker 解压和快照器的实测临时峰值；
1c1g 下 CA、TLS、DNS、时区和真实请求；系统断电后的文件持久性；运行实例的
就绪检查、切流与回滚；正式发布身份和签名策略。它们在结果里保持 null/未测。

磁盘不足测试分为可用空间预检和注入 ENOSPC，没有故意填满共享文件系统。
断网测试使用真实回环 TCP 连接中途关闭，不是公网限流或远程 Docker registry。
进程被强制杀死可能留 `.incoming-*`；只允许持锁清理未引用临时目录。此轮不
自动清理历史版本，不与 DP-28 的空间回收或 DP-29 的发布控制抢职责。

参考：OCI image-spec 的 config、manifest、image-layout；Docker Build cache
及 Optimize cache usage。规范地址记录在 `evidence/source-audit.json`。
