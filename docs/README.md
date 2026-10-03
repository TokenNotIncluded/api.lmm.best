# Documentation Index

This directory contains operational, API, and legal documentation for LMM Forge.

## Core operation

- [`authentication.md`](./authentication.md): authentication and session architecture.
- [`seamless-upgrades.md`](./seamless-upgrades.md): operator upgrade flow.
- [`backend-cli-deployment-contract.md`](./backend-cli-deployment-contract.md): normative provider entry points, package transactions, and manual rollback.
- [`manual-systemd-deployment.md`](./manual-systemd-deployment.md): standalone systemd deployment commands and recovery.
- [`production-release-transaction.md`](./production-release-transaction.md): manual release acceptance and transaction reconciliation.
- [`controller-only-backup-format.md`](./controller-only-backup-format.md): optional controller-owned backup imports and signed evidence.
- [`release-architecture.md`](./release-architecture.md): component-scoped Go/Web release identities and Rust preview boundary.
- [`model-price-locks.md`](./model-price-locks.md): model base price locks, ignored-change warnings, and API compatibility.
- [`claude-refusal-billing.md`](./claude-refusal-billing.md): opt-in billing policy for Claude refusals without output.
- [`async-task-performance-metrics.md`](./async-task-performance-metrics.md): Go terminal task sampling, financial invariants, and shared Rust metric reads.
- [`responses-missing-usage.md`](./responses-missing-usage.md): provider-reported zero, generated output estimates and the confirmed partial-output policy in #373.
- [`go-memory-management.md`](./go-memory-management.md): Go request memory, optional large-request admission, and small-host tuning.
- [`postgresql-migration.md`](./postgresql-migration.md): migration rehearsal workflow.
- [`postgresql-cutover.md`](./postgresql-cutover.md): production cutover transaction.
- [`valkey-lmm-api.md`](./valkey-lmm-api.md): dedicated Valkey deployment guidance.
- [`rust-blue-green.md`](./rust-blue-green.md): Rust provider rollout and ownership checkpoints.
- [`task-plugin-host-decision.md`](./task-plugin-host-decision.md): decision against the current upstream script host migration and evidence required for a future provider proposal.
- [`test-single-instance.md`](./test-single-instance.md): isolated Rust test-host guide.
- [`open-source-bounties.md`](./open-source-bounties.md): bounty mechanics and workflow.
- [`ionet-client.md`](./ionet-client.md): iNet client reference artifact.
- [`channel/other_setting.md`](./channel/other_setting.md): additional channel JSON settings.

## API contracts

- [`openapi/api.json`](./openapi/api.json): admin API contract.
- [`openapi/relay.json`](./openapi/relay.json): relay API contract.
- [`relay-response-model.md`](./relay-response-model.md): provider response-model observations, compatibility rules, and runtime logging boundaries.
- [`responses-websocket-channels.md`](./responses-websocket-channels.md): channel capability, native route eligibility, and persistent connection authorization.
- [`token-log-pagination.md`](./token-log-pagination.md): opt-in token usage-log pagination and Go/Rust response contracts.
- [`translation-glossary.md`](./translation-glossary.md): bilingual terminology base.
- [`translation-glossary.fr.md`](./translation-glossary.fr.md): French glossary.
- [`translation-glossary.ru.md`](./translation-glossary.ru.md): Russian glossary.

## Legal

- [`legal/user-agreement.md`](./legal/user-agreement.md)
- [`legal/privacy-policy.md`](./legal/privacy-policy.md)
- [`legal/terms-of-service.md`](./legal/terms-of-service.md)

## Governance and maintenance policy files

- [`../CONTRIBUTING.md`](../CONTRIBUTING.md)
- [`../SUPPORT.md`](../SUPPORT.md)
- [`../SECURITY.md`](../SECURITY.md)
- [`../CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md)
- [`../FORK.md`](../FORK.md)

## How this index is maintained

- Add new docs when a stable operational process or contract changes.
- Use clear headings and date-sensitive notes for migration and cutover content.
- For language-specific docs, include English fallback or keep linkable references.
