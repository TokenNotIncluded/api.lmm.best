# 内置 AI 客服工具

内置客服当前注册 54 个工具，按 13 组管理。这些是客服的服务器工具。商品和 MCP 组只查询目录，不会加载、授权、调用工具市场服务，也不会购买商品。

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

32 个工具只读，10 个工具准备确认表单，11 个工具会在服务端规则允许时保存记录或改变状态，1 个工具生成站内链接。特别是名称带 `prepare` 的新人礼和每周折扣会保存决策、消耗相应机会，并非纯预览。预约、记忆、标题、L1 升级和注册保护也有实际写入，因此单独标为「服务端校验写入」。

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

测试包括实际注册目录匹配、54 个禁用模拟调用、权限和组继承、目录缓存失效、强制选择、无工具时的模型请求、旧确认卡和管理读取别名。前端测试模拟工具目录接口及开关、搜索、保存、配置错误和重试，没有调用真实账务、删除、预约或发送消息工具。
