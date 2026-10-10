# DP-24 实测报告

## 结论

**完整验收：未通过。研究层面的局部结果：32 项通过、0 项失败、1 项环境受阻。**

测试开始：2026-10-10T15:12:15+00:00。隔离试验用时 11.573 秒，不含编译与前置 Go 单元测试。
执行的是 Python gRPC / SQLite 试验和 Go 标准库 TLS/HTTP2 探针，不是 Rust 核心服务。
没有生产请求、真实秘密、远程 CI、发布、部署、推送、合并或真实服务器测试。

Go 的 5 个顶层单元测试通过，含 14 个拒绝状态子用例，启用数据竞争检查。
第一次独立执行时，`TestGoGRPCFixture` 因未设置隔离试验参数而跳过；
随后该同名测试在真实隔离网络内单独执行并通过，见 `evidence/go-fixture.log`。
没有把第一次跳过当作通过。

## 对照测量

下面是同一进程组的顺序小样本测量。都调用测试程序的 `Capabilities`，不是模型服务。
已复用的连接先就绪再计时。表中 p50 为中位数，p95 为报告使用的第 95 百分位样本估计。

| 项目 | 样本 | p50 / ms | p95 / ms |
|---|---:|---:|---:|
| 同机 Unix socket gRPC | 200 | 1.2117 | 3.1819 |
| 隔离网络、双向 TLS gRPC、复用连接 | 200 | 1.3765 | 2.7546 |
| TCP 建连 + TLS 1.3 握手 | 30 | 1.8880 | 2.8097 |

握手项使用 CPython/OpenSSL；它不是 Rust rustls 或 Go 握手性能。
没有随机交错对照、多轮置信区间或 1 核限制，不能由本表推出固定 TLS 开销、生产恢复承诺或集群容量。
`Capabilities` 很小，其吞吐也不能代替身份查询、事件消费或模型流吞吐。

A 的进程常驻内存为 113.793 MiB；B 为 112.699 MiB；
请求侧为 119.500 MiB。这是试验结束时的整个 Python 进程快照，不是峰值，
不是 TLS 增量内存，也不是 Rust/Go 每条连接成本。
虚拟地址空间、线程、文件描述符与累计 CPU 时间单独保存在 `resource_snapshot`。
未测共享页、文件缓存、容器总内存，也未施加 1 GiB 限制。**不能据此给出 1c1g 部署密度。**

## 直接观察

三组网络命名空间不同，均无默认路由；完整接口和路由见 `evidence/topology.json`。
无证书、过期证书、错误服务器名称均不能完成调用；同一 CA 签发但服务身份错误的证书也被拒绝。
重复认证字段、错误协议主版本、服务身份冒充用户授权、未允许方法均被拒绝。

有限重叠期内两份证书都能使用；重叠结束后旧证书被拒绝。显式撤销后，新证书在既有连接上也被拒绝。
服务令牌轮换同样有到期时间。证书自然到期后，新握手失败，已建立连接上的下一次调用也失败。
过期、损坏和版本回退的策略均不能继续放行。
这些是测试策略的行为；**现有 Rust 启动令牌还没有接上此轮换路径**。

测试会话和 API Key 在 A/B 上返回相同身份；撤销后，两条已建立连接均被拒绝。
将 A 的试验授权来源标为不可用时，A 返回不可用而不是用旧授权放行，B 仍能查询。
这是共享 SQLite 测试数据的结果，不是 PostgreSQL 跨核心会话撤销、读副本延迟或网络断库验收。

丢回复场景在 SQLite 提交后故意返回不可用。调用方在 B 查询原操作标识，并以同一标识重放。
最终只有 1 条操作记录，测试余额从 10000 减到 9983；相同标识更换金额被拒绝。
该结果只说明试验中“先查原记录、不换新标识”的规则可行，**不证明产品真实账本已防重复扣款**。
实际 Rust 付款服务尚未在该 RPC 监听器注册；其调用保持 `UNIMPLEMENTED`。

16 个并发调用中 8 个完成、8 个被拒绝；测得处理器峰值 8，完成后活动处理器归零。
超过 64 KiB 的消息被拒绝；短期限的慢调用取消后释放处理器。
这是测试 gRPC 服务的处理器限制；**没有证明 Rust 跨机 TLS 握手连接上限或 Go 生成客户端的全局队列上限**。

实际关闭 A 的虚拟网卡后，A 查询超时，B 的新请求仍成功。
已经输出的流保留 A 的节点标识，随后以 `DEADLINE_EXCEEDED` 结束，没有转移到 B。
恢复网卡后，下一次测试调用耗时 4.015 ms。
另一个场景终止 A，仅按 A→B 两次上限查询，测得切换调用耗时 4.989 ms。
这是一次受控查询的时长，不是自动发现耗时，也不是整个故障期间的恢复时长承诺。

B 从 51051 换到 51053 后，用同一受验证的服务器名称建立新连接成功，旧连接失败。
该操作由试验程序显式更新；**没有完成自动服务发现**。

Go 探针实际发出两次标准 `Capabilities` gRPC 帧，复用一条 TLS/HTTP2 连接；
撤销服务器身份后，下一次调用被客户端拒绝；服务端记录仅增加两次成功调用，未执行被撤销的第三次调用。
其挂载视图看不到 SQLite 文件与核心私钥，进程环境中没有数据库凭据。
这不是对有系统管理权限的恶意扩展的隔离证明，也没有授予或测试 PostgreSQL 角色。

## 受阻项与未完成项

`tc qdisc add dev dxa root netem delay 40ms loss 10%` 返回：

```text
Error: Specified qdisc kind is unknown.
```

因此随机丢包与 40 ms 网络延迟组合场景记为 **blocked**，进程退出码为 **2**。
网卡断开试验不是随机丢包试验的替代结果。处理器 Sleep 也不是网络延迟注入。

尚未完成：Rust mTLS 监听与实际 Go grpc-go 接线、指定工具链的整仓编译、真实 PostgreSQL 身份及资金结果查询、
两台独立服务器、新旧实际二进制共存、自动发现、证书根切换、带单调时间保护的策略过期、
重启后的策略版本持久化、旧连接有界退出、TLS 握手连接硬上限及 1c1g 资源预算。
不能据此批准跨机上线或资金重试。

## 全部场景索引

机器结果包含每项返回码、时间、断言结果和资源快照；此表不替代原始记录。

| 场景 | 状态 |
|---|---|
| `three_isolated_network_namespaces` | pass |
| `mTLS_and_connection_reuse` | pass |
| `plaintext_rejected` | pass |
| `missing_client_certificate_rejected` | pass |
| `wrong_server_name_rejected` | pass |
| `expired_client_certificate_rejected` | pass |
| `valid_CA_wrong_service_identity_rejected` | pass |
| `service_identity_is_not_user_authority` | pass |
| `duplicate_authorization_rejected` | pass |
| `wrong_protocol_major_rejected` | pass |
| `unknown_method_is_not_enabled` | pass |
| `same_session_and_key_across_nodes_fixture` | pass |
| `user_key_revocation_across_reused_channels_fixture` | pass |
| `disconnected_authority_does_not_use_cached_grants_fixture` | pass |
| `bounded_certificate_overlap_and_explicit_revoke` | pass |
| `bounded_service_token_rotation` | pass |
| `certificate_expiry_on_existing_TLS_connection` | pass |
| `exact_service_method_allowlist` | pass |
| `expired_policy_lease_fails_closed` | pass |
| `malformed_and_rollback_policy_denied` | pass |
| `lost_reply_same_identifier_receipt_and_dedup_FIXTURE_ONLY` | pass |
| `message_limit` | pass |
| `eight_global_calls_and_resource_recovery` | pass |
| `deadline_and_handler_release` | pass |
| `real_netem_delay_and_packet_loss` | blocked |
| `partial_link_down_and_no_stream_migration` | pass |
| `node_down_bounded_query_failover_and_restart` | pass |
| `address_change_with_stable_verified_identity` | pass |
| `latency_UDS_grpc_fixture` | pass |
| `latency_mTLS_grpc_fixture` | pass |
| `TLS13_handshake_cost_fixture` | pass |
| `Go_mTLS_grpc_wire_and_core_credential_isolation` | pass |
| `resource_snapshot` | pass |

## 证据与来源

`evidence/results.json` 是本轮结果；`exit-code.txt` 是实际外层返回码。
`run.log`、Go 日志、服务器日志和网络拓扑均来自同一最终运行。
服务器日志中的 TLS 握手错误包括预期的拒绝测试，不表示它们被忽略为成功连接。
生成测试私钥、测试二进制和本地编译缓存不包含在提交或交付包中。
源码校验与运行文件摘要见 `source-provenance.json`，工具与内核版本见 `environment.json`。
