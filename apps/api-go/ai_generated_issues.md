# AI生成代码的典型缺陷分析

## 概览
Go代码库中发现 **305处** 未使用参数，主要集中在接口实现和错误处理函数。

## 严重程度分类

### 🔴 Critical - 功能缺失

#### 1. ClaudeErrorWrapper 丢弃错误码
**位置**: `service/error.go:61`
```go
func ClaudeErrorWrapper(err error, code string, statusCode int) *dto.ClaudeErrorWithStatusCode {
    // ...
    claudeError := types.ClaudeError{
        Message: text,
        Type:    "new_api_error",
        // ❌ code 参数未使用，硬编码为 "new_api_error"
    }
}
```

**对比**: 被注释的 `OpenAIErrorWrapper` 在第46行有 `Code: code`，证明这是明确遗漏。

**影响**: 所有调用方传入的细粒度错误码（如 `rate_limit_exceeded`, `invalid_api_key`）被丢弃，客户端无法区分错误类型。

---

#### 2. cacheIncrUserQuota 忽略配额增量
**位置**: `model/user_cache.go:156`
```go
func cacheIncrUserQuota(userId int, delta int64) error {
    // ❌ delta 参数未使用，直接失效整个缓存
    return invalidateUserCache(userId)
}
```

**对比**: `updateUserQuotaCache` 在第200行已经把第二个参数改成 `_`，但这个函数没改。

**影响**: 
- 注释声明"legacy delta-shaped helper only invalidates"，但函数签名仍暗示它会增量更新
- 调用方 `cacheDecrUserQuota` 传入 `-delta` 毫无意义（第167行）

---

### 🟡 Medium - 死代码

#### 3. WssAuth 空认证函数
**位置**: `middleware/auth.go:468`
```go
func WssAuth(c *gin.Context) {
    // 完全空实现
}
```

**状态**: 全代码库搜索无调用——是未清理的 stub，非活跃漏洞。

---

#### 4. FinalizeOAuthUserCreation 忽略邀请人
**位置**: `model/user.go:994`
```go
func FinalizeOAuthUserCreation(..., inviterId int) ... {
    // ❌ inviterId 未使用
}
```

**影响**: OAuth 用户创建时邀请关系未记录，可能丢失推荐奖励。

---

### 🟢 Low - 测试桩

#### 5. 测试接口实现
```
relay/channel/api_request_getbody_test.go:191 BuildRequestURL() unused: info
relay/channel/api_request_getbody_test.go:195 BuildRequestHeader() unused: c, info
service/protected_fetch_client_test.go:19 LookupIPAddr() unused: ctx
```

**原因**: 为满足接口而实现的最小测试桩。

---

## 根本原因

### 对比其他语言工具链

| 语言 | 工具 | 状态 |
|------|------|------|
| TypeScript (web) | `noUnusedParameters: true` | ✅ 编译器强制 |
| Rust (relay) | `clippy -D warnings` | ✅ CI 阻断 |
| Go (api-go) | `go vet` | ⚠️  不检查未使用参数 |

### AI 代码生成的特征

1. **签名先行，实现滞后**  
   LLM 先写函数签名（从上下文推断需要哪些参数），再填充函数体（可能从另一个上下文窗口生成）。两阶段不一致时产生死参数。

2. **缺乏增量验证**  
   人类写代码时编辑器会实时标注未使用参数；LLM 一次生成整个函数，无中间反馈。

3. **过度泛化接口**  
   为"将来可能需要"预留参数（如 `WssAuth` 的 `c *gin.Context`），但实现永远不需要。

---

## 建议修复

### 立即行动
1. **修复 `ClaudeErrorWrapper`**: 添加 `Code: code` 到 `ClaudeError` 结构
2. **重命名 `cacheIncrUserQuota`**: 第二个参数改为 `_ int64`，或改名为 `invalidateUserQuotaCache`
3. **删除 `WssAuth`**: 无引用的空函数

### 工具链改进
```bash
# 在 CI 中添加
go vet -unusedparams ./...  # 需要 Go 1.23+ 或第三方工具

# 或使用 staticcheck
staticcheck -checks='U1000' ./...
```

### Code Review 检查清单
- [ ] 每个参数在函数体内至少使用一次
- [ ] 接口实现的未使用参数标记为 `_`
- [ ] 错误包装函数传递所有语义参数
