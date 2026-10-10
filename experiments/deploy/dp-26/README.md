# DP-26：运行资源预算

状态：**完成一个最小组件补丁及本地对照；未达到 1c1g 整站验收。**

指定起点：`72667564c0431754d4856dc2e0db55f360bd2745`。
目标分支：`research/dp-26-runtime-budget`。
目标 PR：Draft → `wip/rust-core-go-extensions`，关联 #675。**本次没有推送、建 PR、部署或合并。**

先读 [REPORT.md](REPORT.md)，所有三次重复的中位数和范围见 [results/TABLES.md](results/TABLES.md)。原始记录、采样文件、性能分析文件和基线源码随证据包交付。不要用这份组件报告填入整站容量表。

## 改动

`apps/lmm-extensions/internal/modules/toolmarket/mcp.go` 复用已经检查、解码的 SSE 结果，避免再次检查和解码整份 JSON。重复字段检查、请求 ID、协议版本、错误与空结果检查全部保留。没有缓冲池、共享结果缓存、自动重试、金额计算或权限改动。

新增 `mcp_runtime_test.go`。研究脚本全部在本目录。没有修改 Rust、宿主模块注册、Go/Rust 生命周期、Docker 配置、数据库、依赖锁文件或工作流。

## 本地复现组件结果

需要 Linux、Go、Python 3、Git、`unshare`、`ip`、`taskset`。仅在批准的隔离研究环境执行。脚本不会下载依赖、启动远程任务、访问生产或安装用户 CLI。

`BASE_SOURCE` 指向指定提交的源码。`CANDIDATE_SOURCE` 指向应用补丁后的源码。证据包的 `source/` 和 `candidate/` 也可用于这个**源码片段**试验，不是完整仓库。

```sh
EXP="$CANDIDATE_SOURCE/experiments/deploy/dp-26"
WORK="$PWD/dp26-local-build"
OUT="$PWD/dp26-local-results"

python3 "$EXP/prepare.py" \
  --baseline-source "$BASE_SOURCE" \
  --candidate-source "$CANDIDATE_SOURCE" --work "$WORK"
python3 "$EXP/checks.py" --build "$WORK" --out "$OUT"

for case in idle small_sse stable_sse concurrent_sse slow_reader large_input large_result json_control; do
  python3 "$EXP/runner.py" --build "$WORK" --out "$OUT" \
    --suite scenario --scenario "$case"
done
for suite in settings observer profiles overlap; do
  python3 "$EXP/runner.py" --build "$WORK" --out "$OUT" --suite "$suite"
done
python3 "$EXP/summarize.py" --results "$OUT" --out "$OUT/summary"
```

结果目录不应包含以往运行；脚本拒绝覆盖同名运行。外层执行器的时间上限应覆盖整个命令；也可逐条运行。内部每个测试仍有单独的时限和资源上限。中断后保留部分日志，不把缺失数据补成零，不用只保留最好一轮的方式重跑。

构建不改变项目的 `go.mod`。本次环境只有 Go 1.23.2，低于项目的 Go 1.25 最低要求，因此只从已核对 Git 哈希的源文件中提取所需声明，建立独立标准库测试模块。没有把新架构降级到 Go 1.23。完整项目测试应使用项目批准的工具链和隔离数据库另行运行。

## 环境与安全上限

每个子进程进入新的用户/网络隔离空间，只有回环接口；TLS 测试服务自行生成临时证书，客户端验证该证书，不使用 `InsecureSkipVerify`。负载代码不接受外部地址、生产 Key、数据库或通知配置。

固定一个逻辑 CPU；虚拟地址空间上限 2 GiB；采样常驻内存超过 512 MiB 停止；12 CPU 秒、12 秒测试时限、15 秒外部监控时限；最多 8 条驱动侧在途请求；测量阶段最多 240 请求，另有最多 8 次预热；测量阶段应用载荷估算预算不超过 128 MiB，预热另留最多约 6 MiB。该预算不是 TLS 线上字节的抓包计数。实际运行的最大响应文本 512 KiB，最大输入 256 KiB。常驻内存监控不是硬容器上限；关闭观察器的对照只有地址空间、请求数、CPU 和时限保护。没有关闭 OOM 保护。

性能分析仅用于研究进程。服务节点不需要安装这些工具。完整服务的剩余工作与任务交接见 [HANDOFF.md](HANDOFF.md)，`full-system-plan.json` 只是未执行的试验定义，不是部署配置。
