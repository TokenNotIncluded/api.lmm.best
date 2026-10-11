# 文档目录

[项目首页](../README.md) · [贡献指南](../CONTRIBUTING.md)

本目录以微服务分支为准：可执行程序只有 `lmm` 客户端、`lmm-core` 核心和 `lmm-extensions` 扩展；Web 是单独构建的静态文件。线上 Go 单体的历史说明不是新架构安装步骤。

## 六个入口

| 主题 | 文档 |
| --- | --- |
| 架构、模块状态与剩余工作 | [架构与状态](core-migration.md) |
| 新安装、服务隔离与日常启停 | [Docker 开发栈](../deployment/docker/README.md) |
| 环境、命令与测试 | [开发指南](development.md)、[本地质量门禁](quality.md) |
| 可编辑协议与发布规则 | [用户、隐私、退款模板](legal/README.md) |
| 独立分发包、校验与发布边界 | [打包与分发](release-architecture.md) |
| 历史资料的使用范围 | [历史文档](archive.md) |

## 详细设计

[核心身份](core-identity.md) · [跨进程接口](core-protocol.md) · [业务验收范围](microkernel-business-acceptance.md)

[部署研究结论与待接入项](deployment-research.md)保留已移除实验目录的有效结论与历史来源，不作为正式部署步骤。

Go 模块的接口与存储规则以各自源码目录为准：[扩展宿主](../apps/lmm-extensions/README.md)、[助手](../apps/lmm-extensions/internal/modules/assistant/README.md)。[CLI 子项目文档](https://github.com/TokenNotIncluded/lmm#readme)与服务端部署分开。

前端资料：[设计](../apps/web/DESIGN.md)、[Logo](../.github/assets/README.md)、[翻译术语](translation-glossary.md)。[安全报告](../SECURITY.md)与[支持渠道](../SUPPORT.md)不属于运行时用户协议。

旧部署、OpenAPI、审计和已合并专题统一从[历史索引](archive.md)查找原文。它们不作为当前服务能力或测试通过的依据。

维护规则：入口只描述当前可执行步骤；细节留在拥有该接口的模块中。删除实现后，删除生成依赖并将历史来源集中到 archive.md；不新增逐文件占位页。保留许可证、安全记录和仍有使用方的接口约定。
