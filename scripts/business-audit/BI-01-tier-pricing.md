# BI-01：长上下文计价修复与验证记录

日期：2026-10-10。状态：**定价包回归通过；数据库扣费验收未完成，不可据此合并或上线。**

## 基线与提交

工作分支：`fix/bi-01-tier-pricing`，从当时的当前 main 创建：
`2f4164978cf27f6e0e605b0589fbe631aef2c441`。提交前复查 main，SHA 未变化。

原审计基线：`565b64e475419e073f55225e64d0718f7bf070b4`。
关联 [审计 PR #711](https://github.com/TokenNotIncluded/api.lmm.best/pull/711)，
原始测试提交 `c288ed6c7e8a05373d2948106011a36d695f4ac2`。
只处理 AUDIT-PRICE-01，不关闭整个审计，不实施 #675。

测试提交：`4a0465f5e271c700ab4bcae9bf8d85b54dea1a97`。
业务修复提交：`2b9c7b5fe8eaa54f532241c86c00ede5fa7c4bcc`。
测试提交单独检出时，已复现的失败断言仍会失败；没有删除或跳过它们。

已检查 [PR #703](https://github.com/TokenNotIncluded/api.lmm.best/pull/703)：
检查时为未合并 Draft，head 为 `d2bb5bf4aac78e09105d097cb758e38ccdc66610`。
其 21 个改动文件不含 `pkg/servicetier/`。本修复没有修改该 PR 涉及的
`common/quota_math.go`、`service/text_quota.go`、支付、账本或其他业务文件。

## 修复与兼容行为

唯一修改的业务源文件是 `apps/api-go/pkg/servicetier/pricing.go`。

价格目录允许各档位独立提供 long 价格。原选择函数却只检查 standard.long，
因此 fast.long 已配置也可能被忽略。现在同一模型任一档位有 long 价格时，
超过阈值就必须使用所选档位的 long 价格；以实际返回档位优先。
该档位缺少 long 价格则返回错误，不静默使用 short。所有档位都没有 long 的
模型继续使用原有固定价格；等于阈值仍使用 short。

这意味着：仅 fast.long 存在时，fast 长上下文可以正确结算；上游实际返回
未配置 long 的 default/ultrafast 时则不能猜价。现有最终结算错误路径会
保留预扣并标记需对账。本次只阅读该调用路径，没有把它的数据库行为标为通过。

预扣还修复了两个已复现的问题：拒绝负输入；缓存读价与输入上限费用分别用
精确数计算后相加，避免两个有限浮点价格相加后溢出并崩溃，也避免引入小数误差。

没有改变折扣、经营倍率、价格快照或 Credits 的最终取整实现。
业务换算保持 1 USD = 500000 整数点，总费用只在最后向上取整一次。

已阅读的调用链：
`setting/service_tier_pricing.go` 的目录验证与读取 →
`relay/helper/service_tier_pricing.go` 的 NewQuote/ReserveUSD/Credits →
`service/billing.go` 的 PreConsumeBilling →
`service/service_tier_pricing.go` 的实际用量换算 →
`service/text_quota.go` 的最终结算和消费记录。
这些调用文件未改写。完整 HTTP 入口、数据库和记录一致性仍待执行验收。

## 实际执行环境与源文件核对

本机：Linux amd64，Go 1.23.2。直接 clone 失败：`Could not resolve host: github.com`。
通过已连接 GitHub 读取指定 SHA 的文件，建立选择性源码工作区，**不是完整仓库检出**。
该定价包只依赖 Go 标准库，因此可在关闭模块和网络下载的情况下直接执行。

初始四个文件均以 `git hash-object` 核对 GitHub blob SHA：

| 文件（相对 pkg/servicetier） | main blob SHA |
| --- | --- |
| pricing.go | dc7bde11584581510e019461cbd37d0dbbd3acb8 |
| pricing_test.go | 64a2e5c44731c4b1c20fdd47baba36f25d11bb26 |
| sync.go | 8bd5524e7f4bfc351dccc947f1f239782afc43fb |
| testdata/official-prices.md | 9b6c83cec6361c5c5da9d22d3321ec47a3128f2e |

提交后读回的文件摘要与本地一致：
pricing.go `7b60d8cb45bf69bc2d4fdbdc7bacbe7eb3cd03a0`；
新增单元测试 `93b6645b3e170d532fad07958932f3151701c2e9`；
数据库测试 `a21f873513a91cb7ef4a4e6d98e6ddfd633ad515`。
原审计测试原样保留，blob `609e62de2d1ae0225da03cd83d0b126f39c37ae7`。

## 准确命令与结果

以下命令均在对应版本的 `apps/api-go/pkg/servicetier` 文件目录执行；
本机最终目录为 `/mnt/data/bi-01/worktree/apps/api-go/pkg/servicetier`。

```bash
export GO111MODULE=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off

# 先运行原有测试；尚未放入审计/新增测试，也尚未修复。
go test -race -count=1 -v .
# 通过，原有 10 个顶层测试；01-existing-before.log。

# 加入 #711 原始测试，仍使用原 main 业务源文件。
go test -race -count=1 -run '^TestBusinessAuditTierLocalLongContextPrice$' -v .
# 失败，退出 1；02-audit-before.log。

go test -race -count=1 -run '^TestBI01' -v .
# 新增断言复现失败，退出 1；03-regressions-before.log。

# 应用业务修复后。
go test -race -count=1 -v .
# 通过；04-all-after.log。完善快照测试后再次全量通过：07-final-after.log。
go test -race -count=10 .
# 通过；05-repeat-after.log。
go vet .
# 通过；06-vet.log（无输出）。

# 最终测试文件 + 原始业务源文件，在 before-final 独立目录再次复核。
go test -race -count=1 -run '^(TestBusinessAuditTierLocalLongContextPrice|TestBI01)' -v .
# 失败，退出 1；09-final-tests-before-fix.log。

# 回到修复后的目录，再单独执行原始审计断言。
go test -race -count=1 -run '^TestBusinessAuditTierLocalLongContextPrice$' -v .
# 通过；10-audit-after.log。
```

原失败原文：

```text
AUDIT-PRICE-01: accepted fast.Long=40 but charged 6.52802400 USD, expected 13.05604800 USD for 272001 input tokens
negative reservation input accepted
finite prices caused a reservation panic: runtime error: invalid memory address or nil pointer dereference
reservation added prices as floats: cost=277500000000000033/62500000000000000000000 error=<nil>
```

修复后，原合成案例为 13.05604800 USD，换算为 6528024 点；原错误结果
6.52802400 USD 对应 3264012 点。**这是合成计价验证，不是已验证的余额差，
更不是线上损失估计。**

新增矩阵覆盖 8 种 long 配置、fast/ultrafast 请求、4 种实际返回情况、
阈值前/等于/之后，共 192 个组合，同时核对预扣与实际价格。还覆盖缓存读写、
缺失价格、24 小时过期边界、未来时间边界、负数、非有限价格、金额上限、
独立快照和最终一次取整。使用独立的十进制计算断言，不复用生产价格选择函数。

额外尝试：

```bash
GOARCH=386 CGO_ENABLED=0 go test -count=1 .
```

失败原因是本机不能执行 386 二进制：`exec format error`，见 08-386-after.log。
这是未完成的架构运行验证，不能计作测试通过。所有新增 Go 文件均通过 gofmt 语法解析。

## 数据库验收：已补测试，但未执行

`apps/api-go/service/bi01_tier_pricing_database_test.go` 使用现有独立 SQLite /
本地 Redis 测试环境及回环地址模拟上游，没有重写钱包。它调用配置验证、
PreConsumeBilling、ObserveServiceTier、serviceTierUsageQuota 和
PostTextConsumeQuotaWithResult，断言预扣、最终余额差、API Key 剩余额度与已用额度、
用户/渠道累计用量、单条消费记录、记录中的价格快照及重复结算不重复扣费。
六个固定金额案例包含原合成案例及实际返回不同档位的缓存读写用量。

本机缺少完整应用源码、Go 1.25.1 和应用依赖；`apps/api-go/go.mod` 明确要求
Go 1.25.1。因此该测试**未编译、未执行**，也没有实际数据库余额或资金流水通过证据。
上述消费记录是现有应用的消费日志，并不等于已经核对全部独立资金账本。
以下是后续本地验收命令，**不是本次已执行命令**：

```bash
cd apps/api-go
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -race -count=1 \
  -run '^TestBI01TierUsageWalletAndConsumeLog$' -timeout=2m ./service
```

运行前需要完整检出此分支、合适的本地 Go 工具链和已缓存依赖，并保持无生产
配置、独立数据库、模拟上游和网络隔离。未验证项还包括完整请求入口、PostgreSQL、
SSE 中断结算、数据库故障回滚、全后端构建以及仓库文档检查。

未执行生产请求、真实扣款、业务通知、远程 CI、发布、部署或合并。
提交带有 `[skip ci]`；没有修改工作流触发条件、调用 workflow_dispatch 或请求评审。
