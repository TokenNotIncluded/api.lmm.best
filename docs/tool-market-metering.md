# 平台可信计量

计量授权独立于 MCP 服务认证和市场审核。`billing_mode = "metered"` 不是允许工具自报账单的开关。
本协议允许平台拥有的采集器提供认证回执；它不能证明第三方自称的用量真实。
采集器只有直接持有上游调用或读取宿主机/云厂商的独立遥测后才能签名。
这次实现没有默认授权任何服务，也没有把现有 Jev MCP 的 `usage` 自动升级成可信回执。

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

示例中的占位密钥不能加载。缺失配置、格式错误、弱密钥、重复适配器或不安全权限都关闭用量计费。
免费/固定收费工具不依赖此文件。配置不对外提供密钥，公开服务详情仅返回可选计量单位。

接入顺序：先创建免费/固定价格草稿得到 service ID；运维配置对应服务、地址、工具和可计量单位；
作者再保存计量价格草稿。保存会生成新版本，运维须将配置绑定到这个版本及校验后的工具定义摘要，
之后独立审核者才可发布。即使是管理员作者也不能绕过独立审核或制造采集器授权。
更换地址、定义或版本必须重新绑定；撤销配置立即阻止新的执行及未验证结果入账。
已经验证并持久化的结果仍可依据调用快照完成一次结算。
所有后端/恢复节点必须读取相同授权策略；轮换密钥前排空未完成调用。

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
结果中的普通 `usage` 字段只作展示，不是账单凭证。输出与计量数据先持久化，恢复过程检查
`metering_verified`，旧的自报用量不能升级为可信数据。

## 边界

本功能实现计价、授权、认证回执和单次任务结算协议，未包含具体云厂商或 hypervisor 采集器。
在接入独立采集器前，新增的用量模式不能上线收费。
对 Jev 应让平台适配器直接执行 TypeSafe 官方请求并获取官方用量，再生成回执；不能仅签现有 MCP 自报的 `usage`。
持续运行 VM 的租约和周期账单未实现。45 秒同步调用范围内完成的任务才适用这套单次结算。
