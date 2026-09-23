# GitHub Actions 入口

GitHub Actions 负责构建、测试、签名和发布产物；后端服务器部署与运维由操作者手动执行，前端发布则是唯一例外。

| 文件 | 职责 |
| --- | --- |
| `ci.yml` | PR 按改动范围检查；main、标签、手动运行和合并队列全量检查；每日 RustSec 扫描 |
| `server-release-qualification.yml` | 保留生产形态 Go、PostgreSQL、Valkey、迁移和恢复验收，以及每日验收 |
| `release-go.yml` | 手动触发 Go 签名发布，不连接服务器 |
| `release-web.yml` | 手动触发 Web 签名发布，不连接服务器 |
| `deploy-web-frontend.yml` | Web 签名发布成功后自动发布到两台生产 origin，仅限前端 |
| `billing-safety.yml` | Go `service`/`model` 改动时运行计费安全回归 |
| `standalone-deployment-tests.yml` | 部署脚本改动时运行 systemd 部署测试 |
| `lmm.yml` | `apps/lmm` CLI 与 OAuth 改动时运行 |
| `codewhale-lmm-provider.yml`、`coweft-identity.yml` | Codewhale provider 与 OIDC 身份边界的 PR 检查 |
| `console-page-review.yml`、`console-navigation-review.yml`、`settings-design-review.yml`、`ui-foundation-review.yml` | 前端页面、导航、设置和 UI 基础组件的 PR 视觉/交互检查 |

以上辅助工作流只按路径触发，不持有服务器凭据，也不属于发布门禁。

PR 描述和格式检查已移除，PR 仍执行与代码改动相关的测试。

GitHub 默认配置的 CodeQL 是仓库设置管理的动态工作流，不是这里额外生成的 YAML；本次不修改它的扫描或权限。

## CI 的选择规则

`ci_plan.py` 直接读取完整 Git diff，重命名按删除旧路径和添加新路径处理，不依赖 API 分页。PR 的前端、Go、Rust 和 Pi provider 改动只选择有关的 CI 任务；共享脚本、工作区依赖、打包、未知目录或无法可靠读取 diff 时运行全量。跨组件改动取并集。仓库文档仍检查仓库契约；组件内部文档仍按组件处理。

`CI Quality Gate` 必须收到全部任务结果。只有计划明确未选择的任务允许 `skipped`；失败、取消、缺失、计划错误和未知结果一律失败。main、标签、手动检查和合并队列不得使用 PR 的缩减计划。每日 CI 只运行安全扫描及其计划/汇总，不额外启动全部构建。完整服务器验收仍是独立工作流，不受 PR 的 CI 组件选择影响。

人工支持确认测试移入已有 Web 环境，保留逐个运行和 15 秒超时。独立 Rust root-route 锁文件检查与 RustSec 移入 CI，原先的锁文件校验、`cargo fetch --locked`、测试和固定版本审计 action 均保留。发布清单中的 root-route 检查名称不变，仅来源工作流改为 `ci.yml`。

## 队列与生产隔离

同一 PR 或 main 上被新提交替代的测试可以取消；手动检查与标签检查保留独立运行。后端服务器部署不属于 Actions 队列：除 `deploy-web-frontend.yml` 外，工作流没有生产 SSH 凭据或部署 job；服务器迁移和恢复验收只使用 runner 内的隔离测试环境。

`deploy-web-frontend.yml` 是唯一持有生产凭据的工作流。它的密钥在服务器侧被 `authorized_keys` 的强制命令 `/usr/local/sbin/lmm-web-deploy` 限制，只能执行前端 `frontend publish`，无法开 shell、无法执行任意命令、无法触达后端。撤销该密钥并在两台服务器移除对应的 `authorized_keys` 行即可关闭自动前端发布。

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
