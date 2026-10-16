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

这项预检只证明资源工具的行为，尚未把真实商店进程接入共享资源组，
不代表 Core+Go 持续联合负载、完整外部支付链或生产验收已经通过。

定位入口：`markref context commerce.money-flow`；新文件未跟踪时用
`markref context runtime.joint-resource --include-untracked`。
