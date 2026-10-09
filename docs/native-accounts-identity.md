# 原生团队：身份存储与接口试验版

本批接续 PR #656 的账号范围设计。基线为 `aba33ebf`。
这是实际存储和 HTTP 接入的第一批代码，不是完整团队系统，也未部署。
此前 [原生账号设计](native-accounts.md) 中的“本批”指规则基础提交；
本文记录后续的实现状态。

## 已写入的流程

使用现有 Go 数据库和浏览器会话，提供团队创建、个人与团队账号列表、团队详情、
成员列表、站内 ID 邀请、已发邀请列表、接受/拒绝/撤销邀请、变更成员身份、移除成员与主动退出。
账号切换不改写登录用户；列表仅是账户选择数据，不是新的登录凭证。

本批只接入站内用户 ID 邀请。邮箱匹配、站外邀请、handle、公开主页、卡片、
前端账号切换、团队等级事实加载、钱包、订阅、预算和团队 Key 尚未接入。
`team_billing_available` 始终为 false；不允许据此宣称团队可以实际消费。

## 数据与权限

`accounts` 使用 `(kind,id)` 共同表示个人或团队，复用 `pkg/account.Ref`。
个人 ID 保留原 user ID；团队使用独立的正整数 ID，保持在浏览器可精确表示的范围。
`(kind,owner_user_id)` 唯一，保证每位用户最多持有一个团队。

创始人/拥有者只保存在 `accounts.owner_user_id`，不再另外保存一行 owner 成员。
`account_members` 仅保存 admin/member，移除时改为 inactive 并递增版本。
对外仍是创始人、团队管理员、成员三个身份。拥有者移交尚无入口；未来必须在
同一事务中维护唯一拥有者并增加 `ownership_version`，使旧邀请不能恢复效力。

`account_invitations` 绑定团队、邀请人、接收人、角色、邀请人的认证与成员版本。
邀请有效期目前是 7 天，团队最多同时保留 100 个未到期邀请。邀请 ID 本身不是
可转交的加入凭证：必须由指定接收人的有效会话接受。
接受时重新检查邀请人的当前权限、版本和有效期。成员降级/移除会撤销相关待处理邀请。
重复接受不会重新创建成员；已经接受的旧邀请不能恢复被移除的成员。

接收人可以拒绝邀请；拒绝和发送者撤销共用 `revoked` 终态，由操作记录中的
`invite.decline` / `invite.revoke` 区别来源，不增加另一套状态机。重复拒绝不重复写事件，
拒绝已接受邀请不会移除成员。并发接受与拒绝由同一团队锁和事务决定唯一结果。

邀请列表增加 `team_display_name`，接收前可以辨认团队，且不泄露隐藏字段。
创始人能查看该团队全部已发邀请；管理员只能查看自己发送的普通成员邀请。
普通成员和未加入的全站管理员不能查看此管理列表。冻结团队不再出现在待接受列表中。
两个列表都按邀请 ID 分页，每页最多 50 条，下一页用末项 ID。

`account_events` 与成员变更在同一事务写入，记录团队、操作人、目标、动作和角色，
不保存凭证、邮箱或请求正文。审计写入失败时，业务变更也回滚。

团队内的变更按照“团队行 → 排序后的相关用户 → 当前会话 → 成员/邀请”的顺序加锁。
团队创建锁定当前用户和会话，由数据库唯一约束保证并发不能创建两个团队。
创建与发送邀请支持 `request_key`，重复键不能改变原请求的关键内容。

自然个人 L0 可以创建团队和接受邀请，不更新个人等级、激活时间、余额或奖励。
停用、明确的 L0/非法等级覆盖、失效会话、认证版本变化会被事务中的新检查阻止。
全站管理员和超级管理员不自动获得团队成员关系或团队权限。
本批没有改变现有个人 L0–L6 计算，也没有把团队支付事实伪造成创建者的事实。

## 迁移与默认关闭

`NATIVE_ACCOUNTS_ENABLED` 只接受空值、`false` 或 `true`，默认关闭。
HTTP 试验版仅支持 PostgreSQL；SQLite 用于存储单元测试，不是已开放的服务端能力。

新表加入已有 `identityMigrationModels`/`startupMigrationModels` 清单，
供正常迁移和 schema 验证复用。开启时须在现有正常 `migrate --apply`/`--verify`
流程和服务启动使用相同环境变量。HTTP 路由注册只验证表结构，绝不自行执行 DDL。

PostgreSQL 会重写 `IN` 检查的类型表达式，并记录多列约束引用的所有列。
账号模型通过 `postgresCheckCatalogForms` 向现有结构清单声明精确的 PostgreSQL 形式，
同时保留用于迁移的可移植检查。原 DDL 表达式发生变化时，旧声明直接报错。
校验仍逐项比较完整表达式、列和验证状态，不剥除类型或跳过检查。
真实 PostgreSQL 测试同时覆盖正常迁移和被削弱约束的拒绝。
旧的快速迁移路径未扩展这些可选表；没有先完成正常迁移时，路由验证会拒绝启动。

只在显式创建团队的事务中登记该用户的个人账号引用。GET 不插入数据，启动也不
全量复制个人用户。不复制余额、不重算历史成长。全量个人归属回填及资金外键迁移
仍是后续工作；旧版 `/self` 和个人 Key 不改变行为。

## 接口

所有路径位于 `/api/accounts`，使用原有会话认证、来源检查、限速、4 KiB 请求限制
和禁缓存。个人 PAT/API Key 不能代替浏览器会话调用本批接口。请求体拒绝未知字段，
不能提交 owner、资金、消费权限或平台角色。自然 L0 不经旧的个人 ConsoleAccessGate
拦截，但每个模型操作都重新检查用户和会话。

| 方法与路径 | 用途 |
| --- | --- |
| `GET /api/accounts?after=<team_id>` | 个人账号 + 最多 50 个所属团队；返回下一页游标 |
| `POST /api/accounts/teams` | 创建团队；字段 `display_name`、`request_key` |
| `GET /api/accounts/teams/:team_id` | 成员可读的团队身份信息 |
| `GET /api/accounts/teams/:team_id/members?after=<user_id>` | 拥有者 ID + 最多 50 个有效成员 |
| `POST /api/accounts/teams/:team_id/invitations` | 字段 `user_id`、`role`、`request_key` |
| `GET /api/accounts/teams/:team_id/invitations?after=<invitation_id>` | 按管理权限查看已发邀请 |
| `GET /api/accounts/invitations?after=<invitation_id>` | 自己的未到期邀请；下一页用末项 ID |
| `POST /api/accounts/invitations/:invitation_id/accept` | 由指定接收人接受 |
| `POST /api/accounts/invitations/:invitation_id/decline` | 由指定接收人拒绝；重复提交无副作用 |
| `PATCH /api/accounts/teams/:team_id/members/:user_id` | 字段 `role`；不能授予 owner |
| `DELETE /api/accounts/teams/:team_id/members/:user_id` | 向下移除或退出；拥有者不能直接退出 |
| `DELETE /api/accounts/teams/:team_id/invitations/:invitation_id` | 撤销待处理邀请，不移除已加入成员 |

创建示例：

```json
{"display_name":"研发团队","request_key":"create-team-request-0001"}
```

团队管理员只能邀请普通成员，不能授予管理员，不能管理其他管理员或创始人。
角色变更和资金授权仍是不同操作。当前接口不开放任何资金操作。

## 验证与运行边界

原 `8190dbff` 的独立验证已复现两个阻塞：测试容器的单引号健康检查参数无法启动；
正常生成的账号 CHECK 约束与原结构清单不一致。不能把测试步骤的继续执行状态当作通过。
本次修复容器参数与精确的结构声明，并补全以下回归；最终执行结果以 PR 中绑定提交
和运行记录为准，而不是据此文档自动视为通过。

```sh
cd apps/api-go
go test -race -p 2 ./pkg/account ./pkg/accountfunding -count=1
go test -race -p 2 ./model -run '^(TestNativeAccount|TestTeamAccountAccess|TestEvaluateTrustLevel|TestPostgresCatalog)' -count=1 -timeout=5m
go test -race -p 2 ./controller ./router -run '^TestNativeAccount' -count=1 -timeout=5m
```

真实 HTTP 回归使用现有登录会话签发、生产认证中间件、PostgreSQL 和实际团队路由，
不是直接伪造 `gin.Context` 用户 ID。覆盖 L0 创建/加入、收到与已发邀请、拒绝后重邀、
成员退出、跨团队/全站角色隔离、PAT/刷新 Cookie 拒绝、跨站来源及已撤销会话。
所有这些操作都检查个人余额、激活时间不变。

PostgreSQL 用例要求 `TEST_POSTGRES_DSN` 指向专用测试库，并设置
`TEST_POSTGRES_ISOLATED_SCHEMA=1`。测试建立和删除隔离 schema，不能指向生产库。
手动工作流的数据库口令仅属于临时测试容器，不是线上凭证。

现有 `billing-safety.yml` 仍仅手动触发。临时验证分支仅用于固定提交的测试，
不合并其测试载荷或临时工作流到本 PR。没有实际团队扣费、前端或生产配置变更。
全仓 Go/Rust/Web、完整升级/回滚和个人资源归属迁移仍须单独完成；不能用本批
身份流程测试代替尚未实现的预算、钱包、订阅和跨账号计费验证。
