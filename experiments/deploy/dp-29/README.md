# DP-29：按需运行的发布控制原型

状态：本地原型，不能用于生产验收。关联 #675。补丁应用目标为 `wip/rust-core-go-extensions@72667564c0431754d4856dc2e0db55f360bd2745`，拟用分支为 `research/dp-29-release-control`。

`lmm-release` 是公共入口。它处理计划、预检、拉取、准备、业务就绪、切流、观察、旧工作完成和回退。进程执行有限步骤后退出，不安装后台服务。Python 仅在控制端运行；服务节点不需要本项目的用户 CLI。

当前环境不能直接拉取 Git，也没有 Docker 或 Rust 编译器。基线代码已通过 GitHub 只读接口核对。交付为新增文件补丁，不是完整仓库副本；没有创建继承指定提交的本地分支，也没有创建远端分支或 Draft PR。PR 文案在 [PR_DRAFT.md](PR_DRAFT.md)。

## 先运行本地验证

需要 Linux、Python 3.10 以上及标准库。实际验证环境为 Python 3.13.5。无需安装第三方包、数据库服务器或 Docker。

```sh
cd experiments/deploy/dp-29
python3 -S -B verify.py
```

测试只使用临时目录、独立 SQLite 文件和 `127.0.0.1` HTTP/SSE 测试进程。它们不是 Rust/Go 应用，不验证真实计费。结果见 [evidence/REPORT.md](evidence/REPORT.md)，原始输出见 `evidence/unittest.txt`。

## 使用统一入口

下例创建三个**同机模拟节点**，不连接任何服务器。目标目录必须不存在；不会覆盖已有状态。

```sh
cd experiments/deploy/dp-29
LAB=/tmp/dp29-lab
python3 -S -B demo.py "$LAB" --nodes 3
ctl() { ./lmm-release --state "$LAB/control" "$@"; }
ctl init --inventory "$LAB/inventory.json"

ctl plan "$LAB/go-next.json" > "$LAB/plan.json"
ID=$(python3 -S -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$LAB/plan.json")
ctl status "$ID"
ctl run "$ID" --expect-revision 1 > "$LAB/run.json"
ctl status "$ID"

REV=$(python3 -S -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$LAB/run.json")
ctl rollback "$ID" --expect-revision "$REV" > "$LAB/rollback.json"
REV=$(python3 -S -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$LAB/rollback.json")
ctl run "$ID" --expect-revision "$REV"
```

`step` 只推进一步。`run` 默认最多推进 100 步，遇到等待或结果不明就退出。退出码 0 表示命令已处理，不表示发布完成；必须检查输出的 `stage`、`pending` 和 `note`。拒绝命令的退出码为 2。

继续执行前，先从 `status` 取新的 `revision`。不能猜测版本号，也不能用旧脚本自动覆盖新状态。`rollback` 只提出并持久保存回退方向，随后仍由 `step` 或 `run` 执行生命周期。

## 实现的边界

发布清单锁定镜像摘要、CPU 架构、配置摘要、能力、协议、表结构读写范围和旧页面 API 要求。只有未启动的同组件计划可以合并。已经预检并取得执行权的计划，即使尚未切流，也不被新计划静默替换。

所有操作人使用**同一个控制端状态目录**。本地文件锁防止两个进程同时执行步骤；发布版本号防止旧命令更新新记录。每个节点还保存控制端身份、递增执行代号及操作收据。不能复制状态目录到多台机器并把它当作多主控制平台，也不能把状态库放到 NFS 后宣称具备分布式锁。

控制器先保存操作意图，再调用节点。断网、超时或确认丢失时，保留原操作 ID。下一次调用核对同一操作，不能换 ID 盲目重试。结果未确认前禁止回退。新执行代号必须先送达全部库存节点；有节点离线时等待，不牺牲安全继续切流。

本版保守地一次更新一个目标节点，单组件最多保留两个运行版本。首节点完成观察和旧工作收尾后，才扩大到下一节点。磁盘或内存不足时等待；旧流没有强制截止时间。长流可以延迟后续发布，当前不据此承诺实际发布频率。

库存可通过 `targets` 指定各组件的目标节点。全体节点参与通信和版本预检，但 Go 更新只对 Go 目标组执行生命周期。见 `examples/inventory-hooks.json`；其中地址和摘要是占位信息，不能直接运行真实发布。

## 回退、前端与空间

回退只改变应用产物和对应配置。控制器不持有业务数据库连接，不执行业务建表或迁移，不导入余额快照，不清理会话。旧程序不能理解当前表结构或业务状态时，拒绝回退。若原发布已经结束，可以提交兼容当前数据的新版本计划进行前向修复；测试覆盖了此路径。活动计划仍被阻塞或操作结果不明时，必须先核对节点，不能强行替换它。

前端旧资源和旧页面接口要求分别保留。停止旧前端进程不等于旧浏览器标签页已经关闭。缺少可靠的引用、会话寿命或资源保留证据时，不删除旧资源、不强制刷新流。真实资源配额、引用过期及删除由 DP-28 提供，本版没有实现镜像或前端资源垃圾回收。

控制状态最多保存 64 条计划，满后拒绝新计划，不静默丢证据。导出所需审计记录后，可显式清理已结束的旧计划：

```sh
ctl status
# 使用上一命令返回的实际 epoch；下面的 2 只是演示第一次发布加回退。
ctl gc --keep 12 --expect-epoch 2
```

清理保留运行中、排队中、被阻塞和仍有未确认操作的计划，并保留每个组件最新的回退记录。它只回收控制状态，不删业务数据、节点收据、镜像或网页文件。SQLite 数据库页预算为 16 MiB；提交日志和文件系统占用需另留空间，不能把 16 MiB 当成整个目录的硬上限。

## 接入现有研究任务

只提供一层接口绑定，没有复制旧部署器。DP-22/23/24/27/28 的真实适配器在基线中尚未提供，默认拒绝缺失接口。`fixture` 模式用于测试，不能自动回退为真实部署。接口及剩余联合验收见 [CONTRACT.md](CONTRACT.md)。

## 在指定基线上应用补丁

在具有指定提交的仓库中执行。使用新工作目录，避免覆盖其他 Agent 的改动。命令不推送，不创建 PR，不触发 CI。

```sh
git worktree add -b research/dp-29-release-control /tmp/dp29-review \
  72667564c0431754d4856dc2e0db55f360bd2745
cd /tmp/dp29-review
git apply --check /path/to/dp-29-release-control.patch
git apply /path/to/dp-29-release-control.patch
cd experiments/deploy/dp-29
python3 -S -B verify.py
```

`/path/to/dp-29-release-control.patch` 需要替换为补丁实际位置。若分支或工作目录已存在，停止核查，不覆盖它。
