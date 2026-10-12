# RT16：本机 Markdown 浏览器回归线束

来源：从冻结的 `origin/main` 提交 `3948a0a1ee1cefdd016a7aa45a11851545bb4b85` 恢复，并适配当前 `wip/rust-core-go-extensions` 的 `apps/web/src/components/ui/markdown.tsx`。

**当前范围：只验证共用 Markdown 组件和本机线束。** 它不是保存内容端到端验收，不创建账号，不连接数据库，不跨账号读取，不访问生产服务，也不证明商品、客服、个人资料或助手历史路径已经安全。

## 线束边界

| 文件 | 作用 | 不代表什么 |
| --- | --- | --- |
| `apps/web/scripts/rt16-markdown-entry.tsx` | 隔离浏览器入口，导入真实 `Markdown` 和 DOMPurify，记录两次真实清理、最终 DOM、链接属性和合成执行标志 | 不加入应用入口，不保存或读取应用数据 |
| `browser_probe.py` | 只监听 `127.0.0.1` 随机端口，阻止外部请求、非 GET 请求、WebSocket 和 Service Worker，运行合法对照与可选私有样本 | 不绕过浏览器或管理员策略，不访问外网，不替代登录用户流程 |
| `link-classification.test.mjs` | 读取当前真实 `apps/web/src/components/ui/markdown.tsx` 中的 `isExternalUrl` 并检查 URL 分类 | 不执行 DOMPurify、React、浏览器或脚本执行 |
| `test_browser_probe.py` | 检查私有 corpus 输入格式与对照要求 | 不测试产品代码或浏览器 |

当前 Markdown 顺序仍是：Marked/公式/图表生成 HTML → DOMPurify 片段清理 → 外链属性补充 → DOMPurify 最终清理 → React HTML 展示。合法公式、图表和链接属于组件显示对照，不是全站视觉验收。

链接分类测试默认读取：`../../../apps/web/src/components/ui/markdown.tsx`（从 `scripts/security/rt16/` 回到仓库根目录后进入 `apps/web`）。如需对比私有摘录，可用 `RT16_MARKDOWN_SOURCE=/absolute/path/to/excerpt.ts`，但不能把摘录结果声称为当前完整应用结果。

## 2026-10-12 本机结果

工具版本：Node `v26.10.0`，Bun `1.3.14`，Python `3.14.7`，tsgo `7.0.0-dev.20260707.2`。非 Rust 命令均使用 `MISE_DISABLE_TOOLS=rust mise exec -- ...`，避免触发 Rust 工具安装。

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 链接分类 | `MISE_DISABLE_TOOLS=rust mise exec -- node --test scripts/security/rt16/link-classification.test.mjs` | 16 项通过，0 失败，0 跳过；Node 报告 `stripTypeScriptTypes` 实验性警告 |
| 线束输入校验 | `MISE_DISABLE_TOOLS=rust mise exec -- python3 -B -m unittest discover -s scripts/security/rt16 -p test_browser_probe.py -v` | 3 项通过 |
| 实际 Markdown 入口构建 | `MISE_DISABLE_TOOLS=rust mise exec -- bash -lc 'cd apps/web && bun build scripts/rt16-markdown-entry.tsx --target browser --outdir "$TMP/bundle"'` | 成功；生成 `rt16-markdown-entry.js` 和 `rt16-markdown-entry.css` |
| 入口类型检查 | `MISE_DISABLE_TOOLS=rust mise exec -- bash -lc './node_modules/.bin/tsgo -p /tmp/lmm-rt16-tsconfig.json --pretty false'` | 成功；临时 tsconfig 在仓库外，未改项目配置 |
| 浏览器探针 | `MISE_DISABLE_TOOLS=rust mise exec -- python3 -B scripts/security/rt16/browser_probe.py --bundle "$TMP/bundle" --output "$OUT"` | 退出 2，`BLOCKED`；原因：`No module named 'playwright'`。未安装依赖，未绕过策略，未执行浏览器样本 |

浏览器探针结果文件写到私有 `/tmp/lmm-rt16-probe.*/result.json`，记录 `stored_paths.product/support/profile/assistant` 全部为 `NOT_RUN`，`server_requests` 为空，`cases` 为空。

## 本机组件探针用法

先在经批准、允许本机浏览的隔离环境准备完整检出、锁定依赖、Bun，以及 Playwright。遇到管理员策略拒绝必须停止。不要更换策略、调用生产站点、远程 CI 或外网服务来取得通过结果。

```sh
# 仓库根目录；不自动安装依赖，不启动产品服务。
MISE_DISABLE_TOOLS=rust mise exec -- node --test scripts/security/rt16/link-classification.test.mjs
MISE_DISABLE_TOOLS=rust mise exec -- python3 -B -m unittest discover -s scripts/security/rt16 -p test_browser_probe.py -v
private_dir=$(mktemp -d)
chmod 700 "$private_dir"
MISE_DISABLE_TOOLS=rust mise exec -- bash -lc "cd apps/web && bun build scripts/rt16-markdown-entry.tsx --target browser --outdir '$private_dir/bundle'"
MISE_DISABLE_TOOLS=rust mise exec -- python3 -B scripts/security/rt16/browser_probe.py \
  --bundle "$private_dir/bundle" \
  --corpus /absolute/private/path/corpus.json \
  --output "$private_dir/results"
```

每个私有样本必须指定 `html/link/svg/math/flow/sequence/encoded` 之一作为合法对照。私有攻击探针、原始观察和后续可利用样本不得提交到公开仓库、Issue、日志或测试快照。

退出 0 只表示所提供的组件样本通过；退出 1 表示组件观察失败；退出 2 表示受阻、只有环境检查或只有合法对照。即使组件探针通过，真实存储、权限、预览、分享、导出及另一账号页面渲染仍未验证。
