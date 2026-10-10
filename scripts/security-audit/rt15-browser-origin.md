# RT-15：浏览器来源边界检查（未完成端到端验收）

关联审计：[#711](https://github.com/TokenNotIncluded/api.lmm.best/pull/711)。
基线：`2f4164978cf27f6e0e605b0589fbe631aef2c441`。
工作分支：`test/rt-15-browser-origin`，目标 `main`，保持 Draft。
本记录针对 Go 主线，不涉及 #675，也不改登录 redirect 或通用权限。

## 结论与实际修复

**不能据本轮结果宣称恶意来源无法借用会话。真实浏览器攻击链、实际
WebSocket 握手和数据库写入均未完成。** 已执行来源判断函数及窗口消息
处理函数的局部测试，另确认一个 CORS 请求头配置兼容问题。

`middleware/cors.go` 只配置 `AllowHeaders: ["*"]`。锁定的
`gin-contrib/cors v1.7.2` 将该列表直接写入预检响应。
[Fetch 标准](https://fetch.spec.whatwg.org/#cors-non-wildcard-request-header-name)
将 `Authorization` 定义为不能由请求头通配符授权的字段。因此显式
Bearer 浏览器客户端所需的授权头许可缺失。

配套修复仅把列表改为 `["*", "Authorization"]`。保持原有来源、方法和
凭据配置，不反射外部来源、不增加 Cookie 授权、不把来源检查全局套在
CLI 上。该改动不是已经复现的跨站读取或账号接管修复。
`Access-Control-Allow-Origin: *` 与带凭据模式的读取限制，不代表服务器
没有执行请求；本轮没有用“读不到响应”作为“没有写入”的证据。

测试和业务修复分开提交。测试提交不改变业务。

## 源码边界清单

| 边界 | 核对的源码 | 当前证据范围 |
| --- | --- | --- |
| 后台认证 | `middleware/auth.go` 的 `classifyDashboardCredentialUncached` | 读取显式 Authorization，区分面板会话令牌与 PAT；不能用刷新 Cookie 代替其认证验收 |
| Cookie 刷新/退出 | `middleware/auth_origin.go`、`common/session_cookie.go` | 安全模式校验单一 Origin 或 Referer 回退；非安全本地模式按现有设计不校验。实际登录 Cookie 属性和写入未测 |
| HTTP CORS | `middleware/cors.go`、`router/relay-router.go`、锁定的 cors 依赖 | 配置分析与静态回归；未测应用的实际 OPTIONS 响应或浏览器读取 |
| 常规 OAuth 绑定消息 | `account-bindings-tab.tsx` | 检查 origin/source/provider/state；调用后端前清除 pending。局部函数测试使用调用记录器，不是真实绑定接口 |
| OAuth 回调结果窗口 | `routes/oauth/$provider.tsx` | 核对 origin/source/provider/state；真实窗口、关闭、超时及重放行为未测 |
| Telegram 绑定消息 | `telegram-bind-dialog.tsx` | 检查 origin 和 flow_token。receiver 没有 source 检查及同步一次性门闩；这属于待验证边界，不是已证明的站外绑定攻击 |
| 模型 WebSocket | `router/relay-router.go`、`controller/relay.go`、`controller/native_voice.go` | `/v1/responses`、`/v1/realtime`、`/v1/live/sessions`、`/v1/realtime/translations` 经 TokenAuth；相关控制器使用同一 upgrader。只测了它的来源判断函数 |

WebSocket 判断保留无 Origin 客户端、精确受信来源和本机开发端口对照。
它还读取 `X-Forwarded-Proto`，不同于 Cookie 来源判断。代理信任、Cookie
或显式 API Key 认证、升级后的双向数据和资金行为都未通过实际握手验证。
没有修改这些策略。

Telegram receiver 的成功分支只触发界面回调；没有证据表明一条消息能够
替代服务器绑定。标准 OAuth 局部测试中的“后端成功”也是测试输入，不能
当作真实服务器状态。没有把这两种情况混为一谈。

## 真实浏览器执行记录

在本机临时端口启动纯静态 HTTP 哨兵，用 Chromium 144.0.7559.96 访问
`http://127.0.0.1:46821/`。浏览器返回 `net::ERR_BLOCKED_BY_ADMINISTRATOR`，
页面显示 `127.0.0.1 is blocked`。哨兵请求记录为 `[]`。

没有修改浏览器管理策略，也没有改用其他地址绕过阻止。三个来源环境、
登录账号及攻击页没有进入浏览器验收阶段。这里只是环境可用性预检，
**不是 HTTP CORS 的 OPTIONS 预检**。

| 场景 | CORS 预检 | 实际业务请求 | 私密响应可读性 | 数据库变化 |
| --- | --- | --- | --- | --- |
| 本机静态页导航 | 不适用 | 无业务请求；静态哨兵也未收到请求 | 不适用 | 未启动业务数据库 |
| 外站/同站子域表单：Key、绑定、安全设置 | 未执行 | 未执行 | 未验证 | 未查询，不能填零 |
| 外站/同站子域 fetch：Cookie 刷新、退出 | 未执行 | 未执行 | 未验证 | 未查询，不能填零 |
| 显式 Bearer 浏览器客户端 | 未执行；仅检查配置 | 未执行 | 未验证 | 未查询 |
| 合法及伪造 OAuth 窗口 | 不适用 | 仅函数调用记录器 | 真实浏览器未验证 | 真实绑定未验证 |
| WebSocket：恶意来源、合法浏览器、CLI | HTTP CORS 不适用于此验收 | 未发起真实握手 | 未验证双向连接 | 认证及会话状态未验证 |

## 本机已执行测试

| 检查 | 结果 | 不代表什么 |
| --- | --- | --- |
| Cookie 来源判断函数 | 21 个分项通过，启用 Go race 检查 | 不代表 Gin guard、Cookie 发送、刷新/退出事务通过 |
| WebSocket 来源判断函数 | 15 个分项通过，启用 Go race 检查 | 不代表 WebSocket 握手或认证通过 |
| OAuth/Telegram 实际 handler 的局部执行 | 17 个测试通过 | 不代表 React 挂载、真实 postMessage、OAuth 交换或数据库绑定通过 |
| CORS 配置静态回归 | 原源码退出 1；配套修复退出 0 | 不代表真实预检或浏览器已通过 |
| 新增 CORS Gin 测试 | 未运行 | 不计入通过数 |
| Go 变更文件格式 | gofmt 完成，复查无差异 | 不是完整构建 |

使用 Node 22.16.0 和 Go 1.23.2。没有取得完整 main 检出：clone 在 GitHub
域名解析处失败。通过连接器读取精确基线文件，构造仅包含相关函数的
独立本机模块。完整 `session_cookie.go`、`auth_origin.go`、原 `cors.go`
的 Git blob 摘要分别核对为：

```text
28981ce9d81722eb483f45a42179c01cd3b8ace0
f2c9567d4b0e4b1b7c9a35f9dcb0aff4bcb7a819
53200ea60967a30ad6dd2f005c1abcebdf955e25
```

`relay.go` 仅取 `checkWebSocketOrigin` 和 `isWebSocketLoopbackHost`；前端
只取实际 handler。其完整远端文件 blob 分别为
`41a7fa8193a60d684d5c5c39a1ae7574fe4fbd16`、
`26b6008bc9b76349a973fc3b0fd1ecab9c46688a`、
`87750f2349ae4a4dce679a39f7dd0f8a3fcbf9ef`。
这些完整文件摘要不是本地片段的摘要，不能据此称为完整应用检出。

Go 局部模块补充的只有配置变量声明和 import，不替换认证、数据存储或
握手。TypeScript 测试通过语法树加载 handler，并提供窗口对象及 API
调用记录器。提交的 Go 来源测试与局部模块执行的测试文件一致。

实际执行命令（本次临时工作目录）：

```sh
python /mnt/data/rt15/browser_preflight.py
# blocked_or_unverified; 静态哨兵 hits=[]

cd /mnt/data/rt15/primitive-checks
GOTOOLCHAIN=local GOWORK=off GOPROXY=off GOSUMDB=off \
  go test -race -count=1 -v ./middleware ./controller
# 两组来源判断，共 21 + 15 个分项通过

RT15_SOURCE_ROOT=/mnt/data/rt15/source-slices \
NODE_PATH=/opt/nvm/versions/node/v22.16.0/lib/node_modules \
  node --test /mnt/data/rt15/delivery/scripts/security-audit/rt15-postmessage.test.cjs
# 17 通过

python -B /mnt/data/rt15/delivery/scripts/security-audit/rt15-cors-config.py \
  /mnt/data/rt15/source-slices/apps/api-go/middleware/cors.go
# source_configuration_only; 原配置 FAIL，退出 1
python -B /mnt/data/rt15/delivery/scripts/security-audit/rt15-cors-config.py \
  /mnt/data/rt15/delivery/apps/api-go/middleware/cors.go
# source_configuration_only; 配套修复 PASS，退出 0
```

项目要求 Go >=1.25.1，本机只有 1.23.2。在仅含版本声明的最小 go.mod 中
实际得到 `go.mod requires go >= 1.25.1`。这是工具链门槛检查，不是完整
项目构建记录。PostgreSQL、Redis、完整应用依赖和前端应用环境也缺失。
完整 middleware/controller 测试、应用构建、类型检查、文档检查及数据库
验收未运行；没有把局部模块的通过结果转记成应用通过。

## 后续本机复验入口（本轮未执行）

在完整检出及项目依赖可用的隔离本机，执行以下已有测试入口：

```sh
python3 -B scripts/security-audit/rt15-cors-config.py apps/api-go/middleware/cors.go
node --test scripts/security-audit/rt15-postmessage.test.cjs
cd apps/api-go
go test -race -count=1 -run '^TestRT15' ./middleware ./controller
```

Node 测试默认加载此检出的前端 TypeScript 依赖；不能把 `RT15_SOURCE_ROOT`
指向旧片段后宣称是在验证新版本。上述入口仍不包含真实浏览器和数据库
验收。真实复验必须使用可正常访问本机的受管浏览器、隔离账号和本地模拟
服务；完整记录 OPTIONS、请求 Cookie/显式认证模式、响应可读性、绑定或
安全设置记录的前后差异，以及实际 WebSocket 升级与双向数据。任何环境
阻止或缺失仍记未验证，不以 HTTP 200、函数返回 true 或无响应替代。

本轮没有访问生产，没有创建真实业务账号、通知或资金操作；没有请求远程
CI、发布、部署或合并。提交使用 `[skip ci]`，不修改工作流。
