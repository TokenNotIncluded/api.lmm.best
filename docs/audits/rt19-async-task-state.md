# RT-19：异步任务状态与资金隔离

状态：**执行受阻，未通过业务验收。**

- 基线：`2f4164978cf27f6e0e605b0589fbe631aef2c441`。
- 分支：`test/rt-19-async-task-state`；关联审计：#711。
- 本提交只有测试与审计说明，没有生产逻辑修复。测试必须先在完整检出中运行。
- 没有访问生产、真实供应商或支付系统，没有发出真实通知、远程 CI、发布、部署或合并。

## 实际执行结果

已通过连接器读取指定提交的生产源码及现有测试。已编写 9 个带 `rt19` 标签的 Go 测试，执行 `gofmt` 与 Go 标准库语法解析。

**没有完成项目编译、类型检查或任何业务测试执行。9 是已编写的测试函数数，不是通过数。**

执行环境只有 `go1.23.2 linux/amd64`；项目 `apps/api-go/go.mod` 要求 `go 1.25.1`。实际执行 `GOTOOLCHAIN=go1.25.1 go version` 时，工具链下载因 DNS/网络不可用失败。`git ls-remote` 同样无法解析 GitHub 域名。本机无预置项目依赖、PostgreSQL 或 Redis 服务端。

不能把源码推断、语法解析或现有测试文件当作本轮数据库验收结果。

## 已读取的生产路径

Kling 视频路径：

`RelayTaskSubmit` → Kling `TaskAdaptor.DoRequest/DoResponse` → 任务记录 → `RunTaskPollingOnce` → `updateVideoSingleTask` → Kling `FetchTask/ParseTaskResult` → 状态条件更新 → 失败退款或成功差额结算。

读取文件：

- `apps/api-go/relay/relay_task.go`
- `apps/api-go/relay/channel/task/kling/adaptor.go`
- `apps/api-go/service/task_polling.go`
- `apps/api-go/service/task_billing.go`
- `apps/api-go/model/task.go`
- `apps/api-go/model/log.go`
- `apps/api-go/controller/task.go`
- `apps/api-go/service/task_billing_test.go`
- `apps/api-go/service/task_polling_test.go`

测试使用真实 Kling 适配器、生产轮询与计费函数。只替换外部 HTTP 服务。任务从“已接单且已预扣”的数据库记录开始，**没有覆盖提交入口的预扣、重试与接单持久化**。

## 状态规则与源码线索

生产状态包括 `NOT_START / SUBMITTED / QUEUED / IN_PROGRESS / SUCCESS / FAILURE / UNKNOWN`。Kling 适配器把 `submitted / processing / succeed / failed` 映射到其中四种状态，并拒绝未知状态。

轮询终态保存使用旧状态条件更新。成功后执行差额结算；失败后执行持久化退款。失败退款已有 `PENDING / COMPLETED` 标记和事务；本轮没有证明它在真实并发数据库中通过。

测试要求：未知状态保留可恢复任务及原预扣；失败退款获胜后，在途成功不能覆盖终态并交付免费结果；成功差额结算重放只能产生一次实际资金变化。任务公开 ID、上游 ID、渠道、用户和原资金来源必须保持绑定。

以下是待复现线索，**不是本轮已确认的线上漏洞**：

1. **跨渠道同名上游 ID。** `RunTaskPollingOnce` 在一个平台内使用 `taskM[upstreamID] = task`，没有把渠道加入索引。后续各渠道查询同一个映射。两个渠道使用相同 ID 时会覆盖其中一个记录；是否造成持久化串写由测试验证。
2. **返回的上游 ID 没有绑定检查。** `updateVideoSingleTask` 解析 `taskResult.TaskID` 后，未在状态更新前与请求任务 ID 比较。测试让本机上游返回另一个 ID 和独立结果地址，要求拒绝保存。
3. **差额结算分步更新。** `RecalculateTaskQuota` 从调用者快照计算差额，依次调整资金、Key、任务额度、统计和日志。没有在同一个事务里锁定任务并读取当前已结算额度。旧快照重放与任务额度写入失败需要数据库验证。
4. **成功终态与结算之间的恢复缺口。** 成功状态先保存，差额资金后处理。已读取的恢复扫描处理失败退款，未发现对应成功结算的待处理标记与扫描。必须通过实际终止进程与重启验证，不能用普通返回错误替代。

用户查询的 `GetByTaskId` / `GetByTaskIds` 在 SQL 中包含用户条件。这是已有防护，不代表 HTTP 认证、结果代理与下载权限已通过。

## 已编写的测试

| 测试 | 检查内容 | 执行结果 |
| --- | --- | --- |
| `TestRT19KlingChannelScopedExternalID` | 两个账号、渠道与本机上游使用同一上游 ID；检查结果与资金归属 | 未执行 |
| `TestRT19KlingRejectsWrongReturnedTaskID` | 查询一个 ID，却收到另一个任务的成功与结果 | 未执行 |
| `TestRT19KlingUnknownStatusCanRecover` | 未知状态保留预扣，下一轮成功可恢复 | 未执行 |
| `TestRT19KlingFailureWinsAgainstInFlightSuccess` | 两个真实轮询请求重叠，失败退款先完成，迟到成功不得覆盖 | 未执行 |
| `TestRT19SettlementReplayUsesPersistedAmount` | 两份独立旧快照重复结算同一任务 | 未执行 |
| `TestRT19SettlementConcurrentStaleSnapshots` | 两个 goroutine 同时使用旧快照结算 | 未执行 |
| `TestRT19SettlementTaskWriteFailureCannotMoveFunds` | 注入任务额度保存错误后重试；检查资金、Key、任务、统计和日志 | 未执行 |
| `TestRT19DeletedKeySubscriptionRefundKeepsOriginalPayer` | Key 删除后重复退款仍回原订阅，不转现金、不恢复 Key | 未执行 |
| `TestRT19TaskLookupRequiresOwner` | 跨账号替换公开 ID / 上游 ID / 批量 ID | 未执行 |

测试为每个用例创建独立临时 SQLite 文件，禁用 Redis、渠道内存缓存、批量写入及用量导出。Kling HTTP 客户端只能访问该用例登记的数字回环地址和端口；代理环境清空，重定向拒绝。请求逐次记录在测试日志中，不保存真实凭据。

SQLite 连接数设为 1，目的是稳定重现旧快照和跨步骤问题；**不能据此证明 PostgreSQL 锁、Redis 失效传播或多实例行为正确**。金额使用人工构造的整数额度，不是实际美元损失估计。

## 未完成项

- 接单后提交响应丢失；重试/重启时复用原操作及上游标识；防止重复外部生成。
- 实际回调入口、签名、重复通知、轮询与回调共同结算。现有重叠测试是轮询对轮询，不能替代回调验收。
- 实际取消入口与完成竞争。
- 资金、状态、提交、响应边界的进程终止和重启。本提交的数据库写入错误不是 SIGKILL 测试。
- HTTP 认证、中间件、结果代理、结果下载和真实文件字节。模型查询测试不能替代下载验收。
- Key 撤销传播、订阅跨周期和额度重置归属。删除 Key 的测试不能替代这些场景。
- 独立 PostgreSQL、Redis、多连接竞争、完整原始消费日志与渠道对账。
- 任何真实供应商行为。

没有给上述项目标记通过或“不适用”。没有为未证实的资金问题提交未经运行验证的生产修复。

## 本机运行

在已具备 Go 1.25.1 和项目依赖的完整检出中，执行：

```sh
cd apps/api-go
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -tags=rt19 ./service -run '^TestRT19' -count=1 -v -timeout=120s
```

需要后续在独立 PostgreSQL / Redis 环境中补齐真实进程重启与完整 HTTP 路径。上面的命令不会触发远程工作流。默认 `go test` 不包含 `rt19` 测试，避免把尚未验证的审计断言混入普通测试结果。
