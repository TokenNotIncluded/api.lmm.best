# DP-30：连续更新的联合验证工具

关联 #675。目标分支为 `wip/rust-core-go-extensions`。本目录只含负载、记录、核对和本轮证据，不实现发布控制、切流、数据库迁移或故障注入。

**本轮没有完成 1c1g 应用验收。** 起点为 `72667564c0431754d4856dc2e0db55f360bd2745`。该版本没有接通模型请求和账本。详情见 [RESULTS.md](RESULTS.md)。

## 可直接运行

需要 Python 3.11 或以上。不安装依赖，不访问外网。

```sh
cd experiments/deploy/dp-30
python3 -B -m unittest discover -s tests -v
python3 -B dp30.py preflight
python3 -B dp30.py plan
# 输出目录必须不存在。只启动本机 Python 测试服务，不启动项目应用。
python3 -B run_fixture_suite.py /tmp/dp30-fixture-new-run
# 离线核对由集成适配器导出的记录。
python3 -B dp30.py verify /path/to/isolated-run
```

`verify` 返回码：0 表示本次检查已验证；1 表示有明确错误或输入无效；2 表示有未验证项。当前工具是单次记录核对器。它始终保留“成对对照、至少三次重复”的套件验收为未验证，不能自己签发整站容量结论。

`run_fixture_suite.py` 为测试进程设置地址空间、CPU 累计时间、文件大小、文件描述符和 CPU 亲和性限制。地址空间限制不是常驻内存限制，亲和性不是 CPU 配额。它不是 1c1g 容器或真实服务器。

## 接入前九项

先用 DP-21 的共同负载和预算替换 `templates/provisional-plan.json` 中的临时值。保留文件摘要。没有取得共同版本时，不比较不同任务的容量。

由 DP-22/23/24/25/27/28/29 的既有实现完成切流、凭据、数据库、产物、磁盘和发布操作。DP-30 不调用 `docker compose`、SSH、`kill`、防火墙、构建、推送或远程 CI。配置文件里没有可执行命令，故障合同不是执行脚本。

固定全部组件集成后的完整 Git SHA。记录每个镜像及配置摘要、实际启用模块、数据库安装状态、隔离规则和节点限制。容器模拟、虚拟机、真实服务器分别保留结果。Git SHA 相同但构建参数不同，也不能直接合并数据。

先完成受限单节点无更新对照。后续按同一到达时间表做 20 轮更新和独立故障组。每个关键对照至少重复三次。20 轮是请求输入，不是生产发布频率。旧版本还在排空时允许发布等待；达到进程、排队或资源上限后停止接纳新发布，不能强杀旧流。

## 有上限的模型负载

`workload.py` 只接受数字形式的回环地址、高位端口和明确的隔离许可。它先核对 `/__dp30/permit` 的随机标识，不跟随重定向，不读取代理变量，不重试请求。并发已满时记录 `client_shed`，不悄悄推迟到下个空闲时刻。

默认没有可调用的应用目标。集成适配器需要在测试入口提供许可响应，并把模型请求送到实际 Rust 路径。只能使用隔离测试账户、测试额度、受控上游和测试 Key。许可响应只是防误操作措施，不能证明隔离；网络和账户隔离还需独立证据。

```sh
python3 -B workload.py \
  --origin http://127.0.0.1:18180 \
  --permit /path/to/isolated-permit.json \
  --expected-chunks /path/to/controlled-upstream-chunks.json \
  --limits-file templates/limits.json \
  --credential-file /path/to/private-test-key \
  --authorize-isolated-test \
  --output /path/to/new-probe-result.json
```

测试 Key 文件不能为符号链接，权限需为 0600 或更严格，最多 4097 字节。Key 不写入结果。会话、服务凭据轮换和用户 Key 撤销由身份适配器分别导出观察结果，模型探针本身不证明这些功能。

硬上限为 16 并发、256 请求、60 秒、4 MiB 总输入、每响应 256 KiB。慢响应头和慢响应体都有总截止时间。文件读取也有行数、单行和总大小限制。实际应用资源停止规则仍由受限节点和独立观察器执行；本工具没有伪造这部分能力。

## 文件

`dp30.py`：计划、只读环境检查、离线核对。`workload.py`：有限 HTTP/SSE 负载。`run_fixture_suite.py`：本地工具回归。`tests/`：正常与故意损坏的输入。`templates/`：尚未接线的计划和结果格式。`evidence/`：源码观察、环境和全部开发阶段的摘要。完整原始记录随本次交付的证据包保存。

字段规则见 [EVIDENCE.md](EVIDENCE.md)。观察缺失必须用 null 或标为未验证，不能填零。模拟数据必须保留模拟标识。不要在结果中写入 Key、会话 Cookie、支付凭据或真实个人数据。
