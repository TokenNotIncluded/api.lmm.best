# LMM Forge 文档

[简体中文首页](../README.md) · [English overview](../README_EN.md) · [贡献指南](../CONTRIBUTING.md)

先按任务选择入口。Go 是默认后端；Rust 后端和独立 CLI 仍为预览版。下面保留现有文档路径，避免更新目录后使旧链接失效。

## 从这里开始 · Start here

| 你要做什么 | 入口 |
| --- | --- |
| 连接客户端 | [在线接入指南](https://api.lmm.best/guide) · [OpenCode](opencode-provider.md) · [VS Code / Zed](editor-providers.md) |
| 发布或使用工具 | [工具市场](tool-market-guide.md) · [连接与权限](tool-market-connections.md) |
| 参与开源悬赏 | [流程、证据与结算](open-source-bounties.md) |
| 本地开发与测试 | [开发指南](development.md) · [贡献要求](../CONTRIBUTING.md) |
| 更新已有服务 | [独立 systemd](manual-systemd-deployment.md) · [软件包签名升级](seamless-upgrades.md) |
| 查接口 | [管理 API](openapi/api.json) · [模型转发 API](openapi/relay.json) |

## 使用与产品 · Product

- [工具发布](tool-market-guide.md)与[客户端授权](tool-market-connections.md)：版本、审核、费用与调用权限。
- [开源悬赏](open-source-bounties.md)：发布、验收、争议与结算。
- [模型价格锁](model-price-locks.md)、[Claude 拒绝计费](claude-refusal-billing.md)、[自定义余额查询](advanced-custom-balance.md)。
- [个人用量聚合](profile-usage-aggregation.md)、[iNet 客户端](ionet-client.md)、[渠道附加设置](channel/other_setting.md)。

## 开发与维护 · Development

- [本地环境与命令](development.md)、[认证与会话](authentication.md)、[Go 内存管理](go-memory-management.md)。
- [前端设计](frontend-design.md)、[着色器维护](shaders.md)、[Logo 规范](../.github/assets/README.md)。
- [异步任务指标](async-task-performance-metrics.md)、[脚本插件宿主决策](task-plugin-host-decision.md)。
- [翻译术语](translation-glossary.md) · [法语术语](translation-glossary.fr.md) · [俄语术语](translation-glossary.ru.md)。
- [核心与扩展迁移](core-migration.md)、[Docker 开发栈](../deployment/docker/README.md)、[CLI 预览](../apps/lmm/README.md)。

## 部署与发布 · Deployment

| 主题 | 文档 |
| --- | --- |
| 发布边界 | [组件发布架构](release-architecture.md) · [运行入口与回退约束](backend-cli-deployment-contract.md) |
| 更新已有实例 | [systemd 部署](manual-systemd-deployment.md) · [签名升级](seamless-upgrades.md) |
| 发布验收 | [发布事务](production-release-transaction.md) · [控制器备份格式](controller-only-backup-format.md) |
| 数据库 | [PostgreSQL 迁移](postgresql-migration.md) · [生产切换](postgresql-cutover.md) |
| 缓存与打包 | [Valkey 运维](valkey-lmm-api.md) · [AUR 打包](../packaging/aur/README.md) |

开发命令不是生产安装器。Go 与 Web 独立发版；合并、打包、发布和部署是不同步骤。

## 接口与行为约束 · API contracts

- [管理接口](openapi/api.json)与[转发接口](openapi/relay.json)。
- [响应模型字段](relay-response-model.md)、[Responses WebSocket](responses-websocket-channels.md)、[流式响应提交边界](stream-commit-boundaries.md)。
- [缺失用量处理](responses-missing-usage.md)、[Token 日志分页](token-log-pagination.md)。
- [JEV 原生转发](jev-native-relay.md)、[OpenAI 原生能力与计价](openai-native-models.md)。
- [内容审核安全说明](moderation-security-review.md)：分组模式、风险归属与公开告知。

## 安全、协议与治理 · Policies

[安全报告](../SECURITY.md) · [问题支持](../SUPPORT.md) · [社区规范](../CODE_OF_CONDUCT.md) · [上游关系](../FORK.md)

[用户协议](legal/user-agreement.md) · [隐私政策](legal/privacy-policy.md) · [服务条款](legal/terms-of-service.md)

## 维护规则 · Maintenance

稳定流程或接口发生变化时，同步修改对应指南，并从本目录链接。中英文 README 保持相同的功能边界、徽章和启动步骤。迁移文档注明适用条件；预览功能不能写成已可用于生产。截图必须来自本项目实际界面，不用示意图冒充产品截图。
