# 内置助手：工具与配置检查

## 范围

基线为 `970ae0aa66966f03f8522a89d5da8c22d47ec3a6`。检查的是 Go/Web 源码、工具注册、参数定义、统一执行入口、权限筛选、循环控制及配置映射，不是线上数据库中的实际设置。没有读取客户会话、密钥或生产余额，也没有执行真实购买、奖励领取、邀请邮件或管理员变更。

本报告记录按需发现和普通结束功能合入时的检查结果：原目录有 67 个工具，新增后为 69 个、16 组。后续政策工具已将当前目录扩为 72 个、17 组，最新目录见 [工具配置说明](assistant-tool-policy.md)。本报告下列数字仍保留当时的测量口径。下表来自代码注册表。目录与模型定义必须一一对应；每个工具必须具有明确的读写类别和权限说明。禁用检查覆盖每个注册工具，不只覆盖新增工具。

| 组 | 数量 | 工具 |
| --- | ---: | --- |
| Service help | 7 | `discover_tools`, `end_conversation`, `get_service_facts`, `navigate_to_page`, `get_setup_guide`, `search_web`, `calculate_math` |
| Account and access | 6 | `get_account_access`, `get_user_overview`, `get_user_usage_summary`, `get_usage_summary`, `prepare_user_action`, `grant_l1_access` |
| Models and costs | 4 | `get_available_models`, `get_model_pricing`, `calculate_cost`, `get_plan_offers` |
| API keys | 3 | `request_create_key`, `list_my_api_keys`, `prepare_api_key_action` |
| Rewards | 6 | `send_invitation`, `get_invitation_rewards`, `get_new_user_gift_status`, `prepare_new_user_gift`, `get_weekly_discount_status`, `prepare_weekly_discount` |
| Open-source bounties | 2 | `get_bounty_guide`, `get_bounty_data` |
| Products and tool catalogues | 4 | `get_store_products`, `get_store_product`, `get_tool_market_services`, `get_tool_market_service` |
| Image generation | 1 | `prepare_image_generation` |
| Human support | 3 | `get_human_support_status`, `book_technical_support`, `request_human_support` |
| Memory and personalization | 7 | `get_overview_greeting`, `set_overview_greeting`, `set_conversation_title`, `recall_memory`, `remember_memory`, `remember_profile_skill`, `forget_profile_skill` |
| Registration protection | 4 | `get_registration_risk`, `notify_registration_risk`, `end_registration_conversation`, `ban_l0_user` |
| Administrator reads | 7 | `get_admin_user_skills`, `get_admin_server_config`, `get_admin_channels`, `get_admin_model_inventory`, `list_admin_operations`, `execute_admin_operation`, `audit_admin_model_pricing` |
| Administrator changes | 5 | `prepare_admin_user_skill_change`, `prepare_admin_config_change`, `prepare_admin_channel_change`, `prepare_admin_model_sync`, `prepare_admin_pricing_change` |
| Site improvement issues | 3 | `get_site_issues`, `create_site_issue`, `update_site_issue` |
| Tool market connections | 3 | `get_connected_market_tools`, `connect_market_tool`, `call_market_tool` |
| Visualizations | 4 | `show_chart`, `show_statistics`, `show_choices`, `show_flowchart` |

## 修改及保留的边界

完整参数不再每轮全量发送。常用工具直接提供，其余按需加载；强制业务检查仍直接提供其所需工具。工具发现不是业务授权，禁用、等级、账号归属、管理员会话、版本、付费授权及用户确认仍在执行时检查。

成功读取不重复执行。失败仍有一次重试机会；明确不可重试的结果被阻止；写入后允许核实最新状态。分类由工具注册表决定，不再因为名称以 `get_` 开头就认定只读。通用管理员操作继续使用独立的操作级只读检查。

图表展示返回短回执；完整图表数据继续传给浏览器，并保留在原工具参数中。商品/MCP 目录原有的分页、详情读取、价格单位和裁剪标记不变。没有统一截断所有返回值，避免丢失精确 ID、执行回执或必要的报价条件。

普通结束与注册保护完全分开。`end_conversation` 输出结语后停止本轮，不再调用模型，也不执行当前批次后续工具。无效或禁用调用不生效，不能替代强制业务工具。用户可以继续同一会话；不会改动账号、限制或归档状态。普通文字最终答复仍无需额外结束工具。

## 配置检查

| 设置 | 基线代码默认值 | 本次处理 |
| --- | --- | --- |
| `AssistantModel` / `AssistantGroup` | `deepseek-v4-flash` / `default` | 不改模型或路由组。 |
| `AssistantReasoningEffort` / `AssistantTemperature` | `auto` / 0.2 | 不覆盖已有配置。 |
| `AssistantMaxTokens` | 900，合法范围 64–8192 | 不通过压低输出上限造成答案或参数截断。 |
| `AssistantMaxSteps` | 12，合法范围 1–32 | 保留最后一轮答复和已有必需流程预算。 |
| `AssistantTimeoutSeconds` | 90，合法范围 5–300 | 保留取消、人工接管及工具超时处理。 |
| `AssistantCacheEnabled` / `AssistantCacheTTLMinutes` | 开启 / 1440 | 结束回执不缓存；不扩大用户数据缓存范围。 |
| `AssistantToolPolicy` | version 1，默认继承开启 | 新工具复用组、单项与等级规则；不添加重复设置键。 |
| `AssistantSkillFiles` / `AssistantSkills` | 空 | 保留平台技能与用户记忆隔离；不删除管理员指令。 |
| 会话保留 | 活跃 90 天、归档 30 天、安全记录 180 天 | 不改保存周期或历史保护。 |

`discover_tools` 被关闭时恢复原有完整目录，防止管理开关间接关闭其他能力。结束工具可以独立关闭。工具页原有保存冲突、失败关闭和权限检查不变。运行时实际配置需要管理员从生产设置页核实，不能把上述默认值当成线上现值。

## 验证方法

```sh
cd apps/lmm-extensions
go test ./internal/agent ./setting ./controller -run 'Test(Assistant|SuccessfulRead|FailedRead|LoopGuard|NormalizeCalls)' -count=1
go test ./controller -run TestAssistantEfficiencyMeasuresDefinitionBytesForEachRole -count=1 -v
```

测试包含目录与定义对齐、全部工具的禁用及分类、按需加载、跨请求隔离、配置关闭、重复读取、写后验证、流式与非流式结束、后续工具不执行、结语保存及同一会话继续。测试中的 schema 大小指标是 JSON 字节数，不是分词器测得的 token 数，也不是生产账单节省比例。
