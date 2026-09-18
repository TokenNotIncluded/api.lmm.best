# GitHub Actions 入口

工作流从 10 个收敛为 6 个。精简的是触发和重复环境准备，不是发布验收标准。

| 文件 | 职责 |
| --- | --- |
| `ci.yml` | PR 按改动范围检查；main、标签、手动运行和合并队列全量检查；每日 RustSec 扫描 |
| `pr-check.yml` | 只检查 PR 描述，使用可信 base 代码；编辑描述不触发整套构建 |
| `server-release-qualification.yml` | 保留生产形态 Go、PostgreSQL、Valkey、迁移和恢复验收，以及每日验收 |
| `release-go.yml` | Go 签名发布，成功发布后通过内联的 `.github/actions/deploy-production/` 部署 |
| `release-web.yml` | Web 签名发布，成功发布后通过内联的 `.github/actions/deploy-production/` 部署 |
| `server-ops.yml` | 独立的人工诊断和明确授权的生产恢复 |

GitHub 默认配置的 CodeQL 是仓库设置管理的动态工作流，不是这里额外生成的 YAML；本次不修改它的扫描或权限。

## CI 的选择规则

`ci_plan.py` 直接读取完整 Git diff，重命名按删除旧路径和添加新路径处理，不依赖 API 分页。PR 的前端、Go、Rust 和 Pi provider 改动只选择有关的 CI 任务；共享脚本、工作区依赖、打包、未知目录或无法可靠读取 diff 时运行全量。跨组件改动取并集。仓库文档仍检查仓库契约；组件内部文档仍按组件处理。

`CI Quality Gate` 必须收到全部任务结果。只有计划明确未选择的任务允许 `skipped`；失败、取消、缺失、计划错误和未知结果一律失败。main、标签、手动检查和合并队列不得使用 PR 的缩减计划。每日 CI 只运行安全扫描及其计划/汇总，不额外启动全部构建。完整服务器验收仍是独立工作流，不受 PR 的 CI 组件选择影响。

人工支持确认测试移入已有 Web 环境，保留逐个运行和 15 秒超时。独立 Rust root-route 锁文件检查与 RustSec 移入 CI，原先的锁文件校验、`cargo fetch --locked`、测试和固定版本审计 action 均保留。发布清单中的 root-route 检查名称不变，仅来源工作流改为 `ci.yml`。

## 队列与生产隔离

同一 PR 或 main 上被新提交替代的测试可以取消；手动检查、标签检查和生产操作不进入这个取消组。发布和运维继续共用原生产互斥组，且不允许自动取消正在进行的生产操作。重构不修改 SSH、恢复脚本、备份要求或部署动作。

仅改变 `.github/server-ops-343-request.json` 的 main 提交不触发 CI 和服务器验收。请求校验、恢复专用测试及授权仍由原 `server-ops.yml` 执行；任何同时修改源代码的提交仍触发正常检查。这样的请求提交没有完整 main-push 发布证据，不能被当作已验收的发布候选。未完成或取消的 main 检查也不会通过原发布门禁。

`release-go.yml` 和 `release-web.yml` 的路径是签名身份的一部分，因此不为减少文件数量而合并。所有必须的 main-push 检查、CodeQL 和 `Server release qualification gate` 仍需真实通过，不能用 PR 的部分检查、旧提交或手动绿色状态替代。

## 修改和验证

新增组件时先更新 `ci_plan.py` 和测试；未分类的路径默认全量，不能默认跳过。新增/移除工作流时同步更新 topology 测试，迁移检查来源时同步更新 `.github/required-release-checks.txt`，不能直接删除发布要求。

```sh
python3 -B -m unittest discover -s scripts -p test_ci_plan.py
python3 -B -m unittest discover -s scripts -p test_ci_quality_gate.py
node --test scripts/workflow-topology.test.mjs
bash scripts/test-verify-release-commit-checks.sh
```

正式 CI 继续执行 actionlint、原质量门禁和生产形态验收。局部脚本测试通过不代表整仓 CI 或线上恢复完成。
