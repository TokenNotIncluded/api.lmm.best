# Rust core ledger — task 02

Parent PR: #675. Task PR: #694. Branch: `wip/mk-02-ledger`.
Target: `wip/rust-core-go-extensions`. This module is not a production rollout.

## Scope and money model

`Ledger` is a trusted Rust in-process interface. It does not register an HTTP or
gRPC route. Go extensions must never receive its database credentials. User,
service, payer and budget authorization must be integrated before exposing any
network write endpoint. This module prevents ledger overdrafts; it does not yet
enforce per-user, per-key or per-team spending budgets.

All ownership IDs are `core_identity.accounts.id`, not user IDs or team IDs.
Personal and team accounts have the same wallet representation. Existing account
policies choose a payer elsewhere; they do not mutate this ledger.

The only unit is `credit_500k_usd`: **500,000 integer units = USD 1**. This follows
`billing::CREDITS_PER_USD`; it is not a new exchange rate. `Credits` accepts only
nonnegative signed-64-bit integers. Positive operations reject zero, except final
capture, which may charge zero and release the whole reservation. Signed entries
use `i64`; additions and revisions fail on overflow. PostgreSQL aggregate checks
use exact integer-valued `NUMERIC`, not floating point. Do not send these integers
through a JavaScript `Number` when they exceed its exact integer range.

Each identity account gets an available `wallet` and a `reserved` bucket.
`clearing` records the opposite side of externally confirmed top-ups and may be
negative. `revenue` receives consumption charges and cannot be negative. No user
or held balance can be negative. Starting all buckets at zero and requiring every
journal's signed entries to sum to zero preserves the global total. This is an
internal credit ledger, not a complete financial reporting general ledger.

| Table | Purpose |
| --- | --- |
| `ledger_accounts` | Immutable owner, bucket and unit |
| `ledger_journals` | Immutable operation, economic payer/payee, amount, original journal and actor/reason |
| `ledger_entries` | Signed changes, resulting balance and per-account revision |
| `balance_state` | Current balance/revision maintained in the same transaction as entries |
| `ledger_operations` | Permanent full request and exact posted or business-rejected response |

Journal IDs are sequence IDs, not a global commit-order clock. Gaps after rollback
are normal. Use per-account revisions for an account's order. Entry totals can
rebuild current balances; ordinary runtime code has no set-balance or repair API.

## Operations

`open_wallet(account_id)` creates the two zero-valued buckets idempotently.
`balance(account_id)` reads both buckets in one PostgreSQL statement/snapshot.
It returns an error for an unopened or unknown wallet, not a misleading zero.

`execute(&Request)` accepts only the following actions:

| Action | Movement and constraints |
| --- | --- |
| `Credit` | Confirmed external funding: clearing to the selected wallet. Unique payment reference required. |
| `Transfer` | Available source wallet to another wallet. No self-transfer or negative source. |
| `Charge` | Wallet to revenue. |
| `Reserve` | Wallet to its held bucket. Held money cannot be spent again. |
| `Capture` | Finalize one reservation: held to revenue for the actual amount; return unused money to the original wallet in the same journal. |
| `Release` | Finalize one reservation by returning its entire held amount. |
| `Refund` | Partial/full compensation of a credit, transfer, charge or capture. The destination is always the original economic payer. |
| `Reverse` | Exact one-time compensation of an unrefunded credit, transfer or charge. Use `Refund` for a captured reservation. |

Capture and release compete for the same original journal and only one can win.
Refunds and reversals share a cumulative original-amount limit. No refund can
choose a different destination. A transfer refund can fail when the recipient has
already spent the money; the ledger does not create a negative wallet to hide it.
A top-up refund reduces the user's wallet and compensates clearing. **It does not
itself send money back through an external payment provider.**

There is no partial multi-capture or automatic reservation expiration. Do not
release a reservation solely because a network deadline elapsed: the metered
request may still be running. Integration must establish a terminal outcome and
then explicitly capture or release it. Rewards and store payouts should transfer
from a funded, authorized treasury/settlement account; never disguise a reward as
an externally funded top-up. Split store settlements, fees and provider payouts
need their own authorized composite business operation in a later integration.

## Retry contract and transaction rules

The unique key is `(scope, operation_key)`. Scope is assigned by authenticated
Rust service dispatch, not trusted from a Go or browser field. Requests include
the action, IDs, amount, payment reference when present, actor and audit reason.
The stored JSON value is compared, not a collision-prone request hash.

An identical request always returns its original receipt, including its original
balance snapshots. The same key with different parameters returns
`Error::IdempotencyConflict`. Both success and expected business rejection are
stored. Funding an account later does not change an earlier insufficient-funds
response. A new business attempt needs a new key; a network retry must keep the
old key and the full original request. Keys/receipts have no TTL.

`Error::Database` can mean that the result of COMMIT is unknown to the caller.
Retry the same request/key after a connection error, lock timeout or process
restart. Never refund, release or generate a fresh debit key just because the
response was lost. Validation errors and unexpected database failures do not
commit a receipt. PostgreSQL unique payment references additionally stop one
external payment being credited through different operation keys.

`execute` opens one short READ COMMITTED transaction, sets a 3-second lock timeout,
a 10-second statement timeout and synchronous commit, posts, then returns only
after COMMIT. It makes no provider or extension calls while the transaction is
open. Lock order is operation-key advisory lock, original journal if needed,
then all affected balance IDs in ascending order. The hash used for the advisory
lock can only cause extra serialization on collision; the permanent key itself
remains exact. Expected business failures roll back every posting before saving
the rejected receipt. Unexpected failures abort the whole transaction.

Do not call the SQL posting function multiple times in one outer transaction.
Do not combine its locks with external services. Task 03 must coordinate budget
checks and ledger reservation atomically inside Rust, or supply a durable
single-use authorization reservation. An authorization check in one transaction
followed by an independent debit is not a safe integration for mutable budgets.
The current public Rust API owns its transaction; that composition hook is not
implemented here and must be resolved before exposing a spending endpoint.

The shared clearing and revenue rows are intentional contention points. This
version proves the listed safety cases, not a production throughput target.
Sharding those system accounts requires a separate measured change.

## Fresh installation and database privileges

`schema/ledger.sql` is deliberately **not** added to `init-db` or the identity
schema. The final integrator must apply it after identity.sql, in the installer's
transaction, and include it in the schema-version/fingerprint checks. It is a
fresh-install definition, not an idempotent upgrade or old-data migration.

Install the tables/functions as a dedicated NOLOGIN owner. The owner requires
access to `core_identity.accounts`. Rust's runtime login must not own tables or
functions, inherit the owner role, have superuser/BYPASSRLS privileges or have
schema CREATE rights. Go has no privileges on ledger tables/functions. Example
grants, after creating the separately managed `lmm_ledger_runtime` role:

```sql
GRANT USAGE ON SCHEMA core_billing TO lmm_ledger_runtime;
GRANT SELECT ON core_billing.ledger_accounts,
    core_billing.balance_state, core_billing.ledger_journals,
    core_billing.ledger_entries, core_billing.ledger_operations
    TO lmm_ledger_runtime;
GRANT EXECUTE ON FUNCTION core_billing.open_ledger_wallet(BIGINT),
    core_billing.post_ledger(JSONB) TO lmm_ledger_runtime;
```

Do not grant INSERT/UPDATE/DELETE/TRUNCATE or sequence privileges. No routine
business role should be able to change defaults, disable triggers or replace
functions. PUBLIC access to ledger tables and all ledger functions is revoked.
The two entry functions use SECURITY DEFINER, a fixed `pg_catalog` search path and
fully qualified table references. The audit tables reject UPDATE, DELETE and
TRUNCATE. A committed/sealed journal also rejects additional entries. A deferred
constraint checks at least two entries and zero sum before commit.

Owners, superusers and the installer are within the trusted boundary. These
controls do not protect against an administrator disabling constraints or editing
storage. An independent audit copy, backup retention, monitoring and recovery
procedure remain deployment responsibilities. PostgreSQL must keep durable WAL
and acknowledgments; a failover to a replica missing an acknowledged commit can
invalidate exactly-once retry assumptions. Use a no-lost-commit failover policy.

## Task 06: required messages and permissions

This is the requested handoff, not a second implementation of task 06's shared
Protobuf files. Implement typed `oneof` actions matching `Action`, not a generic
JSON execution endpoint and not arbitrary ledger entries.

`PostLedgerRequest` must carry a protocol version, permanent operation key,
authenticated actor context, business-event reference/audit reason, unit and a
typed action. All account/journal IDs and amounts use signed `int64` with positive
ID and nonnegative amount validation. Resolve `scope` from the authenticated
service identity; do not accept caller-selected scope. Persist that resolution
and the complete request before an extension retries after restart.

`PostLedgerResponse` must distinguish a committed `Posted` receipt (journal ID,
entry deltas, balance snapshots and account revisions) from a permanent business
`Rejected` code. Idempotency conflict must remain distinct from a transient or
unknown-result database failure. A timeout must not be presented as confirmed
rejection. `GetLedgerBalance(account_id)` returns available and held balances,
unit and revisions, after account-read authorization. A future receipt lookup
must also enforce scope/owner authorization; no public lookup is exposed here.

| Permission | Required check before calling the trusted Rust module |
| --- | --- |
| `ledger.read` | Authenticated user/service can read the selected personal or team account. |
| `ledger.credit` | Dedicated verified-payment path only. Verify provider signature/evidence, merchant, environment, settled amount and unit conversion inside the trusted payment boundary. Never trust extension-supplied money alone. |
| `ledger.transfer` | Payer ownership/team authority, permitted destination and limits. Treasury rewards/payouts need separately scoped treasury authority. |
| `ledger.charge` / `ledger.reserve` | Payer/key ownership and durable budget authorization. A team key must not fall back to a member's wallet. |
| `ledger.capture` / `ledger.release` | Authority over that original reservation and its recorded request lifecycle; cannot operate on another service's reservation by guessing its ID. |
| `ledger.refund` | Explicit authority over the original journal, permitted business reason and remaining refundable amount. Ledger fixes the destination. |
| `ledger.reverse` | Narrow administrative/reconciliation authority and audit reason. Not available to ordinary extensions or users. |

Use an authenticated service channel (for example mTLS plus service permissions),
not possession of an account ID as authorization. Obtain actor IDs from trusted
identity checks. External payment references must be globally namespaced as
`provider/merchant/environment/transaction`; operation-key scoping alone is not
external-payment deduplication. Do not include secrets or raw payment credentials
in audit reasons.

The business layer must durably retry its event with the same operation key.
The ledger and a Go business database cannot commit one distributed transaction.
Go may manage business states but must use a durable retry/reconciliation flow,
not directly adjust balances if it misses a response. A transactional outbox and
external-provider refund confirmation are integration work, not implemented by
this module.

## Reproducible checks

Use a disposable **real PostgreSQL** database, never a production URL. SQLx creates
an isolated database per test. The test connection needs database creation; the
permission/connection-loss tests also require test-only role creation and backend
termination rights. Those are not runtime privileges.

```sh
cd apps/lmm-core
export DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/postgres'
cargo +1.99.0 fmt --all --check
cargo +1.99.0 clippy --locked --all-targets -- -D warnings
cargo +1.99.0 test --locked --all-targets -- --nocapture
```

`tests/ledger.rs` covers concurrent overspending, opposite-direction transfers,
duplicate submissions and changed-parameter conflicts, permanent rejection,
external-payment deduplication, concurrent partial refunds, original payer,
reservation finalization races, overflow, late transactional fault injection,
append-only controls, privilege separation and deferred conservation checks.
It verifies materialized balances/revisions against the immutable entry history.

Two separate transport failure tests cover rollback before commit and loss of
the COMMIT acknowledgment after the server commits. The latter uses a real TCP
proxy: it receives PostgreSQL's COMMIT completion and ReadyForQuery messages,
drops them and closes the socket. The retry must return the stored receipt and
leave the balance debited exactly once. It is not merely a discarded Rust return
value or a mocked database.

CI is `.github/workflows/core-ledger.yml`; it uses an isolated PostgreSQL service,
read-only repository permissions and no deployment step. Each run saves source,
commit identity and formatter/clippy/test logs as an artifact. The task PR records
the verified run and remaining integration work; do not infer a pass merely from
this test list.
