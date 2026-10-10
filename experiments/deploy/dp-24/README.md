# DP-24：小集群 RPC 安全通信研究

**状态：研究补丁；未通过完整 Rust–Go 集群验收。禁止直接启用跨机生产 RPC。**

起点：`wip/rust-core-go-extensions@72667564c0431754d4856dc2e0db55f360bd2745`。
本地分支：`research/dp-24-cluster-rpc`。目标分支：`wip/rust-core-go-extensions`。关联 #675。

## 交付边界

本目录提供可重复的本机隔离试验。服务端是 **Python gRPC 试验程序**；身份与扣款记录是 **SQLite 测试数据**。
Go 探针确实使用 TLS 1.3、HTTP/2 和既有 `Capabilities` 消息字节，但不是项目的生成式 grpc-go 客户端。
这些结果不等于 Rust 核心、真实账本、真实多机或 1c1g 容量验收。

新增 `apps/lmm-extensions/internal/coreclient/transportpolicy/` 是仅使用 Go 标准库的身份校验模块：
它校验专用 CA、服务器名称、服务 URI、完整证书 SHA-256、证书时效及有限期策略。
重新使用连接时也必须重新检查策略。无效或回退的策略替换会停止新调用。
该模块**没有接入现有客户端或 Rust 监听器**，不会改变当前 Unix socket 路径。

`REPORT.md` 是实测结果与未完成项。`HANDOFF.md` 是接入方案及协议交接。
`DRAFT-PR.md` 仅是离线 PR 文案，**不是已创建的 GitHub PR**。

## 本机执行

需要 Linux 网络与挂载命名空间、`ip`、`tc`、`unshare`、`nsenter`、`timeout`、Go、Python，
以及本地已安装的 `grpcio`、`cryptography`、`psutil`。脚本不安装依赖、不下载代码、不运行 Docker 或远程 CI。
本次工具版本见 `evidence/environment.json`。未使用仓库要求的完整 Rust/Go 工具链。

在仓库根目录运行：

```sh
bash experiments/deploy/dp-24/run.sh /tmp/dp24-results
```

输出目录应为本轮专用目录。日志、测试二进制和本地编译缓存只写到该目录。
首次编译有单独的 90 秒上限；隔离试验有 180 秒总上限。
退出码 `0` 仅表示此试验程序的已定义场景通过；**仍不表示整体验收通过**。
退出码 `1` 表示失败；`2` 表示环境阻塞；`124` 表示超时。
本轮退出码为 **2**，因为内核没有 `netem`，没有把受阻项算作通过。

先在外层离线编译带数据竞争检查的 Go 探针，再清空环境变量，进入全新的隔离网络。
试验只建立两个固定的私有 `/30` 测试网段，没有默认路由。
服务节点 A、B 和请求侧各处于不同网络命名空间。Go 探针另外进入隐藏核心文件的挂载命名空间。
直接执行 `lab.py orchestrate` 会被拒绝；它要求外层命名空间标记和只有回环接口的新网络。
所有证书均为本轮生成的测试证书；CA 私钥不落盘，临时叶证书和数据库在退出时清除。

## 已有源码事实

在该起点：

- Rust `src/internal_rpc.rs` 只绑定私有 Unix socket。该监听器最多 8 个连接、8 个在途调用，消息 64 KiB，服务期限 2 秒。
- Rust 只注册 `CoreControl` 与 `CoreEvents`。付款包装代码存在，不代表 Rust 已注册付款服务。
- Go `coreclient/client.go` 只使用 Unix socket；最多 32 个在途调用、2 秒期限，明确禁用普通重试与外来服务配置。
- 服务令牌在启动时读取。用户凭据放在单独的元数据字段中，服务令牌不能代替用户授权。
- 会话和 API Key 是数据库保存摘要的随机凭据，不是每个实例重新生成密钥的 JWT。

精确源码路径、提交与对象摘要在 `evidence/source-provenance.json`。没有修改 Protobuf、数据库权限、工作流或部署文件。

## 安全使用限制

不要把试验的 `dp24.lab.Probe` 方法暴露给用户；它们不是产品协议。
测试令牌是公开测试常量，不能用于部署。
两个 Python 节点共享本机内核与文件系统；这不是两台服务器。
Go 文件不可见试验只证明该探针没有读取核心文件，不证明拥有挂载管理权限的恶意进程无法突破隔离。
生产部署仍需独立数据库角色、容器挂载边界、去掉额外系统权限及网络拒绝规则。
