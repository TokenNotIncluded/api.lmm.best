# LMM 管理脚本

`scripts/lmm-admin.py` 是 Python 3.10+ 标准库客户端，不需要安装依赖或启动服务。
它只调用 Core 的 `/core/v1/admin/abuse` 接口，不连接数据库，也不自行授予管理权限。
这是新架构接口；旧部署不支持时会失败，不会回退到旧管理接口或直接改库。

认证需要有效的 Core **superadmin Session bearer**；普通 API-Key 不能管理用户。
脚本不负责登录、刷新 Session 或从浏览器导出凭证。先通过已经授权的登录流程获得
Session，再使用 `LMM_ADMIN_SESSION` 环境变量、`--credential-file` 私有文件或
`--token-stdin` 传入。没有 `--token` 参数。不要把凭证放进命令、URL、版本控制或日志。
私有文件必须属于当前用户、权限 0600 或更严格，并且不能是链接或管道。

连接地址必须是 HTTPS origin。隔离测试使用 HTTP 时，需要明确添加
`--allow-insecure-loopback`，且主机必须是 `127.0.0.1` / `::1` 等数字回环地址。
脚本禁用自动重定向和环境代理，使用系统默认 TLS 证书验证。网络单次 socket 超时
10 秒，响应体另有 10 秒读取期限，最后一次 socket 读取可能再等待最多 10 秒；
域名解析仍受操作系统解析器时限约束。每次响应最多 1 MiB，不重试写请求。

## 只读查看和私有导出

以下示例地址是不可用的保留域名。替换为你要管理的新架构实例。

```sh
python3 -B scripts/lmm-admin.py --server https://test.example.invalid --credential-file /private/session users list --state marked --limit 50
python3 -B scripts/lmm-admin.py --server https://test.example.invalid --credential-file /private/session users inspect 42
python3 -B scripts/lmm-admin.py --server https://test.example.invalid --credential-file /private/session policy get --out /private/policies.json
```

分页使用输出的 `next_after` 作为下一次 `--after`；每页最多 200 条，没有自动抓全库。
`users list --query-stdin` 从标准输入读取用户名或邮箱片段，最多 320 UTF-8 字节。
默认输出只包括用户 ID、用户名、状态、角色、标记 revision 和 policy ID，不输出邮箱。
`users list/inspect --out /private/users.json` 明确导出这一页/这一用户的邮箱，文件权限
0600；这些文件属于私有取证数据，不应提交。接口未提供 IP，工具不会捏造 IP 证据。
环境变量或私有凭证文件可与 query/reason stdin 结合；`--token-stdin` 不能同时读取它们。

## 批量标记、取消标记

每次明确列出目标 ID，不根据相似邮箱、邀请人或 IP 自动扩大处罚范围。
先建立服务器预览，读取 artifact 中的 `payload.request` 和 `payload.preview`，核对
目标、理由、前后状态、policy revision 和每个用户 expected revision。

```sh
python3 -B scripts/lmm-admin.py --server https://test.example.invalid --credential-file /private/session honeypot preview --action mark --user-id 42 --user-id 43 --policy-id isolated --reason-stdin --out /private/preview.json
python3 -B scripts/lmm-admin.py --server https://test.example.invalid apply --preview /private/preview.json
python3 -B scripts/lmm-admin.py --server https://test.example.invalid --credential-file /private/session apply --preview /private/preview.json --confirm REVIEWED_ARTIFACT_SHA256 --apply
```

第一条命令从标准输入读取审查理由，并返回 artifact 的 SHA256。第二条只检查本地
artifact，零网络请求、零用户变更，也不需要凭证。第三条必须同时提供 `--apply`
和审查过的完整 64 位 artifact SHA256；文件内容或 server origin 改了就拒绝。
仅凭这个 hash 不能证明管理员已审查，操作者仍须看清文件内容。

`--action unmark` 不带 policy ID。`mark` 不会重新启用被普通禁用的用户。
`--action convert_disabled` 是单独的明确操作：它会重新启用指定的普通禁用用户，
同时标记到蜜罐。服务器仍拒绝当前操作者、管理员及其他不允许的目标。
取消蜜罐不会恢复其他独立权限，也不重写已有资金流水。

用户预览有效期五分钟；policy/user 状态漂移返回冲突，不能自动重建预览并替人确认。
Core 保证一个批次原子生效，并保留不可变审计。脚本不实现普通禁用、奖励追回、
提现或余额修改命令：这些需要独立的正式接口与资金预览确认。

## 策略设置

编辑 `policy get --out` 导出的私有 config，然后生成**本地差异预览**：

```sh
python3 -B scripts/lmm-admin.py --server https://test.example.invalid --credential-file /private/session policy preview --file /private/policies.json --reason-stdin --out /private/policy-preview.json
```

输出明确写 `LOCAL_POLICY_DIFF`，不是服务器 policy preview。artifact 保存旧/新 config、
expected revision 和 operation ID；输出列出改变的 policy ID 及旧/新 config hash。
在线预览必须先读取并验证完整 v4 policy 响应，包括版本和全部 capabilities；固定文本
也不例外。缺少凭证别名集合或协议元数据时返回 `INVALID_RESPONSE`，不会生成预览，
也不会把“没有信息”当成“允许任意别名”。
客户端兼容原来的五字段数字回环能力，以及包含 `external_https`、`local_http`、
`allowed_origins` 的完整八字段能力；能力中的未知字段和不一致组合均拒绝。
新服务器即使只使用固定文本且没有别名，也须返回完整能力，不能省略为空值。
八字段预览还在私有 artifact 中保存实际服务器能力；确认时先查询原 operation，
若尚未执行，再回读能力、revision 和旧 config。任何变化都拒绝 PUT，需要重新审查。
已执行的原 operation 仍按原 config hash 核对，不重发。
能力回读不是服务器原子快照；GET 到 PUT 之间的主机能力变化仍由 Core 最终校验，
客户端不会把回读成功当成上游可达或请求已执行。
审查后使用相同的 `apply --preview ... --confirm ... --apply`，单次 PUT 由 Core 做
revision 比较。仍被用户引用的 policy 不能删除。

固定文本策略示例：

```json
{"version":1,"policies":[{"id":"isolated","name":"隔离流量","mode":"fixed_text","fixed_text":"Request processed.","public_models":["public-model"],"clients":{},"limits":{"requests_per_minute_per_user":10,"requests_per_minute_global":100,"max_concurrent_per_user":1,"max_concurrent_global":4,"max_input_bytes":65536,"max_output_bytes":65536,"max_output_tokens":256,"output_bytes_per_second":0,"total_timeout_seconds":30}}]}
```

`custom_upstream` 模式省略 `fixed_text`，`clients` 必须分别配置 `openai_chat` 和
`openai_responses`。每项使用 `base_url`、`upstream_protocol`、`model`、
`credential_reference`；上游协议支持 `openai_chat`、`openai_responses`、
`anthropic_messages`、`gemini`，允许转换。原五字段能力仍只允许数字回环 HTTP。
完整八字段能力可明确允许外部 HTTPS，但 URL 的标准化 origin 必须精确出现在该
`credential_reference` 的服务器白名单里；同一域名的其他端口、子域名或其他别名
不能借用授权。HTTP 和 HTTPS 数字回环还必须单独允许 `local_http`；DNS 名称不能
用这个开关获得 HTTP 权限。客户端只接受 ASCII DNS 名称或标准数字 IP，规范化
大小写、默认端口和 IPv6；不接受 URL 中的凭证、查询、片段或反斜杠。
客户端不解析上游 DNS，也不替代 Core 的全部地址校验、固定连接、TLS 和重定向限制。
完整结构但已不在当前白名单的旧 config 仍可读取、导出和作为 before 快照，以便
移除旧路由；只有待提交的新 config 获得当前能力授权。没有实际服务器能力时，
离线校验不会接受外部 HTTPS。不能配置本地文件路径或明文 token。
`credential_reference` 只能引用服务器预加载的别名，名称为 1–64 位 ASCII 字母、
数字、`_`、`.` 或 `-`。策略名、公开模型名和上游模型名拒绝 Unicode 控制字符，
理由不得含 NUL；本地预校验与实际 Core 的这些输入限制一致。
`public_models` 明确列出给这些用户展示的模型，不读取真实私有分组模型。

这些 limits 只控制蜜罐模型请求，包括每用户/全局每分钟次数、并发、输入/输出字节、
输出 token、输出字节速率及总超时；注册、登录和其他访问频率属于不同设置。

## 不确定的写结果

apply 在写之前查询同一 operation ID；已记录就返回原结果，不再次写。
写后网络断开、5xx、代理未确认的 4xx 或响应损坏时，只查询一次原 operation，不重放 POST/PUT。
仍不能确认时退出码为 3，并打印原 operation ID：

```sh
python3 -B scripts/lmm-admin.py --server https://test.example.invalid --credential-file /private/session operation ORIGINAL_OPERATION_ID
```

不要为不确定结果新建 operation 或换一批目标重发。用户结果还会核对完整 ID、
revision+1 与预览的后状态。政策结果必须同时含 `applied_config_hash`，按递归键排序、
紧凑 UTF-8 JSON、无尾换行的 config 计算 SHA256；必须与 artifact 新 config 匹配。
缺失/错误格式属于未知结果，不匹配属于冲突；两者都不会再次 PUT。匹配时输出
`applied_config_match_verified:true`，证明原操作应用的 config 与审查文件一致，
不能证明此后政策没有被别的操作修改。operation 不含理由的原始 request hash，
复用/恢复仍写 `exact_request_match_verified:false`，不能证明理由逐字一致。
普通输入/权限/冲突错误退出码为 2；0 也可能表示只读或 dry-run，不代表已部署或改动。

## 检查

```sh
python3 -B scripts/test_lmm_admin.py
```

这些检查只覆盖合成回环 HTTP、凭证文件、artifact、协议校验和未知结果处理。
它们不替代真实 Core/PostgreSQL 权限、审计、并发、蜜罐传输和浏览器验收。
