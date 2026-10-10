# GitHub Actions 入口

[文档目录](README.md) · [贡献指南](../CONTRIBUTING.md) · [部署流程](deployment-workflow.md)

本文说明默认分支的入口。修改工作流后，以该提交的 `on`、输入参数、权限和
被调用脚本为准。不要从旧说明、工作流名称或残留注释推断触发方式。

## 测试、发布与部署分开

测试在本地运行并记录结果。当前 [ci.yml](../.github/workflows/ci.yml) 只有
`workflow_dispatch` 入口，可用于明确选择的手动诊断；不能宣称打开 PR、推送
main 或标签就会自动运行这套测试。其他辅助工作流应逐个检查其实际触发条件，
不能用 `ci.yml` 的条件代替它们，也不能把残留的自动触发条件写成统一政策。

| 入口 | 作用和边界 |
| --- | --- |
| [ci.yml](../.github/workflows/ci.yml) | 手动诊断。检查任务的成功、失败、取消和跳过情况，不只看汇总颜色。 |
| [release-go.yml](../.github/workflows/release-go.yml) | 在 Go 组件标签上手动构建、签名和发布；要求 `local_test_evidence`。 |
| [release-web.yml](../.github/workflows/release-web.yml) | 在 Web 组件标签上手动构建、签名和发布；要求 `local_test_evidence`。 |
| [deploy-web-frontend.yml](../.github/workflows/deploy-web-frontend.yml) | 单独授权、单独触发的前端部署。不是签名发布的自动后续步骤。 |

Go/Web 发布前须有与目标代码匹配的真实本地测试记录。发布工作流读取
`local_test_evidence`，不是等待 PR 检查全部变绿。记录格式和来源校验以
[local-release-tests.py](../scripts/local-release-tests.py) 和
[verify-release-commit-checks.sh](../scripts/verify-release-commit-checks.sh) 为准。
不能伪造记录、套用不同源码的旧结果，或把本地测试脚本退出成功当成产物已发布。

## 诊断计划不是触发条件

[ci_plan.py](../scripts/ci_plan.py) 负责选择检查任务。它处理哪些事件或路径，
不等于当前工作流已为这些事件启用自动触发。任务汇总规则仍需检查：未选择的
任务可以跳过；要求执行的任务失败、取消或缺失，不能解释为验收完成。

新增组件或修改检查计划时，核对计划脚本及其测试。修改工作流名称、路径或
调用关系时，同时核对 [workflow-topology.test.mjs](../scripts/workflow-topology.test.mjs)。
Go/Web 签名工作流路径属于签名身份，不为整理文件而随意改名或合并。

## 生产边界

发版与部署是两件事。先按目标机器的安装方式选择
[部署流程](deployment-workflow.md)，核对当前版本和兼容性，保留签名校验、
备份、锁、确认和恢复步骤。包管理安装与压缩包安装不能混用更新路径。

手动触发成功只表示请求被接受。需要记录准确的运行 ID、提交、标签、结果和
目标环境验收，才能声明发布或部署完成。不要查询“最新一次运行”来替代本次运行。
文档修改不应触发发版、服务器访问或自动恢复，也不应改变测试触发策略。

## 修改后验证

在仓库根目录运行相关本地检查：

```bash
python3 -B -m unittest discover -s scripts -p test_ci_plan.py
python3 -B -m unittest discover -s scripts -p test_ci_quality_gate.py
node --test scripts/workflow-topology.test.mjs
bash scripts/test-verify-release-commit-checks.sh
```

仅修改本文时，运行文档检查即可；这些命令用于工作流或配套脚本改动。隔离
测试通过不等于生产数据库恢复、服务器部署或全部应用测试已经完成。
