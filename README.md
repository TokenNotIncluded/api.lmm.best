<div align="center">
  <img src=".github/assets/lmm-logo.svg" alt="LMM Forge" width="96" height="96" />
  <h1>LMM Forge</h1>
  <p>模型接入、MCP 工具与开源协作，一个控制台。</p>
  <p>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml"><img src="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI" /></a>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/releases?q=go-v"><img src="https://img.shields.io/github/v/release/TokenNotIncluded/api.lmm.best?filter=go-v%2A&amp;label=Go&amp;display_name=tag" alt="Go release" /></a>
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

以下步骤用于本地开发，**不是生产安装脚本**。准备 Git、Just、Bun 1.3.14、Node.js 22.12+、Go 1.25.1+，以及独立的 PostgreSQL 和 Valkey 服务。

```bash
git clone https://github.com/TokenNotIncluded/api.lmm.best.git
cd api.lmm.best
just setup
cp .env.example apps/api-go/.env
```

先编辑 `apps/api-go/.env`：填写 `SQL_DSN`、`REDIS_CONN_STRING`，并设置独立随机的 `SESSION_SECRET` 与 `CRYPTO_SECRET`。请使用开发数据库：服务启动时默认执行数据库迁移。

在仓库根目录启动后端：

```bash
just dev-go
```

在第二个终端启动前端，避免与后端的 3000 端口冲突：

```bash
bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

打开 <http://localhost:5173>，完成初始化。完整配置、测试命令和新核心开发说明见[本地开发指南](docs/development.md)。现有 Go 开发流程的 `docker-compose.dev.yml` 仍需自行提供。新的 [Docker 开发栈](deployment/docker/README.md) 仅用于 Rust 核心与 Go 扩展迁移，不能替换当前生产后端。

## 部署与升级

Go 和 Web 分别以 `go-vX.Y.Z`、`web-vX.Y.Z` 发布。**合并代码或发布版本不会自动部署到生产。**

| 已有安装方式 | 从这里开始 |
| --- | --- |
| 独立 systemd 服务 | [检查、升级、确认与回退](docs/manual-systemd-deployment.md) |
| 软件包管理的 Go / Web | [签名发布与升级事务](docs/seamless-upgrades.md) |
| 只更新前端 | [组件发布边界](docs/release-architecture.md) · [前端部署工作流](.github/workflows/deploy-web-frontend.yml) |
| 数据库与缓存 | [PostgreSQL 迁移](docs/postgresql-migration.md) · [生产切换](docs/postgresql-cutover.md) · [Valkey 运维](docs/valkey-lmm-api.md) |

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
