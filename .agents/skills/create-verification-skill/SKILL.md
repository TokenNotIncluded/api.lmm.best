---
name: create-verification-skill
description: "Creates or strengthens executable local verification for a change when explicitly invoked by the user. Reuses existing regressions and checkers; does not authorize CI, production access, paid calls, publication or deployment."
disable-model-invocation: true
---

# 为改动建立可执行验证

仅在用户显式调用本技能时使用。按[工作指南](../../../docs/agent-workflows.md)
选择检查，不另建报告系统，也不把调用本技能当成扩功能或操作生产的授权。

## 先消除错误，再写说明

1. 读受影响代码、调用方和已有失败反例，确定要保护的不变量与拒绝路径。
2. 优先用类型、封装和单一安全路径让错误难以表达，例如不返回授权前的数据快照。
   只在已授权范围内做最小调整，不为验证重构全站。
3. 其次把可检查的约束落成已有 runner 能执行的本地回归或静态检查；复用 checker，
   不复制一套检查系统。说明文字只补工具不能强制的合同或边界。
4. 人工审查集中在权限权威、资金一致性、迁移映射/恢复等业务合同；不能用大量
   prose 规则替代代码，也不能把模拟成功当作真实权威或业务完成。

## 能力与范围先核实

- 开始前确认执行者能读、写、运行什么，有无所需本地工具和隔离凭据。
  缺执行/写入能力就提前标明；只读 reviewer 的反例设计不等于已经运行。
- 开发者自测与独立验收分开。需要独立执行却没有执行者时，记为未执行或受阻，
  不用开发者代跑后冒称 reviewer 自测，更不伪称已有独立 ACCEPT。
- 写前加载用户级 agentgc 技能并确认覆盖；遵守现有独占文件与重型构建锁约束。
  不访问生产、不触发 CI、不做付费调用、发布或部署。

## 小步形成回归

- 先复用原失败用例；bug 修复应保留红日志，再在相同反例上验证修复。
  独立反例原样保留，不删断言、不调低预期、不换 success-only mock 来改绿。
- 补最危险的拒绝、边界、重复、竞争、故障和最终状态断言；测试只做获准的改动。
- 用工作指南中的受影响包/检查器，通过本机 mise 工具链运行；不为局部变更堆叠
  已被聚合检查包含的任务，不顺带构建/验收无关组件。
- 记录实际命令、cwd、工具版本、输入 SHA、子进程退出码与日志。管道/总控 wrapper
  必须保留 child exit；wrapper exit 0 不能覆盖 child exit 1，也不能等同验收通过。

## 冻结与交回

- 冻结被测源码、测试和影响结果的配置/依赖输入；修复改变输入就重新记录 SHA。
  保留旧版本与失败证据，不覆盖红日志后声称原失败从未发生。
- 分开标注 pure、PG、process、browser、external 层的实际证据。内存 double、
  HTTP recorder、真实数据库、真实进程和外部供应商不是可互换的通过项。
- 未运行、Skip、工具受限、合同待裁决都单列；数量、覆盖率和构建成功不是完成率。
- 复用现有日志/manifest/交付格式，向原独立验证者交回冻结输入复验。
  最后停止自己的 watcher、等待最终扫描并分类产物；不借跟踪或阈值授权清理。
