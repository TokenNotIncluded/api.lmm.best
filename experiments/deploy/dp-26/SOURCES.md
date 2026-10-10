# 来源与可复查位置

所有仓库结论固定到 `72667564c0431754d4856dc2e0db55f360bd2745`，不是持续变化的分支 HEAD。以下地址仅用于只读源码和资料核对。

S1：Rust 公共路由不启用模型和计费，`router` / `unavailable`：
```text
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-core/src/http.rs
```

S2：Go 实际模块注册与本地在途限制，`Run` / `New` / `bounded`：
```text
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-extensions/internal/app/run.go
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-extensions/internal/modules/host.go
```

S3：MCP 重复解析、唯一字段检查及原始网络实现：
```text
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-extensions/internal/modules/toolmarket/mcp.go
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-extensions/internal/modules/toolmarket/schema.go
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-extensions/internal/modules/toolmarket/network.go
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-extensions/internal/modules/toolmarket/types.go
```

对应三份输入文件 Git blob SHA-1：
```text
mcp.go      b15f8540b7372052de0f8de96db23e9892cece85
schema.go   90d97e642f0097eddf373a45749381616d96697d
network.go  509bd77eb65fc3bbcec4803b191fcdfeab4f67c5
```
`prepare.py` 在提取前验证这些哈希，不接受仅文件名相同的内容。

S4：Go 最低版本及仓库检查配置。`go.mod` 要求 Go 1.25；基线工作流指定 Go 1.27.2 和 Rust 1.99.0。本次没有触发这些工作流，也没有将它们的测试当成本轮结果：
```text
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-extensions/go.mod
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/.github/workflows/core-protocol.yml
```

S5：Go 官方垃圾回收指南，GOGC、Memory limit、Latency 章节；2026-10-10 查阅：
```text
https://go.dev/doc/gc-guide
```

S6：Linux 官方 cgroup v2 文档，CPU / Memory 章节；2026-10-10 查阅：
```text
https://docs.kernel.org/admin-guide/cgroup-v2.html
```

S7：Rust 转发限制与持久预留/结算接口，`Limits::default`、`Billing`：
```text
https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/apps/lmm-core/src/relay/types.rs
```

S8：Linux 官方 zram 文档，逻辑容量、内存限制和统计；2026-10-10 查阅：
```text
https://docs.kernel.org/admin-guide/blockdev/zram.html
```

实验数字仅来源于本次证据包中的记录。外部文档用于定义和设计边界，不为本次性能收益背书。
