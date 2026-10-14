# 文档目录

[项目首页](../README.md) · [贡献指南](contributing.md)

本目录以微服务分支为准：可执行程序只有 `lmm` 客户端、`lmm-core` 核心和 `lmm-extensions` 扩展；Web 是单独构建的静态文件。线上 Go 单体的历史说明不是新架构安装步骤。

默认开发主线为 `wip/rust-core-go-extensions`；`main` 与 `traditional` 已冻结，
见[分支交接规则](development-branches.md)。
传统 main 的增量是否已移植，见[固定源码对齐核查](main-wip-parity-audit.md)。
[Extore 商品同步](extore-shop-binding-followup.md)尚未验收，禁止部署。

## 主要入口

| 主题 | 文档 |
| --- | --- |
| 架构、模块状态与剩余工作 | [架构与状态](core-migration.md) |
| 注册滥用、蜜罐、独立限频与封禁通知 | [行为与接线范围](abuse-controls.md) |
| 低频用户管理与批量预览 | [Python 管理客户端](admin-cli.md) |
| 架构收尾的并行分工与串行验收 | [10 个并行任务与 1 个串行收尾提示词](architecture-completion-prompts.md) |
| 新安装、服务隔离与日常启停 | [Docker 开发栈](../deployment/docker/README.md) |
| 环境、命令与测试 | [开发指南](development.md)、[本地质量门禁](quality.md) |
| TOML 配置、文件路径与升级保留规则 | [配置与文件路径](filesystem-layout.md) |
| 可编辑协议与发布规则 | [用户、隐私、退款模板](legal/README.md) |
| 独立分发包、校验与发布边界 | [打包与分发](release-architecture.md) |
| 历史资料的使用范围 | [历史文档](archive.md) |

## 详细设计

[核心身份](core-identity.md) · [跨进程接口](core-protocol.md) · [业务验收范围](microkernel-business-acceptance.md)

[RT16 Markdown 隔离安全复现](../scripts/security/rt16/README.md)只验证组件，不能替代商品、客服、个人资料和助手的存储读写验收。

[部署研究结论与待接入项](deployment-research.md)保留已移除实验目录的有效结论与历史来源，不作为正式部署步骤。

Go 模块的接口与存储规则以各自源码目录为准：[扩展宿主](../services/extensions/README.md)、[助手](../services/extensions/internal/modules/assistant/README.md)。[CLI 子项目文档](https://github.com/TokenNotIncluded/lmm#readme)与服务端部署分开。

品牌与产品：[品牌设计](brand-design.md)、[产品记录](product.md)、[分支来源与归属](fork.md)。前端资料：[设计](../apps/web/DESIGN.md)、[Logo](../.github/assets/README.md)、[翻译术语](translation-glossary.md)。[安全报告](../.github/SECURITY.md)与[支持渠道](SUPPORT.md)不属于运行时用户协议。

旧部署、OpenAPI、审计和已合并专题统一从[历史索引](archive.md)查找原文。它们不作为当前服务能力或测试通过的依据。

维护规则：入口只描述当前可执行步骤；细节留在拥有该接口的模块中。删除实现后，删除生成依赖并将历史来源集中到 archive.md；不新增逐文件占位页。保留许可证、安全记录和仍有使用方的接口约定。
