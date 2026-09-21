# Codewhale LMM Provider

通过 LMM 网页授权登录，在 Codewhale 中使用 LMM 的模型与分组，不需要复制 API Key。

**当前版本：0.1.0-alpha.1。** 这是 OAuth 伴随适配器，不是 Codewhale 原生 `/login` provider 插件。依据 Codewhale `b367f6248715510cb5d527e57e4e352db14cb0cd` 的插件和自定义 provider 接口实现。该版本的插件接口不能注册模型提供商或 OAuth 回调，因此使用独立 CLI 完成授权，通过临时本地 provider 连接 Codewhale；`plugin.json` 提供可选的使用说明 skill。

目前支持目录中声明 `openai-completions` 的模型、工具调用请求与 SSE 透传。**不支持 Responses-only、Anthropic Messages-only 模型，也未移植 Pi 的 MCP、市场工具和原生模型选择器集成。** 不会伪造模型能力、上下文长度或价格。

## 安装

需要 Node.js 22+ 和已经安装的官方 `codewhale` 可执行程序。本包没有第三方运行时依赖。

在父项目仓库根目录执行：

```sh
npm install --global ./packages/codewhale-lmm-provider
```

独立源码包解压后，也可以在本目录执行 `npm install --global .`，或不安装，直接运行 `node src/cli.mjs --help`。

## 使用

```sh
codewhale-lmm login
codewhale-lmm models
codewhale-lmm run --model '<models 输出的完整 id>'
```

登录打开 LMM 授权页；回到终端后即可使用。模型 ID 包含分组，不能用上游模型名替代；多个模型时不会静默选择第一个。

```sh
codewhale-lmm run --model '<完整 id>' -- exec '检查这个项目的测试'
codewhale-lmm status
codewhale-lmm balance
codewhale-lmm usage
codewhale-lmm logout
```

`status` 只读本地状态；`balance` 和 `usage` 读取 LMM 账户余额与授权允许的日聚合用量。未知价格保持 `null`，不是免费。

启动时创建临时 provider 配置，设置 `CODEWHALE_CONFIG_PATH`，并传入临时本地连接凭据。不会覆盖用户原有配置，也不会继承原配置中的自定义运行设置。关闭 Codewhale 后清理本地监听和临时配置。适配器不重试模型 POST；Codewhale 自身的重试策略仍由宿主管理。

可选环境变量：

| 变量 | 用途 |
| --- | --- |
| `LMM_ISSUER` | 默认 `https://api.lmm.best`；也可用 `--issuer` |
| `LMM_CODEWHALE_HOME` | 独立凭据目录，默认 `$CODEWHALE_HOME/lmm-provider` 或 `~/.codewhale/lmm-provider` |
| `LMM_CODEWHALE_BIN` | 官方 Codewhale 可执行文件路径 |

Termux 优先用 `termux-open-url` 打开浏览器。`login --no-browser` 仅输出授权 URL，仍需浏览器能访问当前机器的回环回调；这不是 device-code 登录，不能直接解决远程 SSH 的浏览器回调问题。

## 可选的 Codewhale 插件说明

在 Codewhale 会话中执行：

```text
/plugin install ./packages/codewhale-lmm-provider
/plugin validate codewhale-lmm-provider
/plugin enable codewhale-lmm-provider
```

按 Codewhale 显示的内容与权限哈希自行审查、信任，再启用。这里安装的是帮助 skill；它不会自动运行登录，也不能替代 `codewhale-lmm run`。不写入或绕过宿主的信任记录。

## 服务端接入

父项目必须先部署 `lmm-codewhale` 客户端注册补丁。该客户端使用授权码 + PKCE S256，独立于 `lmm-pi` / `lmm-dsh`。初始 scope 仅为：

```text
catalog:read balance:read usage:read models:invoke
```

分组权限由用户同意时的服务端快照追加。没有 MCP、市场工具或账户管理权限。授权码、刷新和撤销均绑定此客户端。

保留既有 `OAUTH_SERVER_ENABLED`、issuer 和分组白名单门槛，不自动启用或部署生产 OAuth。旧服务端尚未登记新客户端时登录会失败，不能拿 Pi 的 client ID 顶替。

## 凭据与故障恢复

OAuth access/refresh token 保存在适配器私有目录，不进入 Codewhale 的配置、命令行或环境。凭据文件为 POSIX `0600`，目录为 `0700`；不安全权限会拒绝使用。不同 issuer 不能混用存储目录。相同 OS 用户仍能读取文件；这不是与 Codewhale 进程隔离的系统沙箱。Windows 的独立 ACL 保护尚未实现，请不要将此预览版用于共享 Windows 主机。

刷新以跨进程锁串行执行；请求前原子写入 `refresh_pending`，成功后原子替换整对令牌。发生响应丢失或崩溃时停止自动刷新，不重放可能已消费的 refresh token。重新授权前先 `logout`；无法完成远端撤销时，本地凭据会保留。明确使用 `logout --local-only` 只删除本地文件，**不代表服务端授权已撤销**。锁的拥有者已退出时可用 `unlock` 清理；不会抢占活跃进程的锁。

本地桥只监听 `127.0.0.1` 随机端口，要求随机 Bearer，拒绝浏览器 Origin、任意上游、未授权分组和不支持的路径。上游只收到 OAuth Bearer、目录给出的 `X-LMM-Group` 与原协议请求；错误不回显服务端响应体或凭据，断开客户端时取消流式请求。

## 验证

```sh
npm test
npm run check
npm run pack:check
```

本地 Linux / Node.js 22.16.0 已执行 36 项测试，全部通过，包括真实 HTTP 回环 PKCE、跨进程刷新互斥、崩溃日志、撤销失败保留、分组隔离、SSE 与取消、临时配置清理，以及模拟宿主进程调用本地桥。模拟宿主不是官方 Codewhale 二进制；尚未完成真实生产授权、Codewhale TUI、账单核对或 Windows/Termux 实机验收。Go 注册测试已在父项目首轮 GitHub CI 通过；本地没有运行 Go 依赖环境。

## 独立仓库与子模块

当前交付暂存于父项目的普通目录中，尚未创建远端独立仓库，也没有向 `.gitmodules` 写入失效地址。

在本 PR 合并后的干净父项目克隆中，使用已登录且可建组织仓库的 GitHub CLI 执行：

```sh
node packages/codewhale-lmm-provider/scripts/publish-subproject.mjs
```

该命令创建公开仓库 `TokenNotIncluded/codewhale-lmm-provider` 并推送已提交源码，然后在独立 worktree 中将父项目目录替换为真实 Git submodule，推送分支并打开 PR。不会删除原工作区源码、覆盖已有远端仓库或自动合并。远端创建成功而后续失败时会停止并报告，不会假装回滚远端。

## 接口依据

- https://github.com/Hmbown/Codewhale/blob/b367f6248715510cb5d527e57e4e352db14cb0cd/docs/PLUGIN_BUNDLES.md
- https://github.com/Hmbown/Codewhale/blob/b367f6248715510cb5d527e57e4e352db14cb0cd/docs/CONFIGURATION.md
- https://github.com/TokenNotIncluded/api.lmm.best/blob/main/apps/api-go/service/oauth_contract.md

AGPL-3.0-only. 本项目不隶属于 Codewhale。
