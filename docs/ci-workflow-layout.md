# 微服务分支的 GitHub Actions 入口

[文档目录](README.md) · [贡献指南](contributing.md) · [架构与状态](core-migration.md)

本文对应 PR #675 的微服务分支，不是 main 旧单体的发布说明。
触发方式、输入和权限以当前提交的工作流为准。

## 检查入口

| 入口 | 作用和边界 |
| --- | --- |
| [ci.yml](../.github/workflows/ci.yml) | 手动诊断；调用微服务检查，并检查前端和翻译。 |
| [core-protocol.yml](../.github/workflows/core-protocol.yml) | 手动、可复用及指定 WIP 路径触发；检查真实 Rust/Go 实现、协议与一次性数据库。 |
| [microservice-distribution.yml](../.github/workflows/microservice-distribution.yml) | 手动或复用，生成三个程序与静态 Web 的独立预览归档；不发布 Release，不部署。 |
| [linux-packages.yml](../.github/workflows/linux-packages.yml) | 构建三个程序的原生 Linux 预览包，并在一次性容器中检查安装、升级、运行与卸载；不发布或部署。 |
| [store-claim-refund-review.yml](../.github/workflows/store-claim-refund-review.yml) | 手动；复用当前微服务检查，保留商店前端及浏览器回归。旧单体用例仍在 main。 |

检查成功、失败、取消和未运行项必须分别记录。指定路径的触发规则不代表
整个仓库每次提交都会运行完整检查。测试入口不能修改成空检查来消除失败。
旧 `ci_plan.py` 与单体 Go 发布流程已移除，不适用于本分支。

## 发布与生产边界

旧站点的 Web 发布、部署工作流和专用包装链路已从本分支退役。
当前工作流生成预览包和检查证据，不安装到现有主机，也不替换线上服务。
历史发布与恢复说明保留在[固定版本的历史入口](archive.md)。
微服务包的支持范围、校验与限制见[打包与分发](release-architecture.md)。

真实测试记录必须绑定实际源码、命令、退出码与日志。保留
[本地证据工具](../scripts/local-release-tests.py)和
[校验入口](../scripts/verify-release-commit-checks.sh)的限制，
不伪造记录，不套用不同源码的结果。

模块测试通过不代表前端、权限、订单与资金的完整链路已接通。
跨服务业务缺口仍按[业务验收范围](microkernel-business-acceptance.md)记录。
任何生产操作都需要单独授权；手动触发请求被接受也不等于部署完成。

## 修改后验证

```sh
python3 -B scripts/check-docs-brand.py
python3 -B -m unittest discover -s scripts -p test_check_docs_brand.py
python3 -B scripts/test-core-boundaries.py
python3 -B scripts/test-docs-microservices.py
node --test scripts/workflow-topology.test.mjs
```

这些检查验证文档与工作流边界，不替代应用、数据库及生产验收。
