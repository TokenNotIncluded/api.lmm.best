# Task 06 integration handoff

Parent PR #675. Task PR #699. Work branch `wip/mk-06-protocol-events`, based on
`b83723cdac3f148140806b8025cb5360a2f6a26d`. No merge or production deployment.
See [EVENTS.md](EVENTS.md) for implemented delivery, schema, security and tests.

## Implemented now versus contract only

`CoreControl` retains its existing Capabilities, Authorize and ListTeams methods.
`CoreEvents` is implemented but must be explicitly attached with
`RpcServer::with_events`; the production initializer and main program are not
changed by this task. Apply `schema/events.sql` separately during final fresh
installation, before attaching the event store. Go never gets core credentials.

`commands.proto`, `ledger.proto` and `payments.proto` generate bindings, NOT live
business services. They are not registered or advertised. A schema/comment does
not implement authorization, payment verification, budgets or money movement.
Do not enable these services until their business handlers and actual domain
acceptance tests exist. The fixture counter tests prove transport and local
transaction behavior only, not real ledger, payment or billing correctness.

## Task 02: canonical ledger commands

Coordinated against PR #694 and `src/billing/ledger/README.md` on
`wip/mk-02-ledger`, including the task-02 comment on #675.

`CoreLedger.PostLedger` uses explicit Credit/Transfer/Charge/Reserve/Capture/
Release/Refund/Reverse actions. Identity account IDs are
`core_identity.accounts.id`, never user IDs or team IDs. Amounts, balances,
deltas and revisions are signed int64. The only allowed unit in this rollout is
`credit_500k_usd` (500,000 integer units per USD). All arithmetic must remain
integer, including refunds and conversion rounding.

The older, unregistered `CoreLedgerCommands` contract is retained without field
or meaning changes. Its `Account` fields are identity selectors (kind plus user
or team ID), not raw identity account IDs; a future adapter must resolve and
authorize them before constructing a canonical `PostLedger` request. Its asset
registry must allow only `credit_500k_usd` in this rollout. Do not register two
independent money-writing paths or reinterpret its existing fields.

Core assigns scope from authenticated service identity. User-originated actions
require separately verified current user authority. Provider/system actions need
an explicit core-issued authority and independently verified evidence, never a
service token treated as a user. Check permissions for each action and each
original journal or reservation; recheck authority before replaying a receipt.
Credit fixes account/amount using a verified core payment record. Charge/reserve
use a core-issued single-use billing grant; capture uses core-verified usage.
These references are design requirements, not records implemented in task 06.
Refund destination is fixed by the original transaction. Reverse is restricted
to explicit reconciliation/administrative authority.

A permanent operation key and full canonical content identify a command.
`PostLedgerResponse` separates Posted and permanent Rejected receipts; a key
conflict is ALREADY_EXISTS. A database/transport timeout may mean an unknown
commit, not confirmed rejection. Retry the same key and content, never invent a
new debit, release or refund to compensate for a missing reply. No TTL may allow
an old payment or debit key to execute again.

**Integration blocker:** the current task-02 `Ledger::execute` owns its database
transaction. It cannot safely be followed by a separate outbox commit or preceded
by a separate mutable budget check. Before exposing financial RPCs, task 02/03
must supply a shared transaction composition hook, or a proven durable single-use
authorization flow whose ledger posting also writes the outbox atomically. Do not
use a second inbox to claim atomicity across independently committed operations.
The task-06 inbox helper is intended for handlers that own the full transaction;
coordinate it with the ledger's existing permanent operation records rather than
creating conflicting sources of truth.

## Task 10: payment intents and refund lifecycle

Coordinated against the task-10 interface request on #675, comment 6093319860.
`CorePayments` freezes a core-owned intent before the provider order is created.
It binds the account, authenticated actor, channel, provider, merchant,
environment, currency, minor-unit amount and core-calculated credit amount.

Credit and provider-driven refund requests carry original evidence. An adapter
must preserve the exact provider-signed bytes and necessary signature metadata,
within the existing message-size limit. Core must independently verify evidence
against its configured provider/channel and the immutable intent. Do not accept
a caller-selected verification URL, a verified boolean or replacement credit
amount. Providers whose signatures need extra metadata require explicit bounded
additive fields; do not approximate their verification using incomplete evidence.
External transaction uniqueness spans provider/merchant/environment/transaction,
not merely a service-scoped request key.

Refunds first hold funds, then commit on verified provider success or release on
verified terminal failure. Unknown provider results never release funds merely
because a deadline elapsed. External refunds must locate the original payment by
intent/reference even when Go lost the credit response, reconcile existing holds,
and permanently prevent a delayed pre-refund credit request from adding money.
Receipt lookup is authorized by intent plus authenticated scope. Its result is a
durable state, not an accepted/queued success indication. Go's durable command
retry/reconciliation flow remains part of the payments task, not the event inbox.

LedgerPostedEvent and PaymentStateChangedEvent define payloads only. Their real
producers must call `EventStore::publish` with the same business transaction.
Only then can actual business-event acceptance be added. Do not fabricate a
passed integration by publishing fixture events after a ledger commit.
