# DP-25：小内存数据库预算与恢复研究

**状态：未通过业务验收。当前交付是源码研究、本地预算检查和未完成运行验收的数据库实验代码。**

基线：`72667564c0431754d4856dc2e0db55f360bd2745`。工作分支目标：`research/dp-25-database-budget`；Draft PR 目标：`wip/rust-core-go-extensions`；关联 #675。本次未推送、未建立远程分支或 PR，未启动远程 CI，未部署或合并。交付新增文件补丁，不修改 main，也不恢复旧 Go 数据库迁移。

## 先读结果

[研究报告](REPORT.md) 给出真实接线、连接预算、数据库与副本边界，以及未完成的验收项目。[本次记录](evidence/result.json) 把未测量的指标保留为 `null`。`python-tests.txt` 和 `proxy-tests.txt` 只证明工具自身通过了对应检查，不证明 PostgreSQL 或业务账务正确。

`budget.py` 不联网、不连接数据库。它累计所有物理连接池，包括旧新版本同时运行、管理进程，以及每个 PgBouncer 实例的数据库/角色池。池大小未知、权限隔离未验证或预算超出时，返回非零状态。克隆的池对象只能计入一次。这个检查不自动发现遗漏的池，清单仍须按实际启动入口复核。

`lab.py` 只创建本机隔离实验资源。不接受外部 DSN，不拉镜像，不构建应用，不调用 CI，不接触已有卷。使用固定摘要的、本机已有的 PostgreSQL 17 镜像。数据库限制为 1 CPU、256 MiB、无额外交换空间。所有服务没有宿主端口映射；测试客户端在单独的受限容器中。它不会把本机容器实验写成独立 1c1g 服务器结果。

实验调用原仓库 `core_billing.post_ledger`，不创建另一套余额实现。加载的 `identity.sql` 和 `ledger.sql` 必须匹配基线 Git blob 摘要。只使用新库和虚构测试单位。为测试建立账号、角色和分配请求编号的序列，不伪造核心结构版本指纹，不声称启动了真实身份 HTTP 或计费宿主。

## 文件

| 文件 | 用途 |
|---|---|
| `budget.py`、`test_budget.py` | 全局连接上限计算、阻止超预算配置、工具测试 |
| `source-inventory.json` | 已核查入口、固定提交和文件摘要；明确未接线模块 |
| `postgresql.conf` | 256 MiB 实验候选参数，不是容量合格证 |
| `lab.py`、`test_lab.py` | 仅本机的新库实验、权限隔离与安全条件检查 |
| `sql/` | 真实账本热点预留/结算、守恒核对、数据库指标 |
| `proxy/` | 只用于实验的 COMMIT 确认截断工具；不用于生产代理 |
| `evidence/` | 本次已执行的工具测试、预检和空缺指标 |

## 只做本地工具检查

在本目录执行。Python 只使用标准库。Go 小工具与应用自身的 Go/Rust 构建分开，不改变应用工具链。

```sh
python3 -B -m unittest -v test_budget test_lab
(cd proxy && GOWORK=off GO111MODULE=off GOTOOLCHAIN=local go test -count=1 -race -v)
python3 -B budget.py --nodes 3 --surge 1
```

最后一个命令应以状态 2 退出：仅身份宿主的配置上限已是 40 个应用连接，加入建议预留后为 46，大于 32。状态 0 也只代表清单满足连接条件，不代表延迟、内存、资金或 HA 验收。

自定义清单可用 `--plan /absolute/path/plan.json`。格式见 `current_plan()`。`instances` 是稳定运行数量，`surge` 是同一时刻额外存活的数量，不是累计更新次数。`role_isolation_verified` 必须来自权限与保留连接实测，不得为了得到绿色结果随意改成 true。PgBouncer 条目使用 `kind=pgbouncer_backend`，并列出 `proxy`、`database`、`user`、`reserve_pool_size`。

## 数据库实验的明确前提

需要本机 Linux Docker Engine、现成 PostgreSQL 17 镜像、完整指定基线源码，以及与镜像架构一致的静态 Linux 小工具。实验仅支持能在容器内读取 cgroup v2 计数的环境；缺失时停止，不拿宿主总内存冒充数据库占用。

**在隔离开发机执行，禁止在生产宿主运行。** 本轮没有满足这些前提，下面的数据库命令没有在本轮成功执行。

先在开发机编译独立测试小工具。目标架构须与 PostgreSQL 镜像一致。不要在 1c1g 服务节点编译核心或扩展。

```sh
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT/experiments/deploy/dp-25/proxy"
GOWORK=off GO111MODULE=off GOTOOLCHAIN=local CGO_ENABLED=0 \
  go build -trimpath -o ../commit-drop .
cd ..
# DP25_PG_IMAGE 必须事先设为本机镜像的 sha256:... ID 或 repository@sha256:...。
: "${DP25_PG_IMAGE:?Set a locally installed immutable PostgreSQL 17 image ID}"
python3 -B lab.py --source "$ROOT" --image "$DP25_PG_IMAGE" \
  --proxy "$PWD/commit-drop" --out "$PWD/local-results/preflight-01"
# 预检通过后，使用另一个未存在的输出目录，明确选择 --run。
python3 -B lab.py --source "$ROOT" --image "$DP25_PG_IMAGE" \
  --proxy "$PWD/commit-drop" --out "$PWD/local-results/run-01" --run
```

源码文件摘要检查只覆盖这两个 SQL 文件，不声称验证了整个工作树。预检不会启动 Docker；选择 `--run` 后还会拒绝远程 Docker 上下文、错误数据库大版本或未实际应用的硬限制。输出目录必须新建；已有目录不覆盖。默认每个客户端 50 次预留/结算迭代，可设 1–100 次；并发固定 1、4、8、16。每个负载阶段有 60 秒外层超时。达到超时或任何断言失败，不计算合格吞吐。

测试结束只清理本轮随机名、相同运行标签的容器、网络和新数据卷，不执行全局 prune。保存到新输出目录的逻辑备份仍保留；它不是异机备份。测试中断后不要删除不明卷。只对照运行标签和记录处理本次孤立资源。

## 已写入实验代码，但尚未完成数据库运行验证

实验包括应用/只读/扩展角色隔离；真实账本重复请求和同键不同参数拒绝；热点预留与部分结算；提交确认丢失后的权威查询和同键重试；保留数据盘的数据库进程崩溃恢复；检查点；逻辑备份恢复至另一新库；恢复后的重复充值拒绝。

COMMIT 小工具只处理隔离网络的明文 PostgreSQL 协议，截断服务器已生成的 COMMIT 完成帧，禁止转交成功确认。它不是 TLS 代理。其本地测试只验证帧处理，不代表已成功注入真实数据库故障。

观察数据包括 `pg_stat_activity`、`pg_stat_database`、`pg_stat_wal`、`pg_stat_checkpointer`、`pg_stat_io`，以及容器的内存、CPU、IO 原始计数和 PSS。连接峰值是采样下界，不是瞬时峰值保证。每次采样记录耗时；未执行带/不带探针的对照，不扣除估算开销。设置输出来自 `pg_settings.setting` 的原生单位：例如 shared_buffers 与 wal_buffers 是 8 KiB 块，work_mem 与 maintenance_work_mem 是 KiB。

即使全部已实现阶段通过，脚本最多输出 `partial_sql_database_tests_passed`，`business_acceptance` 始终为 false。尚未实现事件积压、真实应用池断线重连、PgBouncer 对照、共置对照、独立多节点、持续 WAL 归档恢复、副本隔离与网络分区的联合验收。检查点和备份当前在负载结束后进行，不能证明它们与持续业务并发时的影响。
