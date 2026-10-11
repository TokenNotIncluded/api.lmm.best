# 历史资料索引

本页集中保存已移除专题的固定 Git 原文，不再为每个旧文件保留占位页。当前安装、架构和测试从[文档目录](README.md)开始；仍有效的前端、资金和商店边界分别合入[前端规范](../apps/web/DESIGN.md)、[资金契约](payment-money-contract.md)及[商店模块](../apps/lmm-extensions/internal/modules/store/README.md)。

历史命令和接口只适用于对应提交，不是本分支的安装、迁移或生产恢复步骤。旧部署工具、数据库转换、writer gate 和财务恢复流程不得据此重新启用；历史付款、退款和提货义务仍须保存原始证据，未知结果不能当作失败重放。

## 审计与证据边界

以下审计包含部分完成、受阻和未通过验收的项目。原始记录只证明对应基线和检查范围，不证明当前账本、真实业务兼容或生产部署通过。
- [bi-06-refunds-20261010](https://github.com/TokenNotIncluded/api.lmm.best/blob/79b539abacace5297952274620ec938a8e9da34f/docs/audits/bi-06-refunds-20261010.md)
- [bi-08-store-delivery-20261010](https://github.com/TokenNotIncluded/api.lmm.best/blob/7dc467bc50d8e78964e0cb7a8b892a171b5b4211/docs/audits/bi-08-store-delivery-20261010.md)
- [rt-17-request-ambiguity](https://github.com/TokenNotIncluded/api.lmm.best/blob/b25293a1157782a77ad2244b4a3f4e563a0e2ed2/docs/audits/rt-17-request-ambiguity.md)
- [rt19-async-task-state](https://github.com/TokenNotIncluded/api.lmm.best/blob/67559bc41ae15e7204396140e52fac5436d59fe4/docs/audits/rt19-async-task-state.md)
- [BI-01 旧单体计价验证](https://github.com/TokenNotIncluded/api.lmm.best/blob/b8f775b7a2753c4e374101edf5dfe6c0b066332e/scripts/business-audit/BI-01-tier-pricing.md)
- [RT-15 旧浏览器来源边界审计](https://github.com/TokenNotIncluded/api.lmm.best/blob/b8f775b7a2753c4e374101edf5dfe6c0b066332e/scripts/security-audit/rt15-browser-origin.md)
- [RT-20 旧准入组件资源探针与证据](https://github.com/TokenNotIncluded/api.lmm.best/blob/b8f775b7a2753c4e374101edf5dfe6c0b066332e/scripts/resource-audit/rt20.md)

上述脚本审计原文保留对应的源码基线和未完成项；已移除的旧单体探针不是当前测试入口。仍适用的前端、安全及金融回归输入继续保留在仓库中。

## 旧部署和数据维护

- [backend-cli-deployment-contract.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/backend-cli-deployment-contract.md)
- [controller-only-backup-format.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/controller-only-backup-format.md)
- [credit-balance-rebase.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/credit-balance-rebase.md)
- [credit-financial-coordinator.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/credit-financial-coordinator.md)
- [credit-financial-plan-builder.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/credit-financial-plan-builder.md)
- [credit-financial-transition.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/credit-financial-transition.md)
- [credit-original-table-fingerprints.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/credit-original-table-fingerprints.md)
- [deployment-workflow.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/deployment-workflow.md)
- [fixed-credit-price-recovery.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/fixed-credit-price-recovery.md)
- [manual-systemd-deployment.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/manual-systemd-deployment.md)
- [native-decisions.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/native-decisions.md)
- [native-merchant-writer-guard.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/native-merchant-writer-guard.md)
- [postgresql-cutover.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/postgresql-cutover.md)
- [postgresql-migration.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/postgresql-migration.md)
- [postgresql-scanner-leadership.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/de1c3c15bc5e19632fe8b2375a0760b6a38c26f6/docs/postgresql-scanner-leadership.md)
- [production-release-transaction.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/production-release-transaction.md)
- [seamless-upgrades.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/seamless-upgrades.md)
- [server-ops.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c9475c96b5a6d9601444cb9f2ccfbbfdc3b1f663/docs/server-ops.md)
- [setup-startup.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/setup-startup.md)
- [shared-postgres-maintenance.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/shared-postgres-maintenance.md)
- [standalone-deployment-tool.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/35c5952ab24ea6b9a2ae9a935a75f51485549c32/docs/standalone-deployment-tool.md)
- [three-node-deployment.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/three-node-deployment.md)
- [valkey-lmm-api.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/72667564c0431754d4856dc2e0db55f360bd2745/docs/valkey-lmm-api.md)

## 旧接口和设计

- [advanced-custom-balance.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/advanced-custom-balance.md)
- [ali-image-polling.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/ali-image-polling.md)
- [assistant-admin-control.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/assistant-admin-control.md)
- [assistant-coupon-regression.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/assistant-coupon-regression.md)
- [assistant-efficiency-audit.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/assistant-efficiency-audit.md)
- [assistant-reward-autonomy.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/assistant-reward-autonomy.md)
- [async-task-performance-metrics.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/async-task-performance-metrics.md)
- [authentication.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/authentication.md)
- [channel-marketplace.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/channel-marketplace.md)
- [other_setting.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/channel/other_setting.md)
- [editor-providers.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/editor-providers.md)
- [github-oauth-migration.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/github-oauth-migration.md)
- [go-memory-management.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/go-memory-management.md)
- [ionet-client.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/ionet-client.md)
- [model-price-locks.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/model-price-locks.md)
- [model-pricing-currency.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/model-pricing-currency.md)
- [openai-native-models.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/openai-native-models.md)
- [api.json](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/openapi/api.json)
- [relay.json](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/openapi/relay.json)
- [pprof-diagnostics.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/pprof-diagnostics.md)
- [profile-usage-aggregation.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/profile-usage-aggregation.md)
- [read-only-query-api.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/read-only-query-api.md)
- [relay-response-model.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/relay-response-model.md)
- [responses-websocket-channels.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/responses-websocket-channels.md)
- [service-tier-pricing.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/service-tier-pricing.md)
- [stream-commit-boundaries.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/stream-commit-boundaries.md)
- [task-plugin-host-decision.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/task-plugin-host-decision.md)
- [token-log-pagination.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/token-log-pagination.md)

## 前端专题

- [assistant-human-support.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/assistant-human-support.md)
- [assistant-openui.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/assistant-openui.md)
- [assistant-tool-policy.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/assistant-tool-policy.md)
- [assistant-ui-preferences.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/assistant-ui-preferences.md)
- [frontend-design.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/frontend-design.md)
- [signal-path.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/games/signal-path.md)
- [home-directory-navigation.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/home-directory-navigation.md)
- [mobile-scroll-chrome.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/mobile-scroll-chrome.md)
- [shaders.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/shaders.md)
- [tool-market-connections.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/tool-market-connections.md)
- [tool-market-guide.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/tool-market-guide.md)
- [tool-market-metamcp.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/tool-market-metamcp.md)
- [tool-market-metering.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/tool-market-metering.md)
- [tool-market-paid-test-mcp.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/tool-market-paid-test-mcp.md)
- [tool-market-provider-presets.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/tool-market-provider-presets.md)
- [waiting-experience.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/waiting-experience.md)

## 资金和权限专题

- [browser-credit-unit-handshake.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/browser-credit-unit-handshake.md)
- [moderation-security-review.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/moderation-security-review.md)
- [price-lock-snapshot-consistency.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/price-lock-snapshot-consistency.md)
- [referral-rewards.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/referral-rewards.md)
- [waffo-plan-products.sql](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/sql/waffo-plan-products.sql)
- [team-accounts.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/team-accounts.md)
- [user-risk.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/user-risk.md)
- [wallet-mcp.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/wallet-mcp.md)
- [wallet-red-packet-recovery.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/wallet-red-packet-recovery.md)
- [wallet-transfers.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/wallet-transfers.md)

## 旧商店专题

- [store-extore-import.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/development/store-extore-import.md)
- [merchant-store-analytics.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-analytics.md)
- [merchant-store-capability-one-ci-probe.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-capability-one-ci-probe.md)
- [merchant-store-delivery-templates.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-delivery-templates.md)
- [merchant-store-fixed-content.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-fixed-content.md)
- [merchant-store-guest-email.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-guest-email.md)
- [merchant-store-lifecycle-writer-floor.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-lifecycle-writer-floor.md)
- [merchant-store-minimum-price.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-minimum-price.md)
- [merchant-store-payment-categories.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-payment-categories.md)
- [merchant-store-product-test-mode.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-product-test-mode.md)
- [merchant-store-refund-dispatch.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-refund-dispatch.md)
- [merchant-store-refund-providers.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-refund-providers.md)
- [merchant-store-refund-sync.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-refund-sync.md)
- [merchant-store-refunds.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-refunds.md)
- [merchant-store-sales-controls.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-sales-controls.md)
- [merchant-store-startup-baseline.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-startup-baseline.md)
- [merchant-store-support.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-support.md)
- [merchant-store-variants-contract.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-variants-contract.md)
- [merchant-store-writer-compatibility.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store-writer-compatibility.md)
- [merchant-store.md](https://github.com/TokenNotIncluded/api.lmm.best/blob/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs/merchant-store.md)

## 保留的历史记录

- [旧合并式 Changelog](history/changelog.md)：保留原文，不定义当前版本和发布状态。
- [部署研究](deployment-research.md)：已移除实验的结论、原始来源和未接入项。
- [恢复事故](incidents/2026-09-20-recovery-retirement.md)、[助手反馈审计](assistant-feedback-audit.md)和[注册安全边界](security/assistant-registration-guard.md)：保留事故、安全结论与原始限制。

其余尚保留的供应商、支付和兼容性专题需结合当前源码阅读；前端存在某个界面不代表新后端已接入。许可证、来源归属和法律协议继续保留。
