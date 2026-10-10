# 待创建 Draft PR

Title: research(dp-29): 按需运行、可恢复的多节点发布控制原型

Base: `wip/rust-core-go-extensions`
Head: `research/dp-29-release-control`
Intended starting commit: `72667564c0431754d4856dc2e0db55f360bd2745`

Refs #675

## 变更

全部新增文件位于 `experiments/deploy/dp-29/`。统一入口提供 `init/plan/status/step/run/rollback/gc`；保存不可变发布 ID、清单摘要、期望版本、节点执行代号和待确认操作。一次更新一个目标节点，接入 DP-22/23/24/27/28 的适配器槽位，不恢复 main 旧部署器或旧数据库迁移。

包含模拟节点、独立 HTTP/SSE 测试进程、并发控制进程和进程崩溃测试。应用回退不读取或恢复业务余额快照；旧数据读写范围不兼容时拒绝回退。前端保留旧页面资源与接口要求。

## 验证

执行 `python3 -S -B verify.py`。实际测试数和结果以 `evidence/summary.json` 为准，原始测试日志在 `evidence/unittest.txt`。测试包含 1/3/5 同机逻辑节点、分角色目标组、多个操作进程、确认丢失和控制器退出。

这些结果不是 Docker、多服务器、原生 Rust/Go、PostgreSQL 或生产资金验收。真实适配器缺失时拒绝推进。不得凭模拟结果取消 Draft。

## 当前交付状态

仅生成本地新增文件补丁。未创建远端分支或 PR，未推送、未运行远端 CI、未部署生产、未合并。当前环境不能取得完整 Git 工作树，补丁的结构性应用检查是在空的补丁检查目录中执行，而不是在完整基线仓库中执行。维护人员应在指定提交的独立工作树中再次运行 `git apply --check` 和本地验证。

创建远端 PR 前须单独核实仓库当时的自动工作流是否会触发运行；本次任务没有授权触发远端 CI。
