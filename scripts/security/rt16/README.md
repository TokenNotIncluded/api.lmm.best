# RT16：本机 Markdown 浏览器回归线束

移植来源：`origin/candidate-899` 的独立主题提交
`45f69b0834cdbc2d5d57d4dfd5e6b4616911beb9`（LIghtJUNction），原材料来自冻结的
traditional/main `3948a0a1ee1cefdd016a7aa45a11851545bb4b85`。保留 AGPL-3.0-or-later
署名，使用当前微服务 `apps/web/src/components/ui/markdown.tsx`，不恢复旧应用入口。

**范围仅为共用 Markdown 组件和本机线束。** 不保存或读取应用数据，不创建账号，
不连接数据库，不访问生产服务，不证明商品、客服、个人资料或助手历史路径安全。

## 文件与边界

- `apps/web/scripts/rt16-markdown-entry.tsx`：导入真实 Markdown 和 DOMPurify，记录
  两次真实清理、最终 DOM、链接属性和合成执行标志。不得加入产品入口。
- `browser_probe.py`：监听 `127.0.0.1` 随机端口，在独立无登录上下文运行显示对照
  与可选私有样本；阻止外部请求、非 GET 请求、WebSocket 和 Service Worker。
- `link-classification.test.mjs`：提取当前真实 `isExternalUrl`，仅检查 URL 分类，
  不执行 DOMPurify、React 或浏览器。
- `test_browser_probe.py`：仅检查私有 corpus 格式、合法对照和输入长度边界。

当前组件顺序：Marked/公式/图表 → DOMPurify 片段清理 → 外链属性补充 → DOMPurify
最终清理 → React HTML 展示。公式和图表对照不是全站视觉验收。

链接测试默认读取 `../../../apps/web/src/components/ui/markdown.tsx`，相对测试文件
解析。可用 `RT16_MARKDOWN_SOURCE=/absolute/path/to/excerpt.ts` 对比私有摘录；不得
把摘录测试结果称为当前完整应用结果。

## 本机用法

需要已安装的锁定依赖、Bun、Python Playwright 与 Chromium；脚本不会自动安装依赖、
启动产品服务或连接外网。遇到浏览器管理员策略拒绝必须停止，不更改策略取通过结果。

```sh
# 仓库根目录；只构建隔离组件入口，不构建整个应用。
MISE_DISABLE_TOOLS=rust mise exec -- node --test scripts/security/rt16/link-classification.test.mjs
MISE_DISABLE_TOOLS=rust mise exec -- python3 -B -m unittest discover -s scripts/security/rt16 -p test_browser_probe.py -v
private_dir=$(mktemp -d)
chmod 700 "$private_dir"
(cd apps/web && MISE_DISABLE_TOOLS=rust mise exec -- bun build \
  scripts/rt16-markdown-entry.tsx --target browser --outdir "$private_dir/bundle")
MISE_DISABLE_TOOLS=rust mise exec -- python3 -B scripts/security/rt16/browser_probe.py \
  --bundle "$private_dir/bundle" \
  --corpus /absolute/private/path/corpus.json \
  --output "$private_dir/results"
```

可用 `--executable /absolute/path/to/chromium` 指定已安装浏览器；省略 `--corpus` 只运行
合法对照，省略 `--bundle` 只做浏览器环境检查。缺少 Python Playwright 时结果为
`BLOCKED`，不能据此声称浏览器通过。

私有 corpus 是非空 JSON 数组，最多 100 项。每项包含唯一、非空、最长 80 字符的
字符串 `id`、最多 50000 字符的字符串 `markdown`，以及 `html/link/svg/math/flow/sequence/encoded`
之一的字符串 `control`。私有样本、原始观察和后续可利用样本不得提交到公开仓库、
Issue、日志或测试快照。结果目录权限为 0700，`result.json` 为 0600。

退出 0 仅表示提供的组件样本通过；退出 1 表示组件观察失败；退出 2 表示受阻、仅环境
检查或仅合法对照。真实存储、权限、预览、分享、导出及另一账号渲染仍需独立验收。
