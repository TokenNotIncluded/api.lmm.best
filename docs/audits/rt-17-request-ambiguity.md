# RT-17 请求解释一致性：部分执行记录

关联 #711。代码基线：`2f4164978cf27f6e0e605b0589fbe631aef2c441`。
工作分支：`test/rt-17-request-ambiguity`。目标为 main，不涉及 #675。
日期：2026-10-10。**此记录不是完整业务验收，也不是漏洞修复证明。**

## 结论与交付状态

已在本机实际执行 Go 标准 HTTP 服务的原始 TCP 报文测试，并保留请求字节、响应状态和处理器观测。15 个报文场景均满足本测试的边界断言；另执行了标准 JSON 解码观察。

尚未执行项目的 Gin 路由、GJSON、实际请求 DTO、上游适配器及资金链路。新增的项目回归文件已完成语法解析与 gofmt，**未编译、未运行**。本机缺少完整仓库及依赖，Go 为 1.23.2，而基线 go.mod 要求 1.25.1。没有降低项目版本来伪造应用测试通过。

没有证实请求走私、模型授权绕过、少计费或真实资金损失。没有修改业务代码，因此没有修复提交。所有变更均为测试和记录；正式修复须在实际失败得到复现后另行提交。

## 源码中的解释顺序

下列路径均相对 `apps/api-go/`，引用固定基线，而不是不断变化的 main。

| 阶段 | 实际代码与字段来源 | 本轮证据界限 |
| --- | --- | --- |
| 边缘代理与长度 | `packaging/common/lmm-api/edge-policy/nginx/new-api.conf` 开启 TLS/HTTP2、限制请求体；locations 配置向后端使用 HTTP/1.1 | 只阅读仓库配置。实际部署版本、完整 include 与运行配置未复制 |
| Go HTTP 解帧 | 标准 HTTP 服务先处理 Content-Length、Transfer-Encoding、Host，再产生 URL.Path/RawPath/Header | 仅本机 Go 1.23.2 已执行；不等于目标应用的运行版本 |
| 路由及请求体 | `router/relay-router.go` 注册 CORS、解压、请求体存储清理、统计；常规 `/v1` 转发链为 TokenAuth → 请求接入限制 → 模型限流 → Distribute → controller.Relay | 已阅读源码；应用链未运行。实时连接、语音和异步任务另有分支 |
| 身份 | `middleware/auth.go` 先处理 OAuth 边界；随后 WebSocket 子协议可写入 Authorization；特定路径的 x-api-key 可覆盖它；Gemini 相关路径依次接受 query key、x-goog-api-key | 普通 Header.Get/query 读取单值。不同凭据的优先级不自动构成漏洞；必须核对所有后续消费者 |
| 用户及分组 | ValidateUserToken、GetUserCache 读取存储；token.Group 须是用户可用分组；SetupContextForToken 设置 id/token/group/model limits。指定渠道后缀须通过 model.IsAdmin | 没有将客户端传来的角色头当作存储身份；尚未执行实际普通 Key 的攻击链 |
| 模型入口 | `middleware/distributor.go:getModelFromJSONBody` 对 JSON 调用 GJSON GetManyBytes 读取 model/group；live sessions 使用 session.model；Gemini 从路径取模型；realtime 使用首个 query model | 分组和模型的入口解析与后续 DTO 解析不同，故新增真实函数一致性断言 |
| 授权及选路 | Distribute 使用入口 model 检查模型权限、选择渠道，保存 original_model；普通分组来自认证上下文，Playground 部分入口另外读取 body/query group 并检查权限 | 尚未证明其他字段、路径或请求可以改写最终授权结果 |
| 请求 DTO 与计价 | `relay/helper/valid_request.go` 再次调用 UnmarshalBodyReusable；`relay/common/relay_info.go:genBaseRelayInfo` 的 OriginModelName 取自上下文，而非直接取 DTO.Model；`controller/relay.go` 之后估算、计价、预留，再调用上游 | 不能把本地标准 JSON struct 当作实际 DTO 的运行证据 |
| 速度 | `relay/helper/service_tier_pricing.go` 从真实 DTO 的 ServiceTier 及所有 OpenAI-Service-Tier 头取值，交给 servicetier.Requested；加速请求检查渠道、模型、分组、报价和预留 | `/fast` 在文档中是客户端命令，不假设存在一个新转发 URL。速度混合输入未执行 |
| 上游请求体 | `relay/compatible_handler.go` 普通分支经 ModelMappedHelper 改写 DTO 后重新生成 JSON；透传分支从原始 body storage 建立重放读取器 | 两条路径不能混为一谈，也不能仅凭 DTO 不同就声称上游调用了另一模型 |
| 最终发送校验 | ApplyServiceTierToJSON 在加速报价存在时核对最终模型、分组与输出预算；普通报价为空时有不同分支，部分路径会把 JSON 转为 map 后重新生成 | 需分别验收普通/加速、转换/透传、官方/第三方渠道；并非所有路径都原样发出原始 JSON |
| 结算与重试 | controller/relay.go 与 TextHelper 在上述步骤后执行上游调用、响应处理、结算及错误退款 | 上游次数、用户钱包和资金流水在本轮均未验证 |

## 需要优先复现的候选路径

入口模型通过 GJSON 提取，账单模型继续引用入口上下文。透传路径却会读取原始请求体，最终速度校验又可能使用 map 解析请求体。因此，重复 model、大小写别名及转义别名需要在真实链路中逐层记录。

普通转发会重设 DTO 模型；加速报价还会核对最终模型。这些既有约束可能阻断部分组合。本轮没有运行 GJSON、项目 DTO 或最终发送函数，不能将源码候选写成已证实的“便宜授权、昂贵转发”。

身份候选也须分开：重复 Authorization、跨提供商凭据、WebSocket 子协议、query key 不是同一种输入。当前目录路由回归只检查所选认证身份和后一个请求的连续性，不能代替上游凭据清理验收。

特别注意：令牌 IP 限制会调用 ClientIP。X-Forwarded-For 是否被信任取决于真实代理配置。本轮未检查完整 trusted-proxy 配置、也未运行带 IP 限制的 Key，因此不能宣布“所有转发头都不会影响授权”。

## 已执行的原始 HTTP/1.1 结果

测试文件：`scripts/security-audit/rt17-wire_test.go`。全部监听与连接均限定为本机回环地址，不接受目标 URL 参数。虚拟 Host 为 `rt17.invalid`，请求身份、模型及内容均为无害标记。每个场景主动发送 A、B 两条请求；拒绝非法首条报文时连接可终止。

| 输入 | 实际响应状态 | 实际处理器调用次数 |
| --- | --- | ---: |
| 普通连续请求、相同 Content-Length、合法 chunked | 200 / 200 | 每个场景 2 |
| 冲突 Content-Length、逗号合并长度、重复 Host | 400 | 每个场景 0 |
| 重复 Transfer-Encoding | 501 | 0 |
| Transfer-Encoding 与 Content-Length 同时存在 | 200 / 200 | 2 |
| 重复 Authorization、重复查询参数 | 200 / 200 | 每个场景 2 |
| 编码路径、编码斜杠、双重编码斜杠、大小写及尾斜杠 | 200 / 200 | 每个场景 2 |
| 转发与追踪头 | 200 / 200 | 2 |

**这里计数的是标准 HTTP 测试处理器，不是实际 relay 或上游。** 对同时存在长度与传输编码的报文，直接 Go 服务的本地结果没有显示额外报文；这既不证明请求走私，也不证明代理链安全。Gin 是否匹配这些路径并未由本测试回答。

接受的首条请求保持 BODY_A / TEST_A，后条保持 BODY_B / TEST_B 和 URI_B，没有观察到它们在该处理器之间串用。重复身份字段及查询值仍可在原始多值结构中看到；Header.Get 读取首值。协议允许的相同长度、普通转义和大小写变化没有被一律标记为攻击。

本机标准 JSON 解码的补充结果：

| 输入字段 | 标准 struct 的 model | 标准 map 的 model |
| --- | --- | --- |
| model=a, model=b | b | b |
| model=a, Model=b | b | a |
| model=a, mo\\u0064el=b（JSON 转义名称） | b | b |

唯一的转义字段名也保留正常对照。上表是 Go 1.23.2 标准库观察，不是项目 GJSON 或 DTO 的实测结果。

实际命令（仓库根目录；独立传输测试不需要项目依赖）：

```sh
GO111MODULE=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  RT17_EVIDENCE_DIR="$PWD/rt17-evidence" \
  go test -race -count=1 -v ./scripts/security-audit
```

本次两次运行均通过，最终日志耗时 1.056 秒。不要用此结果替代项目的 Go 1.25.1 构建结果。完整原始请求和观察保存于交付包 `evidence/wire-observations.json`；逐案 CRLF 报文在 `evidence/raw-http/`，测试输出在 `evidence/wire-test.log`。这些是本地测试数据，不包含线上 Key。

## 新增但未执行的应用回归

`apps/api-go/middleware/rt17_request_ambiguity_test.go` 调用真实 getModelFromRequest 与真实 UnmarshalBodyReusable。模型/分组含混输入允许拒绝；若接受，则两个阶段必须相同。正常模型、唯一转义字段及嵌套无关字段必须成功，不能通过拒绝所有请求使测试变绿。

`apps/api-go/router/rt17_request_ambiguity_test.go` 使用既有隔离数据库 fixture、真实 SetRelayRouter 和 TokenAuth，不替换路由处理器。它为两个普通测试用户生成本地 Key，在实际 TCP 连接上发送两条 `/v1/models` 请求，记录认证前后头、用户及状态，并核对数据库中余额、Key 额度与角色未变化。它包含普通身份、混合大小写 Bearer、重复头/查询、提供商凭据冲突、路径编码、伪造追踪/身份头和普通 Key 指定渠道后缀。

该路由测试仅为只读模型目录，不是付费 relay。上述数据库检查是已编写的断言，**本轮未执行，也没有据此产生数据库或资金通过结论**。

在具备完整基线、匹配 Go 工具链、已缓存依赖且阻断外部网络的隔离副本中，待运行命令是：

```sh
cd apps/api-go
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -race -count=1 -run '^TestRT17' ./middleware ./router
```

这些新增不变量可能在基线失败。没有反转断言来保留解析分歧，也没有声称这些文件已经编译成功。

## 数据影响、代理矩阵与剩余验收

已执行部分没有应用数据库、有效 API Key、真实上游或资金操作。未创建业务余额、未写业务流水。**金额影响和实际上游调用次数均为未验证，不是“验证为零”。**

| 链路 | 状态 |
| --- | --- |
| 原始 HTTP/1.1 → 本机 Go 1.23.2 标准服务 | 已执行 |
| 原始请求 → 固定基线 Gin/认证/目录数据库 | 回归已写，未编译/未运行 |
| 模型解析 → DTO → 分组授权 → 计价 → 上游 → 账单 | 未执行，不能验收 |
| HTTP/1.1 → 部署对应 nginx → 目标 Go | 未验证 |
| HTTP/2 → 部署对应 nginx → HTTP/1.1 → 目标 Go | 未验证 |
| WebSocket/realtime、重复 form/multipart、解压后长度、速度冲突、受限 IP Key | 未执行 |

本机存在 nginx 1.26.3，但没有部署对应的完整配置、include 与版本证据；没有拿默认 nginx 配置冒充部署复制环境。没有向任何生产边缘、公共代理或第三方接口发送这些畸形报文。

正式完成仍需：在封闭环境用真实转发路由及可计数的本机上游，分别验证普通/加速和透传/转换；记录授权模型、计价模型、最终请求模型与两条请求各自的账单；再运行对应代理与 HTTP 版本组合。对含混输入必须拒绝或全程一致，并保持正向对照。

## 提交和交接

这是 test-only Draft，不应直接合并为安全修复。没有改动公共认证入口，也没有发送通知或提及负责人。若后续需要拒绝冲突凭据，须先与上一轮账户/后台授权负责人协调 auth.go 的修改；不能把目录优先级当作所有传输协议的授权规则。

代码检查期间 main 曾前进到 `44b697739519937579360a58e99d41de3c01a770`，与固定基线相差四个提交。已比较的变化未涉及本轮读取的转发文件及工作流；本测试分支仍从指定基线开始，不把较新 main 当作已经验收。

创建 Draft 前逐个读取了基线全部 27 个工作流的 on 块，均只有 workflow_dispatch。没有修改工作流或触发远程 CI，没有执行发布、部署或合并。
