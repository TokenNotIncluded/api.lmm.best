# 本地商店流程与资源预检

所有写入命令先按仓库 AGENTS.md 用 `agcc run` 包裹，并覆盖输出、编译缓存和短路径进程目录。
只使用脚本新建的本机 PostgreSQL；不传入已有数据库或生产凭据。

`verify_process.py` 会构建实际 Go 扩展，启动一次性数据库，运行真实 Core → Go
商店资金链回归，并核对运行前后源码。测试包括并发支付、退款提交后丢失确认、
杀死两个产品进程、重启后的工作队列恢复，以及原付款方与账本守恒。
测试进程会在启动和就绪时保存实际 `/proc/PID/exe` SHA256、inode、启动时间和 boot ID。

默认仍由 Cargo 构建运行测试。已有产物可以显式配对：

```sh
python3 -B tests/commerce/verify_process.py \
  --output-root "$PWD/.local-tests/commerce-review" \
  --process-root /tmp/lmm-commerce-review \
  --core-binary "$CORE_BINARY" --core-sha256 "$CORE_SHA256" \
  --test-binary "$COMMERCE_TEST_BINARY" --test-sha256 "$COMMERCE_TEST_SHA256"
```

SHA256 必须来自已审查的产物记录；脚本核对产物摘要，不负责证明外部产物由哪份
源码编译。`--test-binary` 是编译后的 `commerce_process` 测试可执行文件，
不是 Core。参数必须成对提供；预编译测试运行不调用 Cargo，仍构建 Go。也可配对传入 `--extension-binary` 与
`--extension-sha256` 复用已审查的 Go 产物，从运行阶段移除构建与缓存扫描。
脚本要求指定商店用例恰好通过一次，不能把空测试结果或忽略当作成功。
输出目录在仓库 `.local-tests` 下；进程目录需为短绝对路径，避免 Unix socket 超长。
合成凭据和日志只保留在私有输出目录，失败保留数据库证据，不自动重试。

另有资源组基础设施预检：

```sh
python3 -B tests/commerce/verify_resource_group.py \
  --output-root /absolute/private/new-output
```

输出目录必须尚不存在，父目录须已存在。需要当前用户的 systemd bus 和 cgroup v2。
脚本用两个 Python 忙循环检查共享父组的 1CPU/512MiB 硬上限，子组各256MiB、
swap 为0，再验证退出17和未创建单元不会阻断兄弟进程清理。
五秒 CPU 采样要求合计在0.5–1.2核；繁忙主机可能导致下界失败。
临时单元只归本次随机命名的组所有，不安装持久服务，清理后核对 PID 和 cgroup 消失。

这项预检只证明资源工具的行为。真实产品接线使用 `verify_process.py --shared-resources`，
必须同时提供上述三组可执行文件与 SHA256 参数。每个产品由测试直接创建并先暂停，
在瞬态 scope 中核对父子硬限制与归属后再执行产品；SIGKILL 与 wait 仍针对直接子进程。
每次就绪前后继续核对 PID、启动时间、进程组、可执行文件与 cgroup。
运行后要求 Core 和 Go 各两次启动（含故障重启）全部留下资源回执，并清理全部scope。
`resources/samples.json` 每约250ms观察共享组与子组 CPU、内存、任务数及产品 RSS；
子组在崩溃/重启间消失会单独记录。采样错误或内存上限/OOM事件阻止成功。
观察线程和测试/数据库断言在产品资源组外；数据库有独立的 fixture 上限。
资源回执写完整后才发布，已有回执不可覆盖，避免采样读到写了一半的 JSON。

共享资源下的短商店回归不代表 Core+Go 持续联合负载、完整外部支付链或生产验收通过。
完整模型流量与商店吞吐重叠、持续过载恢复由下述联合门槛验证；观察者自身预算仍未验收。

定位入口：`markref context commerce.money-flow`；新文件未跟踪时用
`markref context runtime.joint-resource --include-untracked`。


持续联合负载另加 `--mixed-load`（必须同时有 `--shared-resources` 和三组固定产物）。
同一 Core 同时启用模型和 Commerce，Go 仍只有自己的商店数据库。模型付款方和商店买家
使用不同合成账户，分别核账；不把相互抵消的余额当正确。
模型固定5RPS、80%JSON/20%SSE，60秒预热、300秒稳态、60秒满准入过载、4次首帧取消、
60秒有流量恢复与60秒空闲恢复。模型完整成功率至少99%、p95≤1秒/p99≤2秒。
商店在每个有流量阶段固定每秒一笔完整购买/重复购买/并发支付/履约/结算/退款及退款重试，
全部要求真实退款终态，不能把202或准入拒绝计为成功；完整链路p95≤1秒/p99≤2秒。
整笔交易共用从计划提交时刻起2秒的绝对期限。只有订单ID匹配的
`202 money_result_pending`允许每25ms使用相同订单、操作和凭据有限重试；
全部尝试与取消诊断保留在每阶段回执里，其它HTTP错误不重试。
每个订单核原买卖方、5000credits、三份唯一Core操作与账本journal、Go相同回执、库存与余额。
联合负载全部排空后，再执行既有丢失退款确认与撤销seller会话的故障恢复，避免污染正常流量。
UNKNOWN模型hold仍保留140credits，重启分类另用每64条5秒的有限期限验证，不能冒充流量恢复。
观察还记录宿主CPU/内存、产品PSS/FD和观察者状态；运行前后源码必须冻结。
此项仍不覆盖真实支付供应商、其它扩展模块、整机1C1G、Core+Go联合慢socket消费者或旧架构对比。

联合门槛已在修复socket超时回收的冻结产物上重新通过独立复核：2100次模型请求、
480笔完整商店退款交易全部成功，包含持续过载、取消、恢复与故障重启。
540秒负载后另行完成故障恢复与分类，整轮约675秒。3588个UNKNOWN hold保留资金，
重启分类及重复请求没有再次调用上游；本轮产品进程、资源组和成功fixture数据库已清理。
原先的`503 settlement_pending`仍保留为失败证据，未改算为准入拒绝或成功。

修复只扩大明确回滚后的模型最终结算重试：provider-final保存与settle共用2秒的
新尝试启动窗口，每阶段最多8次。它不取消已经开始的事务，不重放提交结果未知的事务，
也不重新调用模型上游；与商店完整链路2秒绝对期限含义不同。
独立进程测试在真实PostgreSQL中注入前三次SQLSTATE 40001，先验证旧产物失败，
再用同一断言验证新产物成功、仅调用一次上游且资金守恒。这是故障注入，不能证明
原负载失败的每次冲突都已逐一归因。源码或产物变化后须重新验证受影响行为。

以上只证明本地合成上游与隔离数据库下的限定范围；真实上游、实际支付、同付款方争用、
完整CLI/Web及前述整机和联合慢消费者门槛仍未完成，不能据此宣称生产验收通过。
Core单独的240秒持续停读压力和共享HTTP/2流隔离已独立通过，见
[质量指南](../../docs/quality.md)；该结果不替代Core+Go联合停读验收。
Core公共模型、只读RPC身份、事件、Commerce各自池上限为2（源码核对）；
本fixture运行时观察Core总额≤8、Go≤2，不能据此声称逐池实测。
