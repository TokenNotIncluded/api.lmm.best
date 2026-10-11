# 给 lightdot 的本地复测说明

## 交付状态

这是第一阶段补丁，不是整站端到端验收完成版。

阅读基线：`TokenNotIncluded/api.lmm.best`，PR #675，分支
`wip/rust-core-go-extensions`，提交
`bbde7468dcbd08091bc6a666c27946c2a385f2bf`。

本轮生成的是新增文件补丁。没有取得完整仓库的本地检出，没有编译整个 WIP。
没有推送远程、合并、发布或部署生产。报告中的 `actual_repository_sha` 为
`null`，没有拿阅读基线冒充实际构建版本。

已实现独立供应商模拟服务、配置和限制、虚拟时钟、测试批次隔离、报告、
四种模型协议、Stripe 子集、旧 ePay CNY 配置、MCP 测试工具，以及真实
Stripe/ePay/MCP 适配器的测试接线代码。没有新增钱包、账本或路由业务实现。

## 实际验证

最终隔离报告：**39 个 PASS 记录，0 个 FAIL，16 个 BLOCKED，0 个 NOT RUN**。
这是报告行数，不等于全部业务通过。

- Python 模拟器、清理和报告规则：29 项测试通过。
- 正在运行的模拟服务：8 个实例检查通过。
- Go 测试连接器：3 项测试通过，执行了 `-race`；在报告中合为一行。
- 本轮结束时的资源状态检查：通过。

测试在只有回环网卡的 Linux 网络空间中运行。独立 Go 缓存随本轮清理。
两终端 `up` / `run`、0600 运行清单、重置、SIGINT 退出及端口关闭，也另外实测。

16 个 BLOCKED 分别为：

1. 两组真实生产适配器测试：支付、工具市场。代码已写入 `_test.go`，但本机没有完整
   WIP 源码及目标模块所需的构建环境，未编译、未执行这些新测试。
2. 十四个真实应用场景。当前补丁没有提供应用启动、独立数据库、真实账户初始化、
   Rust 资金接线和最终数据库/账本断言驱动。`scenarios.py` 是验收规范，不是完成的
   可执行整站测试。当前基线的核心入口还主动返回 `core_migration_incomplete`。

模拟器的成功响应、Go 连接器测试成功，都不能证明真实应用的计费、路由或到账正确。

## 应用补丁

在实际仓库中新建测试分支，先核对提交和工作区，不要直接应用到无关的 main。

```sh
git switch -c test/unified-mocklab bbde7468dcbd08091bc6a666c27946c2a385f2bf
git rev-parse HEAD
git status --short
git apply --check /path/to/api-lmm-mocklab.patch
git apply /path/to/api-lmm-mocklab.patch
```

本轮只在空的临时目录中检查了新增补丁能否应用，并比对了应用后的文件。
这不替代在真实 WIP 检出中执行 `git apply --check`。若分支已改目录，先核对
当前模块位置和接口，不要为了通过测试降级依赖或恢复旧目录。

## 执行

准备与实际模块匹配的 Go 工具链和离线依赖。读取 `go.mod` / 工具链配置；
不要采用旧文档里的版本号，也不要把 Go 1.25 要求改为本机的 Go 1.23。

优先使用隔离运行：

```sh
python3 -B scripts/mocklab.py isolated --output /tmp/lmm-mock-isolated-01
```

输出目录不能已存在。退出码 2 表示还有 BLOCKED 或 NOT RUN，不是“全部通过”。
隔离运行失败时不要改为使用公网或真实凭据来补齐。

需要持续操作供应商模拟服务时，在两个终端分别执行：

```sh
# 终端一；只启动模拟服务，不启动应用或数据库。
umask 077
python3 -B scripts/mocklab.py up \
  --config tests/mocklab/example.json --manifest /tmp/lmm-mock-run.json

# 终端二。
python3 -B scripts/mocklab.py run \
  --manifest /tmp/lmm-mock-run.json --output /tmp/lmm-mock-report-01
```

外层普通网络环境不运行真实生产适配器测试；用 `isolated` 命令执行它们。
退出用终端一的 Ctrl+C。仅删除本轮的清单文件，不要批量杀进程或清理其他任务。
重复同一组适配器测试前，启动新的一轮，或在没有活动请求/待发送回调时重置本轮。

```sh
python3 -B scripts/mocklab.py control --manifest /tmp/lmm-mock-run.json \
  --data '{"op":"reset"}'
```

重置仅清理供应商侧测试状态，不清理应用数据库。端口默认为系统分配，不写入 hosts。
`main.local` / `wip.local` 可分别配置本地连接地址和 Host 请求头。目前只比较健康入口，
不比较完整业务。请替换 `targets.example.json` 中的示例端口和占位 SHA。

## 下一阶段的必要工作

先接通真实 WIP 宿主、身份和资金，再补应用层驱动。不能通过假的 Core/Funds、
直接写余额、关闭验签或解除 `core_migration_incomplete` 保护来让测试变绿。

支付需要走正常订单入口、正常回调入口和工作队列，并读取真实 Rust 凭据和账本。
模型/渠道需要走真实 Rust 解析、路由、取消和结算，不能用测试自己的失败切换循环。
工具市场需要完成真实登记、发现、发布、报价、调用和资金提交。固定按次计费下，
被接受且返回 `isError` 的调用，不应一律当成免费；丢失结果的调用也不能自动重试。

应用时钟与供应商虚拟时钟是两个对象。本补丁没有控制应用时钟。应用过期、租约、
关闭订单和恢复场景，必须接入其真实时钟接口，再逐项断言。

保留原有 OAuth、支付、PostgreSQL 和 relay 测试；本补丁没有完整替代它们。
协议范围和暂不支持项见同目录 `README.md`。确认这十四个应用场景真实执行并通过
之前，不合并、不发布、不部署生产。
