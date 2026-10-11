# Fresh core database

This branch targets a new installation. It does not upgrade, import, convert, or backfill the Go database or an earlier experimental Rust schema.

The Rust core is the only owner of identity, account permissions, funding rules, ledger state, budgets, subscriptions, and model routing. Go extensions own separate databases and call the core through versioned Protobuf services. An extension must never receive a core database credential.

Schema installation must be an explicit offline command. It must run in one transaction, fail on a non-empty database, and never drop existing data. Normal startup only checks the schema contract; it must not create or alter tables. There is no reset command, old-table fallback, or automatic schema repair.

Keep account ownership, the acting user, and the payer separate. Store monetary values as integers with explicit units. Store references, funding order, and permissions in relational columns. JSON is only for bounded metadata and immutable payloads.

A schema definition is not a completed billing engine. Request reservation, settlement, refunds, budgets, duplicate protection, and crash recovery need runtime tests before the model API can become ready.

## pgroll evaluation boundary

The repository pins the external [pgroll 0.16.3](https://github.com/xataio/pgroll/tree/v0.16.3) development tool. The [isolated example and test](../../../docs/development.md#pgroll-迁移实验) exercise versioned views, rollback and completion on a disposable database. They do not migrate the core schema, authorize a baseline of an existing database, or establish zero-downtime core upgrades.

Core adoption needs a separate compatibility design:

- Rust queries use qualified `core_identity.*` and `core_billing.*` names. Changing `search_path` alone does not route these queries through pgroll's versioned views.
- Ledger `SECURITY DEFINER` functions use a fixed safe search path and qualified tables. Table views do not automatically version functions, permissions or cross-schema dependencies. Preserve these security boundaries; raw SQL migrations do not inherit pgroll's zero-downtime guarantees.
- `pgroll init` creates application objects, which conflict with core's empty-database check if run first. Keep the current explicit, transactional fresh installation and read-only normal startup; do not automatically initialize or baseline pgroll.
- The committed identity check verifies the recorded installation SQL hash, not the live catalog. The uncommitted whole-core catalog-fingerprint draft is not used by normal startup; pgroll metadata, views and triggers would change its input. Neither check can be treated as an already compatible migration contract.

Any next integration must first use a new isolated database, prove old/new client access and restricted-role permissions, and define schema/function version and fingerprint handling. Existing billing drafts and core/extension database ownership remain unchanged.
