# 工具市场用量计费

市场默认接受 MCP 工具提供方上报的用量。`tool_reported` 表示自报数据经过格式和额度校验，
不表示平台独立确认了实际成本。上游 API key 由 MCP 提供方保管，市场不要求获取供应商 key。
用户按已审核版本的价格授权调用，并可针对具体账单举报；管理员确认虚假账单后停用服务。

## 自报用量格式

推荐将整数用量放到 MCP 结果的 `_meta.lmm_usage`：

```json
{
  "_meta": {"lmm_usage": {"cpu_core_milliseconds": 120000, "memory_mib_seconds": 245760}},
  "structuredContent": {"status": "completed"}
}
```

也接受结果顶层 `usage`、`structuredContent.usage`，或文本内容中 JSON 对象的 `usage`。
Jev 的文本 JSON `usage.input_tokens` 可直接结算，不需要适配器或计量签名。
同一结果有多个用量来源时，所有来源必须给出完整、相同的计费数量，不能拼接不同来源。
缺失、null、负数、非整数、重复 JSON 键、超过上限或相互矛盾的用量拒绝扣费，并释放预留款。
只读取价格快照中要求的单位；工具返回的单价、总费用和成本字段不能改变账单。
签名回执若存在则必须通过验证，错误签名不会退回自报模式。

调用前按每项上限冻结款项，成功后逐项按 `ceil(数量 × rate_quota / 单位尺度)` 相加结算，
退回预留差额。明确为零的用量结算为零。失败不扣费；请求标识去重，重复查询不会重复执行或转账。
所有单价为钱包整数 Credit，USD 单价必须按永久 `CreditsPerUSD` 锚点转换，不能采用旧 QuotaPerUnit 或当前汇率。
Jev 定价目标为官方输入单价 $0.042/M 的 10 倍，即 $0.42/M；输出不收费。
单价只配置在价格字段，不写进工具描述。此文档不自动修改已发布版本价格。
`scripts/jev-market-pricing.py` 从市场 `/config` 的 `credits_per_usd` 读取固定锚点，
生成 10 倍价格草稿；`--save-draft` 仅保存草稿，不自动发布或跳过独立审核。
使用 `--service-id`、`--auth-file`（权限 0600 的服务作者平台令牌文件）和 `--output`。
这里使用的是平台登录令牌，不是 TypeSafe key。未部署自报计费协议时脚本拒绝保存。
当固定锚点为 3359744 时，$0.42/M 对应 1411093 个 Credit/M，65536 token 最大预留 92478 个 Credit。
整数 Credit 精度带来的不足一个 Credit 的价格舍入是向上取整。

账单保留用量来源、用量、原始上报对象、当次价格快照和结果摘要。
原始结果仅保留一小时；账单和举报证据独立保留，不依赖原始结果仍可查看。
举报只能由该调用的付款用户提交，每笔调用最多一份，重试相同原因返回原记录。
管理员和商家不能审核自己参与的账单。确认后服务状态为 `suspended`，提供方不能自行恢复；
已结算余额不会在举报时被静默改写或自动退款，资金争议需另行处理。

支持输入/输出 token（每百万）、字符（每千）、图片（张）、音视频毫秒（每秒）、
CPU 核毫秒（每核秒）、内存 MiB 秒（每 GiB 秒）、GPU/VM 毫秒（每秒）、
存储 MiB 秒（每 GiB 小时）。可以组合多个单位，全部适用一次调用的费用上限。
持续运行 VM 的租约和周期账单未实现；这里只结算一次任务，远程同步调用最长 45 秒。

## 可选的平台验证回执

平台控制的独立采集器仍可签名，结果来源标为 `platform_verified`。
这不是普通商家的接入条件，也不是要求商家提供上游 key 的方式。
独立采集器必须直接执行上游请求或读取独立遥测，不能给任意工具自报数据签名。

## 配置

设置 `LMM_TOOL_MARKET_METERING_CONFIG` 为绝对文件路径，例如 `/etc/lmm-api/tool-market-metering.json`。
文件必须是非符号链接的普通文件，只有一个硬链接，不超过 64 KiB，权限为 `0600` 或更严格；所有者为 root 或后端服务用户，由运维持有。
采集器签名密钥至少为 32 字节随机值，使用标准 Base64 编码。
不要把密钥给 MCP 发布者、放入 Tool 参数/描述、前端配置、Git 或日志。

```json
{
  "adapters": [
    {
      "id": "platform-vm-run-v1",
      "service_id": "exact-service-uuid",
      "version_id": "exact-reviewed-version-uuid",
      "endpoint": "https://platform-controlled.example/mcp",
      "tool_name": "vm_run",
      "remote_digest": "exact-validated-tool-definition-sha256",
      "metrics": ["cpu_core_milliseconds", "memory_mib_seconds"],
      "key_base64": "REPLACE_WITH_PRIVATE_RANDOM_KEY"
    }
  ]
}
```

示例中的占位密钥不能加载。缺失配置、格式错误、弱密钥、重复适配器或不安全权限都会拒绝对应签名回执。普通自报计费不依赖此文件。
免费/固定收费工具不依赖此文件。配置不对外提供密钥，公开服务详情仅返回可选计量单位。

可选采集器配置必须绑定精确服务、版本、地址、工具定义摘要和单位，所有验证节点读取相同策略。
未签名结果不需要这项授权。平台审核仍检查工具定义和版本，不能自行审核自己发布的服务。

## 调用与回执

平台在 `tools/call` 的 `_meta.lmm_metering` 发送：

```json
{
  "call_id": "request-specific-call-sha256",
  "version_id": "exact-version-uuid",
  "tool_id": "exact-tool-uuid",
  "input_digest": "canonical-arguments-sha256",
  "pricing_digest": "immutable-pricing-rules-sha256"
}
```

这些值由平台生成，不采用用户参数中的同名字段。采集器必须把该上下文绑定到自己执行/测量的任务。
上下文没有签名能力，不允许凭着它请求一个“签任意数字”的公开服务。
采集器入口必须认证平台调用方，并持久化 call_id 去重，将一次计量上下文只绑定到一次执行。
不得允许 MCP 发布者凭调用上下文反复触发不同任务并为同一账单签名。

最终 MCP 结果 `_meta.lmm_metering` 返回以下回执：

```json
{
  "adapter_id": "platform-vm-run-v1",
  "context": {"call_id": "...", "version_id": "...", "tool_id": "...", "input_digest": "...", "pricing_digest": "..."},
  "quantities": {"cpu_core_milliseconds": 120000, "memory_mib_seconds": 245760},
  "result_digest": "canonical-result-sha256",
  "signature": "hex-hmac-sha256"
}
```

`quantities` 必须恰好包含价格快照中的各项，整数值范围为 0 到各项上限。
结果摘要为 SHA-256：将最终 MCP 结果解码为 JSON，移除 `_meta.lmm_metering`；如果 `_meta` 已为空则移除 `_meta`，
再以 Go `encoding/json` 的紧凑排序对象键格式序列化。保留数字的 JSON 文本，遵循 Go 默认字符串转义。
模型实现 `ToolMarketMeteringResultDigest` 提供同一标准。
签名消息为 UTF-8 的 `lmm-tool-market-metering-v1\n`（实际换行），后接紧凑 JSON 对象，字段顺序为
`adapter_id`、`context`、`quantities`、`result_digest`。上下文字段按上面示例顺序，数量对象键按字典排序。
对消息执行 HMAC-SHA256，签名以 hex 编码。密钥仅在平台验证器和平台拥有的采集器之间共享。

签名覆盖实际结果，MCP 中间服务不能替换结果、改变参数/单价/数量，或拿其他调用的回执收费。
普通 `usage` 按默认市场自报策略结算，来源明确标为 `tool_reported`。
历史结果没有新的已校验来源或有效验证标志，恢复时不能自动升级成可计费数据。
