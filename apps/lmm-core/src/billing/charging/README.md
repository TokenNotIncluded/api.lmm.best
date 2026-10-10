# Task 03: transactional charging

Base: `wip/rust-core-go-extensions` at `b83723cdac3f148140806b8025cb5360a2f6a26d`.
Work branch: `wip/mk-03-billing`. Integration PR: #675. Fresh PostgreSQL only.

## Boundaries and readiness

This directory implements the charging transaction and its PostgreSQL contract
tests. It does **not** implement a wallet, balance table, or production ledger.
There is no Go mutation endpoint, HTTP forwarding hook, schema auto-installer,
legacy migration, production deployment, or change to shared Protobuf files.

The test entry `tests/charging.rs` compiles this directory without editing the
parallel agent's `src/billing.rs`. Final integration must add `pub mod charging;`
to that file, install `schema/charging.sql` after `schema/identity.sql`, and supply
the task 02 adapter. Do not enable real upstream requests before joint acceptance.

## Transaction and ledger contract

`Charging::new(PgPool, Arc<dyn Ledger>)` requires an explicit ledger. `Ledger::apply`
receives the **same PostgreSQL connection and transaction** as charging. An adapter
must not commit, acquire another pool connection, call a remote money service, or
have any side effect outside that transaction. An error must leave its effects
rollbackable. This is a hard contract, not a performance preference. If task 02
cannot join this transaction, stop integration and agree on a durable outbox and
reconciliation protocol instead; do not implement a remote adapter behind this trait.

Commands contain an unambiguous hashed operation ID and immutable request, actor,
key, owner, payer, price version, and source. Identical IDs plus identical payloads
are safe replays. A changed payload must conflict.

| Change | Required task 02 behavior |
| --- | --- |
| Reserve(total) | Set an absolute, increasing request hold; lock the real wallet and reject insufficient funds. Never interpret total as an additional delta. |
| Settle(amount) | Capture the actual cost and release the unused hold in one transaction. Amount must not exceed the hold. |
| Release | Close an unstarted request hold exactly once. |
| Refund(amount) | Return at most the unrefunded captured amount to the original payer and source. |

A subscription source identifies the original grant and cycle. It must produce
an auditable entitlement-funded consumption record, **not a debit of wallet cash**.
Subscription limits in this module are rights to consume, not stored cash balances.
`grant_reference` must identify a core-verified purchase or grant. The final Rust
subscription provisioner must create grants only after the corresponding task 02
payment/grant is durable. Go must have no write grants on either core schema.

## Request flow

1. The trusted Rust model/price resolver supplies `Request.price`, an integer
   credit price snapshot, and a SHA-256 fingerprint of the full canonical request.
   Never deserialize a client's price, payer, actor, or reservation policy into
   this internal interface. Model/group access control belongs to the trusted core
   resolver; this module checks subscription model/group eligibility.
2. Generate a server-only, random 32-byte worker token. Call `reserve` with the
   real API-key secret. The secret is checked against the core identity tables
   inside the financial transaction, including user versions, team grants,
   membership generations, `can_spend`, key owner, and every configured payer.
3. Call `start`. Only a `true` result authorizes one upstream dispatch. A replay
   returns `false`. On an unknown commit, do not dispatch again. Use the same
   upstream request ID and provider status to settle the uncertainty.
4. Persist cumulative usage with `checkpoint` before exposing corresponding
   output. Round the cumulative cost once, never each chunk. Cached input is a
   subset of input; it is not charged twice. Late cache metadata may reduce the
   final input cost; checkpoints are observations, not captures.
5. Keep the lease alive with `heartbeat`. `increase` must succeed before the
   provider is allowed to exceed the current hold. The gateway must set provider
   output limits and stop/cancel generation when further reservation fails.
6. Call `settle` for successful, cancelled, or failed dispatched requests with
   authoritative actual usage. Partial output is billable. `release` is valid
   only before dispatch. Persist provider usage evidence if the result is unknown.

One request selects one fully funding source. `subscription_first`, `wallet_first`,
`subscription_only`, and `wallet_only` are ordered **whole-request** fallbacks;
there is no partial split across wallets, payers, or subscription cycles. Failed
candidates roll back to a savepoint. After selection, payer, source, and price are
immutable. A personal key preference cannot override a team's account policy.

## Budgets, cycles, and permissions

Budgets support account totals, account+member limits, global user self-limits,
and key limits. Every applicable rule must pass. Team members are identified by
stable user+account IDs, never membership generation or key digest. Swapping a key,
leaving/rejoining a team, changing the cap, or installing a cap after consumption
does not erase usage. Request history is the source for budget/entitlement totals;
there is no independent mutable usage counter to reset.

Calendar day/week/month windows use UTC, with Monday as the first weekday.
Custom windows use an explicit anchor and duration; all windows are half-open.
Subscription anniversary months retain the original day/time, including Jan 31
-> Feb 28/29 -> Mar 31. There is no local-time/DST budget policy in this version.
All of one request's budget cost stays in its authorization window, including
long streams and later refunds. A top-up does not move a stream into the next
subscription cycle. Reserve a bounded maximum before crossing a cycle, or stop
at the existing hold. Refunds restore only the original window.

`set_budget` accepts a current core **session**, not an API key or a claimed role.
The team owner can set total/member/key limits; an admin can limit their own use
or regular members, not the owner or other admins. Members can set their global
self-limit and their own personal keys. Platform L5/L6 is not spending authority.
Issuer identity is part of each rule: an admin cannot raise a founder's cap by
writing a new self-limit. All issuer rules are intersected. Rule scope and cycle
are immutable; updates change the cap without clearing historical consumption.

## Concurrency and recovery

Every mutation uses SERIALIZABLE isolation. Conflicting aggregate reads return
`Error::Retry`; the caller must retry the **entire operation** with the same ID,
payload, and worker token, with a bounded retry/backoff policy. Budget and ledger
writes commit or roll back together. Only a definitive insufficient-funds/budget
error permits trying another candidate; ledger/storage errors fail closed.

`CommitUnknown` is not a failure confirmation. `resolve(id)` uses READ COMMITTED
and the same request advisory lock so its read snapshot occurs after the original
transaction finishes. Repeat the original operation after resolution, not a new
request ID. Settlement/refund replays return their saved result.

Run `recover_expired` from a trusted Rust worker. Unstarted expired holds can be
released. Dispatched expired streams enter `reconcile`, retain their entire hold,
and reject the old stream worker. Only authoritative provider evidence may drive
`reconcile`, including zero use after proof that no request ran. A crash, empty
local usage, or elapsed time alone is never proof that funds can be released.
`reconcile`, `resolve`, and `refund` are internal service methods, not public or
Go-callable authorization endpoints. Final integration must enforce that boundary.

## Validation and joint acceptance

Run from `apps/lmm-core`, against disposable PostgreSQL with database-create rights:

```sh
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/postgres \
  cargo test --locked --test charging
cargo clippy --locked --test charging -- -D warnings
```

The PostgreSQL tests create isolated fresh databases. Missing PostgreSQL fails the
tests; it is not silently skipped. `RecordingLedger` records commands in the test
transaction and injects failures after writes. It holds **no cash**. Calendar,
concurrent budget consumption, late caps, key changes, team isolation, leave/rejoin,
subscription ordering/rollover, partial cancellation, duplicate events, settlement
rollback, refunds, lease expiry, and in-flight commit resolution have test cases.
The commit-resolution case tests lock/snapshot behavior; it does not simulate a
real network dropping a COMMIT acknowledgement.

Before production acceptance with task 02:

- Implement/review the same-transaction adapter and the original-source refund map.
- Repeat concurrent personal/team wallet tests against actual ledger funds and
  assert conserved balances, no overdraft, balanced entries, and exactly one hold.
- Kill the process before/after reserve, capture, refund and COMMIT; restart with
  the same request IDs. Drop commit acknowledgements using a database proxy.
- Prove that subscription-funded requests do not debit wallet cash, including
  expired-cycle settlement/refund and purchase/grant activation.
- Wire the Rust price resolver, model permissions, bounded upstream output,
  durable dispatch/provider IDs, background recovery, and schema installer.
- Verify database roles block every Go balance, entitlement and budget write.

Until those checks pass, this Draft is an integration component, not real-funds
acceptance and not authorization to merge #675 or deploy.
