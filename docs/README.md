# Documentation Index

This directory contains operational, API, and legal documentation for LMM Forge.

## Core operation

- [`authentication.md`](./authentication.md): authentication and session architecture.
- [`seamless-upgrades.md`](./seamless-upgrades.md): operator upgrade flow.
- [`release-architecture.md`](./release-architecture.md): component-scoped Go/Web release identities and Rust preview boundary.
- [`dynamic-pricing.md`](./dynamic-pricing.md): dynamic pricing feature: formula pipeline, configuration, status API, and operation.
- [`postgresql-migration.md`](./postgresql-migration.md): migration rehearsal workflow.
- [`postgresql-cutover.md`](./postgresql-cutover.md): production cutover transaction.
- [`valkey-lmm-api.md`](./valkey-lmm-api.md): dedicated Valkey deployment guidance.
- [`rust-blue-green.md`](./rust-blue-green.md): Rust blue-green and ownership checkpoints.
- [`test-single-instance.md`](./test-single-instance.md): isolated Rust host operation note.
- [`open-source-bounties.md`](./open-source-bounties.md): bounty mechanics and workflow.
- [`ionet-client.md`](./ionet-client.md): iNet client reference artifact.
- [`channel/other_setting.md`](./channel/other_setting.md): additional channel JSON settings.

## API contracts

- [`openapi/api.json`](./openapi/api.json): admin API contract.
- [`openapi/relay.json`](./openapi/relay.json): relay API contract.
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

