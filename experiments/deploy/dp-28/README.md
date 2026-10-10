# DP-28：磁盘生命周期本机实验

基线：`wip/rust-core-go-extensions@72667564c0431754d4856dc2e0db55f360bd2745`。目标分支：`research/dp-28-disk-lifecycle`。关联 #675。

本目录是研究原型，不是生产部署工具。它只允许在独立、限额的本机测试文件系统写入。没有改变已有 Docker 配置、资金逻辑或数据库。用户 CLI 不是服务节点依赖。

实测结论见 [REPORT.md](REPORT.md)。数据库及发布控制交接见 [HANDOFF.md](HANDOFF.md)。拟议 PR 说明见 [DRAFT-PR.md](DRAFT-PR.md)。

## 一条命令重跑

在可创建用户、挂载和网络隔离空间的 Linux 本机执行：

```sh
bash experiments/deploy/dp-28/run-lab.sh /absolute/new-dp28-evidence
```

要求 Python 3.11+、C 编译器、Bash、GNU coreutils 和 util-linux。输出目录必须是新的绝对路径。脚本编译一个本地测试程序，创建 16 MiB、512 个文件节点名额的独立 tmpfs，然后执行首次安装、20 轮更新和 34 项测试。16 MiB 只是故障实验限额，不是服务器容量建议。测试不联网，不调用远程 CI，也不运行 Docker。

脚本结束时卸载并删除自己创建的测试挂载点。测试程序和生成的包留在明确指定的证据目录。权限或隔离条件不足时退出，不改用宿主磁盘压力测试。禁止去掉隔离检查后在生产运行。

## 内容

| 文件 | 作用 |
| --- | --- |
| `disk_lifecycle.py` | 本机账单、按产物计算的空间/文件节点预检、默认预览清理、中断重试、版本引用保护、有限诊断日志 |
| `run_experiment.py`、`fixture.c`、`lab_support.py` | 可复现的本机原生程序和 20 轮实验；不是 Rust/Go 产品程序 |
| `test_disk_lifecycle.py`、`test_contracts.py` | 系统调用故障测试，以及离线 Docker 清理规则测试 |
| `docker_plan.py` | 只读 JSON 清理预览；不会连接 Docker，也没有删除接口 |
| `event_retirement.py` | 事件载荷回收的前置证明检查；不访问数据库 |
| `policy.lab.json` | 明示实验余量及日志规则；不表示真实业务容量 |
| `evidence/` | 原始账单、峰值采样、实际删除清单、保留理由、故障记录、产物摘要、测试输出 |

## 预检与清理入口

以下命令只能在测试脚本建立的隔离空间中使用，`ROOT` 必须是本项目标记、调用者拥有的 0700 私有目录。不要给它传生产目录。

```sh
python3 disk_lifecycle.py --root "$ROOT" init --policy policy.lab.json
python3 disk_lifecycle.py --root "$ROOT" bill
python3 disk_lifecycle.py --root "$ROOT" preflight   --archive "$ARCHIVE" --manifest "$MANIFEST" --manifest-sha256 "$MANIFEST_SHA256"
python3 disk_lifecycle.py --root "$ROOT" gc
# 核对上一步的逐条删除计划和保留理由后，才传入计划摘要。
python3 disk_lifecycle.py --root "$ROOT" gc --apply-plan-sha256 "$PLAN_SHA256"
```

`gc` 不传摘要只预览。摘要绑定目录标记、状态代数及文件身份。状态变化后必须重新预览。未完成清理使用原摘要恢复；已完成的同一摘要返回已有收据，不重复删除。

`preflight` 可检查固定清单的普通预编译文件包（`kind=lmm-release-package-v1`）。它不支持把 Docker 虚拟镜像大小当作解压空间。归档仅允许扁平普通文件、444/555 权限、精确长度及 SHA-256。拒绝符号链接、归档硬链接、路径越界、重名及摘要不符。`install-fixture` 只执行本目录生成的测试包，不执行真实产品包。SHA-256 固定输入不等于校验发布者身份；可信清单来源及签名验证交给发布控制。

## 不删除的内容

当前版本、上一已验证版本、仍被进程使用的版本，以及控制器明确保留的版本都受保护。引用没有自动到期时间。进程退出和业务排空不是同一件事；真实接入必须满足 HANDOFF 中的释放条件。

数据库、WAL、资金记录、审计、去重证据、未确认事件、备份、未知缓存及无标记资源不进入通用清理列表。本原型不能通过删除它们解决空间不足。

程序按目录文件描述符访问，拒绝符号链接、路径越界、不同挂载点（含同一设备的 bind mount）和异常硬链接。拉包、写状态、清理共用项目锁；版本进程另持有可继承的共享锁。清理过程中必须再次检查身份和引用。

`/proc` 检查仅覆盖当前可见进程；看不到的进程和未知映射大小会在账单中标出。它不是完整宿主机证明，不能代替运行时持有版本引用。私有目录和协作锁也不能防止拥有同等或更高权限的管理进程绕过规则。

## 诊断日志

实验日志最多 3 份，每份 4096 字节；轮转文件超过 86400 秒可回收。活动文件受容量限制，不承诺按时间自动截断。文件节点上限和日志数量一同检查。

日志只接收事件名、数量、时间。没有任意正文、密钥、URL 或模型大响应入口。写日志失败会明确返回失败；发布状态和清理收据写失败必须停止操作，不能借用“诊断可丢弃”规则继续发布。

[compose.logging.example.yml](compose.logging.example.yml) 仅是项目服务的容量参数示例，未应用。不要用通用脚本修改 Docker 自己的日志文件，不要轮转资金审计表。

## 来源核对

源码读取固定在上述基线：`deployment/docker/compose.core.yml`、`compose.identity.yml`、`core.Dockerfile`、`README.md`。核心 Compose 已有 30 分钟停止宽限；这不等于磁盘版本保留证明。数据库使用独立命名卷。所读 Compose 没有服务级日志容量条目；这不能推出未知宿主的 Docker 默认日志没有限额。镜像没有本原型要求的自定义管理标记，离线清理默认保留它们。

官方参考：Docker [磁盘统计](https://docs.docker.com/reference/cli/docker/system/df/) 与 [local 日志](https://docs.docker.com/engine/logging/drivers/local/)，读取于 2026-10-10。具体系统调用差异同时保留了本机 `df`、`du` 原始输出。
