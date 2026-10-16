# 历史余额 opening 导入

当前仅支持**经独立审核、全用户预留为零的期初余额**，写入一个全新、离线、由安装 owner 控制的目标库。
入口是 [财务脚本](../scripts/migrate-legacy-financial.py)，具体约束在
[opening 模块](../scripts/legacy_financial_opening.py)。普通 `--apply` 仍明确拒绝；其他金融领域仍由 planner 保留和分类。

当前真实 06 批次仍为 `BLOCKED`、`target_writes=0`：没有可用的完整零预留审阅材料和已审核 opening 输入，旧工具指纹也不能直接作为当前授权。
隔离合成库的有限验收不代表真实历史资金、历史 MFA、生产迁移或[完整仓库验收](architecture-completion-prompts.md)完成。

可复现的合成验收入口是 [CLI 验收脚本](../scripts/test_legacy_financial_opening_cli.py)。
它只创建新的本机 PostgreSQL 集群与合成用户，接受明确的冻结源码和对应 Core 二进制指纹，
覆盖预览、审核材料、整批写入、严格重放，以及真实 COMMIT 成功后丢弃 psql 确认的原 attempt 对账。
验收只适用于记录中的源码、schema 和二进制；后续 Pi schema 的签字不能复用旧目标或旧 approval。

## 先定位待处理的经济权利

`--source-rights-preview` 只读取已经核验的冻结导出和身份映射，列出钱包、债务、affiliate、订阅、Key 额度、悬赏托管等来源事实，帮助逐项审核。它不查询来源或目标数据库，不生成 opening payload、approval 或零预留证明。

该模式要求 `--reuse-export-proof`；不能同时选择三个 opening 模式，也不能传目标 receipt、预留审核或 opening approval/attempt。其他共用来源参数与 planner 一致，输出仍须为 `~/Backups` 下一个新的私有目录。凭据和用户事实不放进 argv 或仓库。

详细结果保存在 `source-rights-reconciliation.private.json`，manifest 绑定它的实际文件 hash 和类型 hash，以及当前工具源码。汇总按表、字段、观察状态分别统计，不把重叠来源或不同币种相加。未知单位、缺失关联、未映射用户和未投影的表继续保留为待审核。核对范围仅限这份选定导出，来源 catalog 中未导出的表也不因此获得放行；`reserved_zero_proven=false`、`financial_rows_migrated=0` 是正常结果，命令成功不代表经济审核或迁移成功。

非零托管金额必须单独保留其归属、原始单位和后续释放/退款权利；不能加入钱包余额、再扣一次钱包，或因为将其排除在 opening writer 之外就视为已处理。来源行定位、状态名称或代码版本本身都不能证明历史部署执行过某种扣款规则。

源码定位入口：`markref context migration.financial-rights --max-bytes 8000`。同一标签串联 CLI、离线投影和测试，不能代替来源审核。

## 支持范围

单位固定为整数 `credit_500k_usd`，`1 USD = 500000 credits`。
来源 `users.quota` 的正值进入 wallet，负值保留为 debt；`users.aff_quota` 独立进入 affiliate，不转成可消费余额。
不再扣一次历史 used，也不通过重放支付回调或伪造模型消费补余额。

预留证据必须覆盖冻结映射中的每个用户，以及冻结财务导出中的每张选定表。非零 reserved、pending transfer、未知权利或缺失覆盖均拒绝整批。
没有 pending 分类不能证明 reserved 为零；金额为零也不能代替审核。当前 opening 不恢复原 reservation 的 capture/release 操作权利。

历史计数、Key 额度、订阅、旧支付退款基准、escrow 和在途权利不在这个 writer 的迁移范围内。
opening journal 不授权历史退款，也不激活原来停用的身份。空 opening 批次暂不支持。

## 三个入口与参数

三个 opening 模式互斥。不选择它们时，脚本沿用财务 planner 流程。
参数名称可用只读帮助核对：

```sh
python3 -B scripts/migrate-legacy-financial.py --help
```

| 模式 | 行为 | 额外要求 |
| --- | --- | --- |
| `--opening-preview` | 重新核对冻结来源和目标，生成待审核的 opening candidate；目标写入为零 | 下表中的全部共用输入 |
| `--apply-opening` | 只执行与重新计算结果完全一致、已单独审核的 opening 批次 | `--opening-approval` |
| `--reconcile-opening` | 只确认原不确定 attempt 的结果，不重试导入 | `--opening-approval`、`--opening-attempt` |

所有 opening 模式都需要以下参数；文件参数只能指向已准备、受保护的本地材料。
这里列参数而不提供自动生成 approval 或初始化目标库的命令。

| 参数 | 必须指向或表达的内容 |
| --- | --- |
| `--source-receipt` | 冻结来源 receipt，绑定原始 archive、导出和元数据 |
| `--identity-map` | 完整、已审核的 source user 到原生 user/personal account 映射 |
| `--identity-manifest` | 与同一来源、同一映射文件绑定的 identity-only manifest |
| `--reuse-export-proof` | 原冻结财务导出的 proof；opening 不重新查询来源库 |
| `--target-receipt` | 资源 owner 为这一个新离线目标准备的身份、合同、前态和独占 lease receipt |
| `--reservation-evidence` | 全表、全用户的真实预留核对证据文件 |
| `--reservation-review` | 独立审核记录，绑定上述证据的实际文件字节、来源、映射与排除范围 |
| `--migration-id` | 本 opening 批次固定 ID，必须与其引用的 identity import ID 不同 |
| `--output` | `~/Backups` 下一个尚不存在的新输出目录，权限为 `0700`；每次预览、执行或对账各用新目录 |
| `--source-timezone` | 可选：有证据的历史应用 IANA 时区；不得从数据库时区推断，冻结后须保持一致 |

`--opening-approval` 是审核完成后单独保留的 approval 文件。
`--opening-attempt` 必须指向**原执行输出中的** `opening-attempt.private.json`，不能指向新造的 attempt。
文件内容、凭据和用户数据不要放进命令参数、Git 或公开日志；连接凭据由私有 target receipt 引用，runtime 不获得导入凭据。

## 审核后才能执行

预览会生成 `opening-approval-candidate.private.json` 和 `opening-payloads.private.json`，状态为 `opening-preview-review-required`。
生成 candidate 不等于审核通过。审核至少要确认：

1. 原 archive、导出字节、metadata、行数基线和原始用户行 hash 匹配；身份映射完整且一一对应。
2. 零预留证据确实来自同一 source/mapping，逐表原始行 hash 和逐用户覆盖完整；独立 reviewer 已审查经济权利，而不只是填写 hash 或状态字符串。
3. 每个 payload 的原始行、账号、整数金额和观察时间均绑定；观察时间只是冻结 dump 的捕获起点，不是历史支付或结算时间。
4. 当前 planner、identity helper、opening writer、全部 Core schema 和实际 catalog 指纹一致；目标的原 identity receipt 与原生用户映射实际读回一致。
5. 目标确为新离线库，只有初始化 seeds 和已核实的 identity-only 导入；未运行 app/worker，没有其他业务材料。

当前 identity helper 要求 schema v4 和完整的 13 组件集合，初始化合同为
`ledger-zero-events-empty-routing-unset-abuse-default-oauth-contract-v3`。除零账本、空事件容量和未选择路由外，
只允许 `core_abuse.settings` 的唯一默认记录：`revision=0`、`document={"version":1,"policies":[]}`。
另外核验初始化生成的唯一 `core_oauth.contract` 记录，包括版本、DDL 指纹和实际 OAuth catalog 指纹。
缺失、修改或新增的业务记录均拒绝；身份导入还会读回确认这些默认记录未改变。
旧 v1/v2 seed receipt 不适用于当前目标，不能只改 receipt 中的版本字符串来复用授权。

审核保留的 approval 必须与执行时重新计算的对象逐类型一致；`False` 不等于整数 `0`，`4.0` 也不能代替版本整数 `4`。
来源、映射、工具、目标或审核材料变化后，不得复用旧 approval。

目标使用受保护的本地 Unix socket，绑定 cluster system identifier、数据库 OID、启动时间、nonce、版本和 catalog。
实际数据库/schema owner 必须为 NOLOGIN，连接它的 LOGIN 角色单独配置并显式切换；runtime 的危险继承、SET/admin 路径、直接账本/receipt/序列权限均拒绝。
资源 owner 提供稳定的目标独占 lease；不同输出目录不能各造一个锁代替它。
执行期间必须持续保持离线独占：advisory lock 和一次无其他客户端检查不能排除不遵守该约定的 owner、DDL 或直接取号操作。

## 原子写入、重放与序列

全部用户在同一个 SERIALIZABLE、持久提交事务中调用 Core 的 owner-only opening 函数，完成完整读回后写一个整批 receipt。
同批 ID、同一审核材料的严格 replay 返回 `opening-replay-noop`，不追加 opening、不改变表或任何序列。
同 ID 改材料、换 ID 重导、映射冲突或向已封存批次追加账户均拒绝；整批冲突在任何 opening/取号前检查。

成功导入只允许两个账本 identity sequences 按预期取号，无关表和序列保持原状。
中途 SQL 失败回滚金融行，但已取走的账本 ID 可能留下空号；这不等于金融数据部分提交。
writer 记录触达序列的影响，不使用 `RESTART` 或 `setval` 抹平空号，也不宣称普通回滚能恢复所有序列。

## UNKNOWN：查原 attempt，不重试

超时、连接中断或 COMMIT 确认丢失后，`opening-outcome-unknown` 表示执行结果未确认，不能理解为“没有写入”。
不得换 ID 重导、修改原材料、重新执行 `--apply-opening`，或因为缺少 receipt 就断言已经回滚。
保留原输出目录中的以下材料，并用新输出目录运行 `--reconcile-opening`：

- 原 `opening-attempt.private.json`、approval 与全部冻结输入。
- 原 `opening-import.private.sql`、`opening-session-prefix.private.sql` 及其 hash。
- 原 `opening-backend.private.json`：同一执行连接的真实 PID、backend start、数据库 OID 和 system ID。
- 原 stdout/stderr、前态与其他故障证据；不得手改 metadata 的时间或身份。

writer 在同一 psql 连接中临时 `SET ROLE NONE`，以实际 LOGIN 读取自身真实 backend start，再恢复已审核的 NOLOGIN owner 后执行业务事务。
这解决 owner 角色下 stats 字段隐藏的问题，不授予监控角色，也不补造时间。

对账先验证原 attempt、SQL 字节和完整 backend metadata。
同一个对账连接先取得 session advisory fence，再建立新的只读事务快照，并确认原 PID 已不在；PID 仍存在或复用不明时继续拒绝判定。
不能在取得 fence 前建立旧快照，也不能释放 fence 后换连接读取结果。

| 对账结果 | 含义与后续 |
| --- | --- |
| `opening-confirmed-committed` | 整批 receipt、完整账户/账本读回和所有序列 seal 均匹配；对账不再写入资金 |
| `opening-confirmed-business-rollback` | 原业务前态和无关序列匹配，触达账本序列的合法前移单独记录；`new_reviewed_sequence_envelope_required=true` |
| 仍为 UNKNOWN / 拒绝判定 | 原 backend 未结束、metadata 缺失、材料/读回冲突或实例身份不符；保留证据并继续人工核对 |

确认业务回滚后，旧 sequence envelope 也不能直接重用；若要另一次执行，必须保留原经济 payload 和 ID，重新测量并审核执行前态。
所有路径均无自动重试。重启导致 boot 身份变化时，需资源 owner 审核恢复证据链，不能把重启当成回滚证明。
