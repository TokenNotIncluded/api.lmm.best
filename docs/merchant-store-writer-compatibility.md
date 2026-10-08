# Shop writer compatibility protocol

The compatibility binary has writer capability **1**. Variant-aware binaries
have capability **2**. The required capability is the reserved, persistent
`options` row `MerchantStoreMinimumWriterCapability`, with exact value `1` or
`2`. Every new checkout, product/stock/publication/payment-setting/economics
write reads the actual database row under a shared row lock in its transaction.
Missing, malformed or unreadable state blocks new writes. OptionMap and Redis
are never the authority for this gate.

This shim adds no tables, columns, indexes or automatic data migration. Runtime
and AutoMigrate **never create, repair or reset** the gate. The public API uses
HTTP 503 / `STORE_UPGRADE_IN_PROGRESS` when a writer is incompatible. Public
reads, legacy/default-order replay, authenticated payment callbacks, cancellation,
confirmed-provider closure, reconciliation, pickup and queued delivery remain
available. Their financial, ownership and signature checks are unchanged.
The capability-1 shim cannot replay a new nondefault-spec checkout digest; that
request fails with a conflict. The existing order ID still supports its frozen
payment callback, cancellation and pickup without reading current spec prices.

The gate is a deployment capability, not a shop setting. General single/bulk
option writes reject its reserved spelling, including case/space aliases and
MySQL collation aliases. There is no general option-deletion API. Direct
privileged database changes remain operator actions and must not lower or
delete the row.

## Explicit initialization

Use private environment-supplied `SQL_DSN`. No credentials appear in command
arguments or JSON output. The CLI opens only that primary database, starts no
server, workers, Redis or migrations. Operations have a 30-second deadline;
advisory-lock cleanup is separately bounded to two seconds.

```sh
lmm-api-go merchant-store-writer-gate bootstrap
lmm-api-go merchant-store-writer-gate status --require-writable
```

`bootstrap` only initializes missing state to `1` **before any variant table or
stock/order variant column exists**. An existing valid `1` or `2` is preserved;
invalid values are rejected. Missing post-variant state is rejected, never
recreated at `1`. A fresh installation must perform this reviewed compatibility
phase before variant DDL. Recovery of a deleted post-activation gate requires a
separate reviewed operator recovery; this command cannot reopen an old writer.

Status JSON contains `required_capability`, `writer_capability`,
`new_writes_allowed`, `supports_writer_gate` and `supports_variants`. Missing or
invalid state reports safe capability metadata and exits 1. A valid gate 2
returns status normally for a capability-1 shim with `new_writes_allowed=false`;
`--require-writable` then exits 1. This supports a frozen-fulfillment rollback
without claiming it can accept new orders.

## Activation and retained rollback

1. Publish and verify the capability-1 shim on both hosts. Explicitly bootstrap
   before variant DDL, and confirm each host reads gate 1 and allows new writes.
2. Publish variant-aware binaries on both hosts, apply the reviewed additive
   schema once and verify each actual serving artifact. At gate 1, capability 2
   must not write nondefault stock, enable multi-spec publication or create
   nondefault orders; those operations explicitly require gate **2**.
3. Only after schema preservation, old obligations, capacity concurrency,
   retained-shim rollback, UI and both-host capability proofs pass, activate:

```sh
lmm-api-go merchant-store-writer-gate activate --expected-current=1 --reviewed-variants-ready
lmm-api-go merchant-store-writer-gate status
```

Activation takes the gate's exclusive row lock and only increases `1` to `2`.
Completed activation retries are idempotent. It requires the actual variant
table fields and stock/order variant columns, performs no DDL and cannot lower
the gate. It never locks products or wallets. New-write transactions take
product and any sorted wallet locks before their gate shared lock; callbacks
and pickup do not acquire the gate at all. This prevents activation from
reversing the business lock order.

On PostgreSQL, explicit bootstrap and activation use the existing startup
migration advisory lock key on a context-bound dedicated connection,
serializing with automatic DDL. Identity, advisory lock, and transaction all
use that same connection, including Unix sockets and a one-connection pool.
The original pool remains unmodified. Failed unlock discards the physical
connection instead of leaving a session lock in the pool. Capability-1
startup apply rejects gate 2 before AutoMigrate. This rollout's concurrent-DDL
proof is PostgreSQL-specific; MySQL/SQLite rollouts must serialize migration and
activation externally rather than claim the PostgreSQL advisory-lock proof.

An ordinary deployment/rollback provider must check the real target binary's
status protocol **before stopping a writer or replacing packages**. A binary
without this protocol, including an unshimmed Go86 writer, is unacceptable as
an activated rollback. The retained capability-1 shim may serve frozen orders
at gate 2 while every new write fails closed. That service must start in
`LMM_DB_MIGRATION_MODE=verify`: capability-1 automatic apply is deliberately
refused at gate 2. Never reset gate 2 to make an old
artifact writable. Provider integration and its actual artifact rollback test
are a separate release gate; this source alone does not prove that integration.

## Verification boundaries

Unit/SQLite tests cover fail-closed reads, missing-state preservation, reserved
option writes, schema prerequisites, monotonic activation, all new writer
entry points, frozen payment/cancellation/pickup, asynchronous AI publication
and HTTP error mapping. Test fixtures explicitly initialize the gate before
shop DDL; runtime does not share that test-only behavior.

Real PostgreSQL shared-lock activation contention, one-connection advisory
binding, complete legacy schema/data preservation, N-1 running-artifact
exercise and deployment-provider pre-stop enforcement require their own
evidence. A DSN-gated skipped test is not a database-concurrency pass. No source
or fixture validation authorizes production DDL, payment or inventory access.
