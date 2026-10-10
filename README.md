<div align="center">
  <img src=".github/assets/lmm-logo.svg" alt="LMM Forge" width="96" height="96" />
  <h1>LMM Forge</h1>
  <p>模型接入、MCP 工具与开源协作，一个控制台。</p>
  <p>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml"><img src="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI" /></a>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/releases?q=web-v"><img src="https://img.shields.io/github/v/release/TokenNotIncluded/api.lmm.best?filter=web-v%2A&amp;label=Web&amp;display_name=tag" alt="Web release" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="License: AGPL-3.0" /></a>
  </p>
  <p><strong>简体中文</strong> · <a href="README_EN.md">English</a></p>
  <p><a href="https://api.lmm.best">在线体验</a> · <a href="https://api.lmm.best/guide">接入指南</a> · <a href="docs/README.md">项目文档</a> · <a href="https://github.com/TokenNotIncluded/api.lmm.best/issues">问题反馈</a></p>
</div>

## 简介

LMM Forge 是一个开源的 AI 服务控制台。它将模型调用、Remote MCP 工具市场和开源悬赏协作放在同一个账户与额度体系中。用户可以接入自己的客户端，发布收费工具，也可以通过完成开源任务获得平台余额。

本项目基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 持续开发。Go 仍是当前生产后端，前端使用 React 与 TypeScript。新分支正迁移为 Rust 核心与 Go 扩展服务，尚不可接入生产流量；独立 CLI 仍为预览版。

## 核心能力

| 方向 | 可以做什么 |
| --- | --- |
| 模型接入 | 接入 OpenAI Chat Completions / Responses、Anthropic Messages 和 Gemini 接口，管理渠道、分组、额度与用量记录。 |
| 客户端连接 | 通过 OAuth 连接 Pi、DSH、OpenCode 和 Codewhale；VS Code、Zed 集成仍在预览阶段。 |
| 工具市场 | 接入已有的 HTTPS Remote MCP 服务，按工具设置价格，发布经过验证与审核的版本。 |
| 权限与支出 | 分别控制工具加载、调用授权、客户端权限、调用次数、有效期与支出上限。 |
| 开源协作 | 发布悬赏、锁定奖励、提交 Issue / PR 证据，并完成审核、验收与争议处理。 |
| 管理控制台 | 管理用户、角色、余额、充值和用量；支持桌面端、移动端及深浅色界面。 |

实际可用的模型与能力取决于上游渠道和账户权限。工具收入进入平台余额，**目前不支持提现**。加载工具不等于授权付费；内置绘图工具不收工具调用费，但模型用量仍会计费。

## 在线体验

直接查看[服务首页](https://api.lmm.best)、[模型定价](https://api.lmm.best/pricing)和[工具市场](https://api.lmm.best/tool-market)。使用已有服务不需要部署本仓库；接入客户端请从[新手指南](https://api.lmm.best/guide)开始。

## 快速开始

**本分支是破坏性的全新架构 WIP，不是线上整站的可用替代品。** 不提供旧用户导入、旧表升级、余额换算或自动数据库迁移。

使用 [Docker 开发栈](deployment/docker/README.md)。通过 `lmm-core-admin init-db` 显式安装空的核心数据库；已有应用对象或重复安装都会拒绝。普通启动不建表、不改表。

`apps/core-rust` 是 Rust 核心，`apps/api-go` 是 Go 扩展服务。Go 目前只接通只读身份模块，商店、助手、支付和原控制台接口尚未接回。模型接口仍不可用。前端源码保留，但不能据此认为整站功能已经完成。

本地命令和各服务独立的环境配置见[开发指南](docs/development.md)。不要把旧 Go 的数据库配置复制给新的扩展服务。

## 部署边界

核心与扩展使用独立 Docker 项目。更新 Go 不应重启核心或核心数据库。本分支不再提供旧 Go 单体的软件包和 systemd 数据库升级工具；验证流程不部署生产，也不修改生产数据库。

参见[全新安装](deployment/docker/README.md)、[核心身份](docs/core-identity.md)、[Protobuf 通信](docs/core-protocol.md)。旧部署文档仅描述已经退役的架构，不可用于安装本分支。

## 文档

[文档目录](docs/README.md)按使用、开发和运维任务分类。常用入口：

- **使用与协作：**[工具发布](docs/tool-market-guide.md)、[连接与授权](docs/tool-market-connections.md)、[悬赏与结算](docs/open-source-bounties.md)。
- **开发与接口：**[本地开发](docs/development.md)、[贡献指南](CONTRIBUTING.md)、[管理 API](docs/openapi/api.json)、[模型转发 API](docs/openapi/relay.json)。
- **发布与维护：**[发布架构](docs/release-architecture.md)、[认证与会话](docs/authentication.md)、[核心与扩展迁移](docs/core-migration.md)。

## 贡献与安全

提交前阅读 [CONTRIBUTING.md](CONTRIBUTING.md)，使用仓库的 [Issue 模板](.github/ISSUE_TEMPLATE)，并说明实际执行的验证。问题支持见 [SUPPORT.md](SUPPORT.md)，社区规范见 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。

漏洞报告请按 [SECURITY.md](SECURITY.md) 处理。不要在 Issue、日志或截图中公开密钥和账户信息。默认边缘访问策略阻止地理位置为中国大陆（`CN`）的请求，管理员可以配置明确的 IP 路由规则。

[用户协议](docs/legal/user-agreement.md) · [隐私政策](docs/legal/privacy-policy.md) · [服务条款](docs/legal/terms-of-service.md) · [Logo 与使用规范](.github/assets/README.md)

## 许可证与致谢

本项目按 [AGPL-3.0](LICENSE) 发布。上游署名和必要声明保留在 [NOTICE](NOTICE)、[FORK.md](FORK.md) 与[第三方许可证清单](THIRD-PARTY-LICENSES.md)中。

README 的信息层次参考了 [TokenRouter](https://github.com/TokenFlux/TokenRouter)。本文的功能、部署说明、许可证与 Logo 均以 LMM Forge 本身为准。
