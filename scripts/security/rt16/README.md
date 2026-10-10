# RT16：保存内容的浏览器安全验证

关联审计：#711。工作分支：`test/rt-16-stored-content`。
源码基线：`2f4164978cf27f6e0e605b0589fbe631aef2c441`，不是 Rust 重构分支。

**当前状态：部分完成，浏览器验收受阻。没有证明存在可利用漏洞，也没有证明安全。**
本目录提供本机检查入口，不提供账号、生产连接、存储替身或已通过的端到端结果。
本提交不修改产品代码。没有证实需要修复的问题，因此没有修复提交。
以后有证据支持的修复必须另行提交。

## 已核对的边界

| 路径 | 源码事实 | 本轮实际验证范围 |
| --- | --- | --- |
| 商品说明及商家预览 | `StoreProductPage` 将 `product.description` 交给共用 `Markdown`；预览与公开查询不同 | 读取调用方；未保存、未读取数据库、未运行页面 |
| 商店客服 | `StoreSupportChat` 将 `message.body` 放入 React 文字节点，不调用 Markdown | 读取发送、历史查询与展示代码；未执行买家到卖家的保存/读取 |
| 助手输出 | `ai-elements/response*` 使用另一套结构化渲染；公式当前显示为文字代码，`openui` 有独立组件 | 读取部分组件；未完成历史、分享、导出和后台读取链路 |
| 个人展示与分享 | 找到 `profile_share*` 和服务器 SVG 生成入口，不可用 Markdown 测试代替 | 入口盘点未完成；未运行保存、分享或 SVG 浏览器检查 |

共用 Markdown 的源码顺序为：Marked/公式/图表生成 HTML → DOMPurify
清理为片段 → 补充外链属性 → DOMPurify 再次清理 → React HTML 展示。
第二次清理已经存在，不应作为本轮新修复。合法公式、图表和链接没有被删除。

参考：[Markdown](../../../apps/web/src/components/ui/markdown.tsx)、
[商品页面](../../../apps/web/src/features/store/product-page.tsx)、
[客服页面](../../../apps/web/src/features/store/support-chat.tsx)、
[客服数据规则](../../../docs/merchant-store-support.md)。
客服原文是加密保存的。后续数据库验证须区分密文与经应用解密的内容，不能把
密文字符串直接当作页面原文。管理员身份也不自动取得买卖双方的私人对话权限。

审阅时的 Markdown Git blob 为 `456ec6f190ad7020fd6b4c194f2a75a9e8b0d92b`。
Web 清单固定 DOMPurify `3.4.16`。现有部分交互测试替换了 Markdown 组件；
既有 `store-pickup-review.mjs` 使用模拟 API 响应。这些不能作为真实保存链路证据。

## 2026-10-10 的实际结果

| 检查 | 结果 | 不代表什么 |
| --- | --- | --- |
| `isExternalUrl` 分类检查 | Node 22.16.0：16 项通过，0 失败、0 跳过 | 不执行 DOMPurify、React 或浏览器；不是脚本执行测试 |
| 测试工具的输入检查 | Python unittest：3 项通过 | 只检查样本格式和对照要求，不测试产品 |
| 新 TSX 入口语法 | TypeScript 5.8.3 `transpileModule`：无语法诊断 | 未解析依赖，未作完整类型检查或构建 |
| 实际 Chromium 本机探针 | Chromium 144.0.7559.96、Playwright 1.57.0：`BLOCKED`，退出 2 | 本机对照页也未加载，不能报告攻击被清理 |
| 四类真实保存后跨账号读取 | **0/4 完成，全部 NOT_RUN** | 没有数据库原文、清理结果及最终 DOM 的联合证据 |

最后一次浏览器尝试为 `2026-10-10T14:22:45Z`。正常导航到本机对照页时返回
`net::ERR_BLOCKED_BY_ADMINISTRATOR`，本机服务器请求数为 0；未更改或绕过策略。
15 个私有探针虽已通过输入检查，但未执行任何一个浏览器样本。

本机克隆因 GitHub 主机名无法解析失败；源码通过已连接的 GitHub 工具读取。
链接分类测试实际使用从上述基线读取的完整函数摘录，而非完整应用检出。
默认测试入口则直接读取检出的真实源文件。没有将函数摘录或其他版本伪称为完整 main。
本机没有 Bun、应用依赖或隔离应用数据库。应用完整构建、lint、类型检查、
仓库文档检查、真实数据库测试以及其他浏览器均未运行。

实际命令（本机私有目录在仓库之外）：

```sh
RT16_MARKDOWN_SOURCE=/mnt/data/rt16/private/markdown-isExternalUrl.excerpt.ts \
  node --test scripts/security/rt16/link-classification.test.mjs
python -B -m unittest discover -s scripts/security/rt16 -p test_browser_probe.py -v
node --check scripts/security/rt16/link-classification.test.mjs
python -B scripts/security/rt16/browser_probe.py \
  --executable /usr/bin/chromium \
  --corpus /mnt/data/rt16/private/corpus.json \
  --output /mnt/data/rt16/private/final-browser-run
```

## 本机组件探针的用法与限制

以下构建命令**本轮未运行**。先在经批准、允许本机浏览的隔离环境准备完整检出、
锁定依赖、Bun，以及 Playwright 1.48 或更新版。遇到管理策略拒绝必须停止。
不要更换策略、借生产服务或调用远程 CI 来得到通过结果。

```sh
# 仓库根目录；不自动安装依赖，不启动产品服务。
node --test scripts/security/rt16/link-classification.test.mjs
private_dir=$(mktemp -d)
chmod 700 "$private_dir"
(cd apps/web && bun build scripts/rt16-markdown-entry.tsx \
  --target browser --outdir "$private_dir/bundle")
python -B scripts/security/rt16/browser_probe.py \
  --bundle "$private_dir/bundle" \
  --corpus /absolute/private/path/corpus.json \
  --output "$private_dir/results"
```

`rt16-markdown-entry.tsx` 只用于此隔离构建，不加入应用入口。它导入真实 Markdown
和 DOMPurify，不替换它们的实现；记录解析后的 HTML、两次实际清理调用的输入/输出、
最终 DOM、链接属性、测试执行标志和测试状态。它不取得数据库内容。

每个私有样本必须指定 `html/link/svg/math/flow/sequence/encoded` 之一作为合法对照。
工具另运行七类公开合法对照。每次展示使用新的空浏览器上下文；这**不是**已登录的
保存者和读取者。只激活带测试标记的链接，不替代所有实际用户交互。

工具仅监听 `127.0.0.1` 的随机端口，不接受站点地址。它阻止外部请求、非 GET 请求
和 WebSocket，阻止 Service Worker，并观察本机测试信号。受阻的请求仍记录为尝试；
主动网络请求、测试秘密请求、执行标志变化和错误均不能记为通过。普通被动资源请求
单独记录；本工具不承诺清理器会禁止所有图片或 CSS 请求。

DOM 命名访问和重复 ID 是观察项，不单独等于应用漏洞。要证明覆盖安全对象，仍需
验证真实应用代码是否读取该对象及其结果。合成冻结状态也不是实际账户安全状态。

退出 0 仅表示所提供的**组件样本**通过；退出 1 表示组件观察失败；退出 2 表示
受阻、只有环境检查或只有合法对照。结果中的四类 `stored_paths` 始终是 NOT_RUN，
不会因组件结果而改变。即使此工具通过，仍必须补齐真实存储、权限、预览、分享、
导出及另一账号的页面渲染。合法对照的 DOM 节点检查也不是完整视觉验收。

## 私有材料

攻击探针、原始观察和后续可利用样本只能留在仓库外的私有交付包中；不得复制进
公开 PR、Issue、日志或测试快照。当前私有探针全部未执行，不是已复现漏洞。
确认问题后依照 `.github/SECURITY.md` 处理，不自动向外发送报告。
