# Agent 工作指南

[文档目录](README.md) · [仓库规则](../AGENTS.md) · [技能目录](../.agents/skills/README.md)

本文用于修改仓库，不是客户端登录指南，也不是生产部署授权。

## 先确认分支，再读技能

先检查当前分支、基准提交、未提交修改和相关 PR。以该提交中的代码为准。
重构方案、开发分支、已合并代码、已签名版本和线上部署，必须分别确认。
不能因为某个 PR 使用新架构，就修改默认分支的说明，宣称切换已经完成。

从技能目录选择当前任务需要的技能。界面新增文案，同时读取界面和翻译技能。
发布、部署或恢复服务，单独读取部署技能。技能不提供额外权限，也不保证当前
会话已连接其中提到的工具。仓库内的本文件和 `AGENTS.md` 不会自动变成网站路由。

## 命令与路径的依据

| 内容 | 核对来源 |
| --- | --- |
| 根目录命令 | [justfile](../justfile)、[package.json](../package.json) |
| 前端命令与依赖 | [apps/web/package.json](../apps/web/package.json) |
| 本地环境与端口 | [开发指南](development.md) |
| CI、签名发布与部署 | [Actions 说明](ci-workflow-layout.md)、[部署流程](deployment-workflow.md) |
| 翻译同步的实际行为 | [sync-i18n.mjs](../apps/web/scripts/sync-i18n.mjs) |

除命令明确切换目录外，均从仓库根目录执行。应用前端位于 `apps/web`。
不要把组件包内命令直接当成根目录命令，也不要通过更新依赖来修复文档路径。

## 按改动选择检查

文档和技能：

```bash
python3 -B scripts/check-docs-brand.py --docs-only
python3 -B -m unittest discover -s scripts -p test_check_docs_brand.py
```

修改翻译写入工具后：

```bash
node --test .agents/skills/i18n-translate/scripts/apply-translations.test.mjs
```

修改 Logo 后，使用不带 `--docs-only` 的完整检查。修改应用代码，则按照开发指南
运行对应组件的测试。不要为纯文档修改执行全仓格式化，也不要执行生产示例来
“检查命令是否正确”。

文档检查范围是维护入口和项目技能：本地链接、技能名称与描述、技能内的旧前端
路径、中英文 README 徽章。它不覆盖全部 Markdown 文件、完整 YAML 语法、标题
锚点、远程链接或命令执行结果。自动检查通过后，仍需人工核对文档与代码。

## 交付记录

记录修改范围、基准提交、实际测试命令、结果和未验证项。环境缺少依赖时说明
具体限制，不把阅读代码写成执行测试，不把测试样例通过写成真实数据库或线上
验收通过。提交后报告分支和 PR；只有获得授权后才合并、发版或部署。

不得将密钥、Cookie、密码、数据库连接串或未脱敏日志写入文档和测试证据。
使用本站时遵守一人一个账号，不批量注册，也不通过额外账号领取奖励。
