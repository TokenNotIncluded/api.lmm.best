# Rust / Go 验证记录：2026-09-24 第一批

**Rust 尚未达到 100% 对齐，不能据此替换生产 Go。** 当前 Go 有 704 条路由；
本文件记录的首轮批次结束时，Rust 仍缺少 103 条源声明，普通监听器台账有 28 条禁用占位。
这些是 2026-09-24 的历史测试结果；最新工作区路由审计见
[当前对齐验收](rust-go-parity-current.md)。代码提交状态须核对 Git 修订，测试结果不证明发布或部署。

## 本批实现与验证边界

- 公告状态/顺序确认/管理员查询 3 条接口、脚本列表/下载/编辑/仓库配置与导入 9 条接口、
  会话偏好设置 1 条接口已接入普通监听器。会话列表、删除和撤销其他会话补齐共享缓存拒绝标记。
- ePay 3 条已有占位路由接入真实 PostgreSQL 仓储和签名器，补齐小数金额快照、幂等到账、
  优惠券、首充返佣及提交后的审计/缓存处理。12 组当前 Go 原生 handler 样例参与对照。
- OpenAI 转发补齐完成/失败/取消的结算生命周期、重复请求防护、普通定价基础、token 计数和
  缺失 usage 的部分补全。**普通监听器仍使用原固定费用构造，完整 Go 计费尚未接通**；
  `tiered_expr`、订阅资金来源、工具加价、多模态和自动对账仍是缺口。
- 新增 contract 11/12 迁移及约束破坏回归。部署签名清单可以读取当前 Go 的 billing gate 字段，
  但 Rust 尚未实现该部署排空/恢复流程，操作门禁仍明确拒绝，未扩大生产操作权限。
- 脚本仓库导入逐文件暂存到磁盘，避免将全部仓库脚本留在内存。拒绝仓库符号链接比当前 Go 更严格，
  是明确行为差异，不计作逐项一致。

## 测试证据

| 检查 | 结果 |
| --- | --- |
| 当前 Go `go test ./...` | 通过，106 个包结果；后续新增 ePay 导出测试单独通过 |
| Rust workspace / all targets / all features | 1704 通过，0 失败，135 标记 ignored |
| 显式依赖与补充测试首轮 | 144 项：137 通过、7 失败；保留失败记录 |
| 修复后定向真实依赖复测 | 24 / 24 通过；包括迁移、签名清单、13 项 relay、会话、设置和脚本 |
| 测试夹具修复独立复测 | 4 / 4 通过；迁移 search_path、umask 父测试、真实 HTTP mock、token 数据基线 |
| 新 CI suite 实跑 | ePay 12 / 12、relay settlement 13 / 13、scripts 1 / 1；Go ePay 参考当次重新生成 |
| Go token 计数对照 | 208 条文本/请求向量一致 |
| Clippy | workspace/all targets/all features，`-D warnings` 通过 |
| Rust 格式与 diff 检查 | 通过 |
| 性能校验器反例测试 | 4 / 4 通过：错误 JSON、失败响应、重定向、错误数据不能算成功吞吐 |
| 优化版构建 | Rust 1.91.0 `--release` 成功；Go 1.27.1 默认优化构建成功 |

以上测试存在重叠，不能相加为唯一测试数量。常规 Cargo 成功也不能证明 ignored 测试已执行。
新执行器按编译后的清单运行，给每项测试新建数据库，并额外执行原先缺环境就静默返回的依赖测试。
新 CI suite 检查编译清单数量、实际通过数量及 Go fixture 集合，零测试必须失败。

可复跑入口：

```sh
CARGO_BUILD_JOBS=2 CARGO_INCREMENTAL=0 CARGO_PROFILE_DEV_DEBUG=0 \
  CARGO_PROFILE_TEST_DEBUG=0 RUSTUP_TOOLCHAIN=1.91.0 \
  python3 apps/api-rust/tests/scripts/run-all-ignored.py \
  --output-dir "$HOME/.cache/lmm-rust-parity-check" --fuzz-seconds 30

python3 apps/api-rust/tests/scripts/epay-current-differential.py \
  --output-dir "$HOME/.cache/lmm-epay-current-go-check"
```

本次日志保存在 `/home/lightjunction/.cache/lmm-rust-parity-20260924/`，包含普通测试、
首轮依赖失败、修复复测、CI suite、lint、构建和 54 组性能原始记录。

## 当前普通监听器性能

数据来自 [完整机器可读结果](benchmarks/rust-go-public-reads-2026-09-24.json)。
复跑脚本和方法见 [性能测试说明](../apps/api-rust/tests/performance/README.md)。

同机 Linux x86-64、24 个逻辑 CPU；Go `GOMAXPROCS=4`、Rust `TOKIO_WORKER_THREADS=4`。
两端使用同一份新建 PostgreSQL 18.6 数据、分开的 Valkey 9.1.2 缓存库，关闭请求限流。
Go 初始化数据库后在 verify 模式重启，Rust 启动普通 blue listener；未使用受限 test-instance。
所有编译在测量前完成。

每接口、每并发档、每后端测 3 轮，每轮 10,000 请求，先预热并交替执行顺序。
**54 组测量共 540,000 请求，全部成功，每次 JSON 内容均通过一致性检查。**

| 进程指标 | Go | Rust |
| --- | ---: | ---: |
| 测量前 RSS | 82.54 MiB | 27.26 MiB |
| 测量前 PSS | 80.33 MiB | 24.57 MiB |
| 负载期间最大采样 RSS | 96.30 MiB | 29.62 MiB |
| 进程 swap | 0 | 0 |
| 已初始化数据库上的启动观测 | 约 0.4 秒 | 约 0.1 秒 |

启动仅各观测一次，且就绪轮询间隔为 100 ms，不是精确冷启动结论。RSS/PSS 不包含数据库、
缓存和负载生成器。Go 启动时加载默认分词器，Rust 按需加载；**这里不能推断真实 AI 转发的内存节省**。

以下均为三轮中位数，延迟单位 ms：

| 接口 | 并发 | Go 请求/秒 | Rust 请求/秒 | Go p95 | Rust p95 | Go p99 | Rust p99 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `/api/livez` | 1 | 6,142 | 9,924 | 0.344 | 0.221 | 0.477 | 0.372 |
| `/api/livez` | 8 | 31,306 | 31,103 | 0.605 | 0.681 | 1.017 | 0.941 |
| `/api/livez` | 32 | 17,181 | 15,978 | 4.112 | 4.472 | 5.615 | 5.868 |
| `/api/about` | 1 | 5,976 | 5,725 | 0.351 | 0.371 | 0.480 | 0.535 |
| `/api/about` | 8 | 32,166 | 29,414 | 0.582 | 0.515 | 0.942 | 0.861 |
| `/api/about` | 32 | 18,200 | 20,769 | 3.973 | 3.169 | 5.498 | 4.418 |
| `/api/notice` | 1 | 6,667 | 5,235 | 0.298 | 0.372 | 0.401 | 0.553 |
| `/api/notice` | 8 | 32,137 | 30,347 | 0.586 | 0.503 | 0.957 | 0.789 |
| `/api/notice` | 32 | 18,233 | 20,606 | 3.946 | 3.272 | 5.246 | 4.556 |

还有一个已测得的配置/能力差异：Go 默认记录每次 HTTP 访问，测量生成约 37 MiB 访问日志；
当前 Rust 没有普通请求访问日志，整个 listener 日志仅 780 字节。因此 CPU/吞吐表反映的是
当前两套实现的整体开销，不能解读为单独由语言造成的差距。需补齐日志后再次比较。
同机负载生成器也包含 JSON 校验开销，并发 32 的下降不能单独证明服务饱和。

下一批仍需验证：日志及流式排空语义、首个真实 tokenized 请求、词表预热后内存、实际 AI 转发/流式
并发、完整资金来源/工具计费，以及全部尚缺业务接口。当前吞吐互有快慢，不作“Rust 全面更快”的结论。


## 第二批：生命周期、资金与配置验证（进行中）

这部分是上述首轮结果之后的新增工作，不能与首轮 release 二进制或内存结果混用。

- 请求计数现在覆盖响应体整个生命周期；访问日志在完整响应、body error 或客户端取消时只记录一次。真实 TCP 流测试验证 EOF 和断开连接，HTTP 模块 55 项通过。
- 正常 OpenAI listener 已接入数据库模型价格快照，替换原固定每请求 1 quota 的测试构造行为；启动和周期结算恢复、停机等待后台资金任务正在进行整体接线验证。
- 个人偏好保留语言、币种和会话退出选择，补上排行榜可见性与可信等级控制的 IP 绕过偏好。请求 JSON 的大小写、null、重复字段顺序按照 Go 处理。19 项模块测试、15 项 HTTP 测试与 4 项真实 PostgreSQL/Valkey 测试通过；23 个侧栏形状和 10 个可见性向量来自当前 Go。
- `batch2-live-extended` 首轮 36 项真实依赖测试通过 34 项；认证测试 secret 与共同价格配置/JSON 数字表示两处问题已修复，并在随后全量中通过。
- 性能工具新增真实鉴权非流式 chat 负载，逐批核对钱包、令牌、渠道、日志金额/用量、未完成结算和实际 provider 请求总数。7 项性能工具回归通过。普通 Go/Rust listener 的少量真实 HTTP 冒烟已通过：响应、640 quota/请求与全部资金日志一致，两端各记录 29 次请求日志。该冒烟使用 Rust 调试构建，不作为性能结论；优化版基准另行记录。

证据目录为 `$HOME/.cache/lmm-rust-parity-20260924`，本段具体日志包括
`http-tests-batch2.log`、`batch2-live-extended.log`、`batch2-live-extended/results.json`
和 `performance-checks-batch2-extended.log`。缺口仍在继续收敛，尚未达到完整对齐。


第二批随后完成的整体验证：

- Rust workspace/all-targets/all-features：**1733 passed、0 failed、181 ignored**，98 个测试 binary 完成。
- 全量依赖执行器：193 项中 192 项首次通过；唯一失败为旧 embedding fixture 将 Stripe 的显示金额误写为 quota。实际当前 Go/PostgreSQL 确认原数据应有 0.90 折扣；将数据修为 $100 / 50,000,000 credited quota 后保留 0.97 精确断言，定向复测通过。运行逻辑未为该测试改变。
- 从当前 Go 现场导出后运行新 CI suites，再加上述 embedding 复测：**68 passed、0 failed**。包括 ePay15 输入、Stripe 钱包/订阅事件及两阶段回执失败、AI 价格/URL、token price 六种上下文、缓存竞态和 28 个 Go 资金场景；Go 导出与 Rust 用例都强制执行计数，拒绝跳过或零测试。
- 当前 Go `go test ./...`：62 个有测试包通过、44 个包没有测试；新增订阅 checkout oracle 后，controller 包再次通过。
- 该批结束时的审计为当前 Go 704 路由、Rust 源声明609/缺95、正常挂载台账518、占位26。Stripe 钱包下单与共享回调已从占位转为正常实现；订阅 checkout、其他支付 provider、完整协议与剩余模块仍未完成。此处是源与挂载清单，不能解释为全部行为已验证。

完整日志：`batch2-all-tests.log`、`batch2-all-dependencies/results.json`、
`batch2-ci-runtime.log`、`go-full-batch2.log`、`go-controller-final-batch2.log`。
尚待后续批次的已知细节包括：免费模型预扣开关、跨重试的完整价格冻结、embedding 最小预扣、
API key 管理对 OAuth/assistant runtime key 的保护、Stripe SDK 重试，以及阶梯表达式和缺失的业务模块。


优化构建现已完成，并通过正式基准：54 万次公开读取、9000 次测量转发；含预热和首次请求，
9602 次实际 provider 调用全部通过响应和账本核验。每端记录 283805 次访问日志，已统一每端单日志输出，
并将 Go 数据库连接池上限/寿命配置为与 pinned SQLx 默认相同的 10 / 1800s。
完整数据、三轮范围、CPU、冷启动限制与源码/二进制校验信息见
[第二批优化构建对比](benchmarks/rust-go-release-2026-09-24-batch2.md)。

空闲 RSS 为 Go 88.99 MiB / Rust 29.36 MiB，转发采样峰值为 Go 120.43 MiB / Rust 80.94 MiB。
Go 的 about/notice 吞吐更高；本轮单账户计费转发 Rust 中位数较高，Go 波动明显，不能据此断言所有业务更快。
`release-validation-batch2.log` 记录最终格式、Clippy、受机械修改影响的 8 项定向测试和 release 构建通过。
当时列出的后续事项包括订阅 checkout、当前 Go 选价时点/最小预扣、系统管理 API key 保护与 acquisition；目标仍未完成。

## 2026-09-27 静态审计补记

本次工作区执行 `check-go-route-manifest.sh`：当前 Go 704 条、Rust `implemented` 台账
520 条、fail-closed shell 26 条。`audit-current-routes.py` 报告普通监听器挂载台账
518 条、缺 186 条。该工具的工作区源码扫描包含本次提交范围外的获客草稿，不能用其
源码候选数字描述最终提交；上文 609 条候选声明、缺 95 条是此前批次结果，待干净工作树复核。
22 条获客路由仍全部没有普通挂载，获客草稿不在本次提交范围。本次补记只核对静态清单，
不代表新增业务测试通过、
生产部署或行为对齐。
