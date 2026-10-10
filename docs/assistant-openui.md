# 助手生成式界面

此功能在现有助手中增加 OpenUI Lang 回答。它使用官方 `@openuidev/react-lang`，不是另一套聊天服务。Go 继续负责模型路由、工具调用、账户权限、计费、历史记录和确认操作。React 只负责显示。

## 接入方式

采用 OpenUI 官方的“已有聊天界面和后端”接入方式：定义组件、从组件生成提示词、把提示词交给原有模型、用 Renderer 显示结果。没有新建 Next.js、LangGraph 服务，也没有将账户数据发送到新的模型供应商。

OpenUI Gateway 是官方推荐的模型生成方案，但这次保留已有渠道，不自动切换到 Gateway。没有新增供应商密钥或付费账户。模型调用和新增提示词仍按现有助手计费规则处理。本次没有接入 Gateway 的输出修复，也没有增加模型自动重试。

官方资料：
- https://www.openui.com/docs
- https://www.openui.com/docs/build-agents
- https://www.openui.com/docs/openui-lang/renderer

## 用户看到什么

助手先给出可独立阅读的文字说明，再按需要生成一个 `openui` 代码块。前端将代码块显示为界面：指标、条形图、可点击表头排序的表格、站内页面入口。普通问题和代码示例仍使用原有 Markdown。

示例提问：“查看我的用量，按模型做一个比较表，并说明统计时间。”助手必须先调用现有用量工具，不能用示例数据代替真实结果。图表仅支持同一单位的非负值。缺失数据不能当成零。

没有新增充值、创建密钥、安装工具或管理设置的自动执行入口。此类操作仍使用现有工具和确认卡片。站内链接只打开页面，不代表执行成功。人工接管和原有权限校验不变。

## 组件与边界

组件白名单为 `Stack`、`Metric`、`BarChart`、`DataTable`、`ConsoleLink`。不注册 HTML、脚本、表单、远程查询或修改工具。文本交给 React 转义。链接从固定页面表中选择，不接受任意网址和 `/api/` 路径。

每块最多 16000 个字符、128 行。根布局最多 12 个组件；图表最多 24 项；表格最多 8 列、32 行。界面生成期间不可交互，生成结束后才能点击。解析或渲染失败时显示原代码块，之前的文字说明保留。该机制不能证明模型给出的数值正确，工具数据准确性仍依赖原有服务端和模型规则。

界面模块按需加载。普通 Markdown 不加载 OpenUI 运行时。所有显示仍使用现有主题颜色、留白和字体。

## 开发与验证

在仓库根目录安装锁定依赖：

```sh
bun install --frozen-lockfile
cd apps/web
bun run openui:generate
bun run openui:check
bun test --preload ./scripts/test-preload.mjs src/components/ai-elements/openui/
bun run typecheck
bun run build
```

`src/components/ai-elements/openui/library.tsx` 是组件和模型提示词的单一来源。生成器将提示词写到 `apps/api-go/controller/assistant_openui_prompt.txt`，Go 编译时嵌入。构建会检查生成文件是否过期。不要手改生成文件，也不要把账户数据或密钥写入提示词。新增组件后重新生成并一起提交。

在 `apps/api-go` 执行：

```sh
go test ./controller -run TestAssistantOpenUIContract -count=1
```

测试使用合成数据，不请求付费模型、不读取生产账户。测试涵盖组件白名单、链接范围、输入上限、HTML 转义、真实渲染、异常回退、流结束校验、点击排序和前后端提示词一致性。完整浏览器验收还需检查深浅色、窄屏、键盘交互，以及真实模型能否稳定输出该格式。

## 发布

按现有 Web / Go 发布流程操作。建议先更新 Web，再更新 Go，避免旧前端把新格式仅显示成代码。无需数据库迁移。现有助手配置仍然有效。本次代码提交本身不表示已经合并、发布或部署到生产。
