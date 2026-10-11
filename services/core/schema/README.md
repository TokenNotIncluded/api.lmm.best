# Fresh core database

Deployment defaults to a new installation. It does not automatically upgrade, convert, or backfill the Go database or an earlier experimental Rust schema. An identity-only ETL was exercised between an isolated restored snapshot and a separate local core target, covering 888 legacy users at completion. Source funds, API keys and sessions were not imported, and production was not written. This is not a full business-data migration or a count of all currently usable accounts.

The Rust core is the only owner of identity, account permissions, funding rules, ledger state, budgets, subscriptions, and model routing. Go extensions own separate databases and call the core through versioned Protobuf services. An extension must never receive a core database credential.

`lmm-core init-db` is an explicit offline command. It installs eight components in one transaction: identity, ledger, payments, charging, routing, events, charging-ledger and migration, together with component contracts and the whole-core catalog fingerprint. It rejects an occupied application database and never drops existing data. Normal startup checks runtime-role authority, the complete installed contract and the live catalog fingerprint; it does not create or alter tables. There is no reset command, old-table fallback, or automatic schema repair. Installing a component does not mount its HTTP API.

Keep installation-owner credentials separate from the restricted network runtime. Role creation and GRANTs must be explicit offline provisioning; startup and package hooks do not grant privileges. Runtime must not own application objects, assume an owner role, or write immutable ledger tables directly. Required capabilities are checked in [runtime_database.rs](../src/runtime_database.rs). Owner names and GRANTs are deliberately excluded from the catalog fingerprint, so restricted-role provisioning does not require changing the schema contract. Current Docker identity and standalone examples reuse one URL for initialization and runtime and still need this separation; owner runtime startup is rejected.

Keep account ownership, the acting user, and the payer separate. Store monetary values as integers with explicit units. Store references, funding order, and permissions in relational columns. JSON is only for bounded metadata and immutable payloads.

A schema definition is not a completed billing engine. Request reservation, settlement, refunds, budgets, duplicate protection, and crash recovery require their own runtime tests. With a valid restricted role, complete schema and configured identity service, `/health/ready` can return 200 while models remain disabled; `/health/model-ready` then returns 503. Models default to disabled, and the current optional gateway is limited to explicit numeric-loopback synthetic upstreams without streaming. This does not establish external-provider compatibility, mounted payment APIs or parity with main.

## pgroll evaluation boundary

The repository pins the external [pgroll 0.16.3](https://github.com/xataio/pgroll/tree/v0.16.3) development tool. The [isolated example and test](../../../docs/development.md#pgroll-迁移实验) exercise versioned views, rollback and completion on a disposable database. They do not migrate the core schema, authorize a baseline of an existing database, or establish zero-downtime core upgrades.

Core adoption needs a separate compatibility design:

- Rust queries use qualified `core_identity.*` and `core_billing.*` names. Changing `search_path` alone does not route these queries through pgroll's versioned views.
- Ledger `SECURITY DEFINER` functions use a fixed safe search path and qualified tables. Table views do not automatically version functions, permissions or cross-schema dependencies. Preserve these security boundaries; raw SQL migrations do not inherit pgroll's zero-downtime guarantees.
- `pgroll init` creates application objects, which conflict with core's empty-database check if run first. Keep the current explicit, transactional fresh installation and read-only normal startup; do not automatically initialize or baseline pgroll.
- Normal startup uses the complete component contracts and live whole-core catalog fingerprint, including function and trigger definitions and trigger enable state. pgroll metadata, views and triggers can change that catalog input. The current check is not an already compatible online-migration contract.

Any next integration must first use a new isolated database, prove old/new client access and restricted-role permissions, and define schema/function version and fingerprint handling. Preserve the core/extension database ownership boundary; isolated identity ETL evidence does not establish a full financial migration.
