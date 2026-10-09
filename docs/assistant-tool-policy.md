# 内置 AI 客服工具

内置客服当前注册 67 个工具，按 16 组管理。原有商品和 MCP 目录组只查询目录；新增市场接入组在用户明确授权后可调用远程工具，详见下文。

超级管理员可在「系统设置 → 内置助手 → 工具」搜索工具、开关整组或单个工具，并保存配置。默认继承已有能力和权限；全局开关只能收紧能力，不能让 L0 获得 L1、管理员或超级管理员权限。

## 工具目录

| 组 | 工具 | 能力 |
|---|---|---|
| 服务帮助（5） | `get_service_facts`, `navigate_to_page`, `get_setup_guide`, `search_web`, `calculate_math` | 查询连接地址、活动、客户端配置；提供站内链接；搜索、计算。 |
| 账户与升级（6） | `get_account_access`, `get_user_overview`, `get_user_usage_summary`, `get_usage_summary`, `prepare_user_action`, `grant_l1_access` | 查询账户、余额、进度和用量；准备账户操作表单；服务端验证后升级当前 L0 账户。 |
| 模型与费用（4） | `get_available_models`, `get_model_pricing`, `calculate_cost`, `get_plan_offers` | 查询真实模型、价格和套餐；按已知价格估算费用。 |
| API 密钥（3） | `request_create_key`, `list_my_api_keys`, `prepare_api_key_action` | 查询本人密钥元数据，准备创建、停用或删除一个精确密钥的确认卡。密钥内容不交给模型。 |
| 奖励（5） | `get_invitation_rewards`, `get_new_user_gift_status`, `prepare_new_user_gift`, `get_weekly_discount_status`, `prepare_weekly_discount` | 查询邀请奖励、新人礼及每周折扣；评估奖励资格并记录一次性决策，领取仍需用户确认。 |
| 开源悬赏（2） | `get_bounty_guide`, `get_bounty_data` | 查询流程、公开悬赏及有权限访问的个人或管理数据；不出资、结算或转账。 |
| 商品与工具目录（4） | `get_store_products`, `get_store_product`, `get_tool_market_services`, `get_tool_market_service` | 查询商品和 MCP 服务目录、详情及要求。 |
| 绘图（1） | `prepare_image_generation` | 准备使用可用绘图模型的确认卡；用户确认后的生成可能扣费。 |
| 人工支持（3） | `get_human_support_status`, `book_technical_support`, `request_human_support` | 查询资格和请求；用户明确预约后提交站内预约；准备人工转交或账户停用审核申请。 |
| 记忆与个性化（5） | `set_conversation_title`, `recall_memory`, `remember_memory`, `remember_profile_skill`, `forget_profile_skill` | 设置标题，查询、保存本人记忆和回答偏好；按用户明确要求移除 AI 生成的偏好。 |
| 注册保护（4） | `get_registration_risk`, `notify_registration_risk`, `end_registration_conversation`, `ban_l0_user` | 检查当前 L0 的服务器证据；依据确定性校验记录警报、暂停验证或封禁当前 L0。 |
| 管理读取（7） | `get_admin_user_skills`, `get_admin_server_config`, `get_admin_channels`, `get_admin_model_inventory`, `list_admin_operations`, `execute_admin_operation`, `audit_admin_model_pricing` | 有权限的管理员查询用户偏好、配置、渠道、模型和定价；发现控制台操作并执行经过审查的只读操作。 |
| 管理变更（5） | `prepare_admin_user_skill_change`, `prepare_admin_config_change`, `prepare_admin_channel_change`, `prepare_admin_model_sync`, `prepare_admin_pricing_change` | 准备精确变更预览，管理员在界面确认后才应用。配置、价格和模型同步要求超级管理员。 |

39 个工具只读或展示数据，15 个工具准备确认表单，12 个工具会在服务端规则允许时保存记录、调用远程工具或改变状态，1 个工具生成站内链接。特别是名称带 `prepare` 的新人礼和每周折扣会保存决策、消耗相应机会，并非纯预览。预约、记忆、标题、L1 升级和注册保护也有实际写入，因此单独标为「服务端校验写入」。

## 开关规则

持久化设置为 `AssistantToolPolicy`，结构版本为 1：

```json
{"version":1,"groups":{"drawing":false},"tools":{"search_web":false}}
```

- 没有配置的组和工具继承开启状态，实际可用性仍取决于账号、功能及业务条件。
- 组关闭时，组内所有工具关闭，单项 `true` 无法覆盖。重新开启组会恢复各单项此前的状态。
- 多个管理页面的草稿按每个组和工具与原始基线比较后合并，远端独立修改会保留；同一开关发生冲突时优先保留关闭状态，并提示复核。保存前读取最新配置，再携带 `expected_values.AssistantToolPolicy` 比较基线；保存期间另一端又改了策略，服务器返回 `409 ASSISTANT_TOOL_POLICY_CONFLICT`，整批设置均不写入，草稿保留供重新复核，不自动重试保存。
- 关闭 `grant_l1_access` 会停止助手的自动 L1 升级；关闭注册检查不会取消服务端注册证据要求。
- 关闭后，工具会从模型目录移除，强制工具选择不会选择它，模型即使伪造调用也会被服务器拒绝。
- 回答缓存前、每次工具执行和旧确认卡执行前读取数据库的权威策略，另一台后端关闭工具不必等待后台设置同步。数据库不可读或策略非法时拒绝执行。已经开始并提交的业务操作不会因为随后关掉开关而撤销。
- 回答缓存包含规范化后的策略，关闭工具后不会命中旧策略的回答。
- 已生成的助手确认卡在执行前重新检查：密钥、昵称、绘图、人工转交、账户停用申请、奖励领取和管理应用均受此限制。禁用的管理员预览不会被消费。
- 普通账户页面和控制台原有业务接口继续独立检查自身权限；此设置管理的是助手的能力，不是整个站点功能开关。

通用管理读取会检查别名依赖，不能通过 `execute_admin_operation` 读取已关闭的服务器配置、渠道、模型目录或用户个性化工具。关闭 `get_admin_user_skills` 后，用户详情、列表和搜索等混合读取仍可返回基本账户数据，但其结果中的 `assistant_profile` 会被递归移除；普通管理员页面的查询保留原有可见性和权限。通用工具依然拒绝写操作。助手不能修改 `AssistantToolPolicy` 来重新开启自己，只有超级管理员通过设置界面配置。

未知组、未知工具、非布尔值、重复字段、`null`、不支持的版本及超过 16 KiB 的策略均被拒绝，错误配置不会悄悄恢复全部开启。空旧配置继承默认值。根账户会话可从 `GET /api/assistant/admin/tool-catalog` 读取不含秘密的完整定义。

## 本地验证

```sh
cd apps/api-go
go test ./setting ./controller -run TestAssistantToolPolicy -count=1
```

测试包括实际注册目录匹配、67 个禁用模拟调用、权限和组继承、目录缓存失效、强制选择、无工具时的模型请求、旧确认卡和管理读取别名。前端测试模拟工具目录接口及开关、搜索、保存、配置错误和重试，没有调用真实账务、删除、预约或发送消息工具。

## 工具配置中心

工具页现在同时管理开关与配置。每行的「配置」打开独立弹窗。搜索服务商、搜索地址与凭证、新人礼包金额上限从旧分区迁入对应工具，不复制设置键，也不改变已保存的值。弹窗修改只是草稿；点击「保存助手设置」后才生效。

`AssistantToolPolicy` 仍兼容 version 1，新增可选的 `rules`。每项等级范围包含上下限，只能收紧原有业务权限。默认用量汇总为等级 ≥ L1；管理员是 L5（内部 role=10），超级管理员是 L6（内部 role=100），没有改变原有数据库角色值。低等级不能通过配置得到管理员能力。

```json
{"version":1,"groups":{},"tools":{},"rules":{"get_usage_summary":{"min_level":1,"max_level":6},"prepare_weekly_discount":{"min_level":0,"max_level":6,"discount_percent_by_level":{"0":10,"1":10,"2":10,"3":10,"4":10,"5":0,"6":0}},"create_site_issue":{"min_level":0,"max_level":6,"default_visibility":"user"},"call_market_tool":{"min_level":1,"max_level":6,"market_service_ids":[]}}}
```

折扣值 10 表示减免 10%，不是支付 10%。普通用户保留原有 10% 上限，管理员不领取对话奖励。金额和等级会在决定、实际执行或领取时检查；已领取的折扣码不因后来修改上限而被撤销。新人礼继续使用 `AssistantNewUserGiftMaxCredits` 与现有货币换算；未设置时保留旧规则，不把历史计价单位改称人民币或美元。

多个管理员编辑同一规则时保留更严格的交集，并要求复核；无法同时满足的等级范围不会被自动放宽。仍使用现有版本比较，409 冲突不自动重试。模型不能调用工具开启自身权限。

## 新增工具

共新增 13 个工具，目录合计 67 个。新增「原生可视化」「站内改进 issue」「工具市场接入」三组。

| 用途 | 工具 | 实际行为 |
| --- | --- | --- |
| 个性化 | `get_overview_greeting`, `set_overview_greeting` | 读取本人问候语；修改需浏览器确认。 |
| 站内改进 | `get_site_issues`, `create_site_issue`, `update_site_issue` | 读取本人可见 issue；创建需确认；状态、可见范围和备注更新限 L5/L6，且需确认。 |
| 邀请 | `send_invitation` | 使用当前用户的推荐链接准备单封邮件，确认后复用现有发送模板与限流。 |
| 市场接入 | `get_connected_market_tools`, `connect_market_tool`, `call_market_tool` | 查询允许接入的远程服务，确认精确工具版本的授权，再通过已有市场调用和计费流程执行。 |
| 原生展示 | `show_chart`, `show_statistics`, `show_choices`, `show_flowchart` | 展示受限结构化数据，不执行模型生成的 HTML、JavaScript 或任意 SVG。 |

市场服务默认不接入。管理员在配置弹窗点击接入并保存后，助手才能发现相应服务；这不等于用户付费授权。用户确认卡默认只允许一个免费调用、十分钟有效期，付费额度需用户自己填写。调用仍检查精确版本、数据归属、价格、余额、次数、到期时间与授权。管理员移除服务后，旧授权也不能继续预留费用或启动执行。内置绘图服务不经过这个远程工具通道。

issue 类型包括 bug、security、experience、feature。用户可见指「提交者与管理员」，不是向所有用户公开。安全漏洞始终只对管理员可见。每条状态记录保留创建时的可见范围；后来公开 issue 不会公开旧内部备注。记录更新使用 revision 比较。没有自动向外部 GitHub 发布漏洞或 issue。

## 概览与聊天展示

概览顶部使用大引号显示问候语。简体中文默认 `HI,$name,现在是$time`。支持 `$name`、`$time`、`$date`、`$weekday`、`$level`、`$site`、`$balance`，`$$` 表示字面美元符号。时间使用浏览器时区；每种语言有自己的模板，留空恢复默认，最多 512 个字符。普通页面与助手修改使用同一个版本检查，不覆盖其他用户偏好。

概览图表读取本人实际用量，提供 7/30 天视图以及请求、token、费用指标。加载失败与无数据分别显示，不使用随机数或零值掩盖错误。折线、柱状、环形图附有可展开数据表。统计卡使用固定图标列表；流程图有文字步骤；选择按钮只填入输入框，不自动发送消息或确认付费操作。聊天中助手自行提供的数据有来源提示，不当作服务端验证结果。

新功能包括 Go 数据表迁移，需要配套部署 Go 和 Web。Rust 预览后端尚未实现这些新增接口，不能仅替换前端就认为两个后端均已支持。本分支没有修改生产配置、余额、发送真实邀请或部署。
