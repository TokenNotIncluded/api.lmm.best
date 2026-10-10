# Core protocol and durable events (task 06)

Parent: PR #675. Work branch: `wip/mk-06-protocol-events`.
This component is for a **fresh PostgreSQL database**. It does not migrate old
balances or make Go a financial authority.

## Implemented versus reserved interfaces

`control.proto` and its existing fields are unchanged. `CoreControl` still
provides Capabilities, Authorize and ListTeams. The additive `events.proto`
implements `CoreEvents.Negotiate`, `Pull`, `Acknowledge` and `Retry` over the same
private Unix socket and service credential. Requests are bounded unary calls;
subscriptions and retry state live in PostgreSQL, not in a gRPC connection.

`commands.proto` defines **future domain contracts**, not live money endpoints:
credential revocation; capture of a core-verified payment; authorized transfer;
refund against an original operation; reserve against a core-issued quote;
settle against core-verified usage; release; and activation of an immutable,
core-validated routing configuration. These services are **not registered or
advertised** until their business handlers exist. They contain no arbitrary
SQL, generic commands, or set-balance operation. Core must resolve all reference
IDs and validate their current state; a caller-provided reference is not proof.
Amounts are integer units of a core-owned asset definition. No float amount,
client-selected scale or client-submitted usage total can authorize settlement.

## Version and wire rules

Do not reuse a v1 field number, change its type/name/presence or change an
existing meaning. Reserve deleted names and numbers. Breaking changes require
a new package (`lmm.core.v2`), not silently changed v1 handlers. The frozen Go
field baseline covers existing control fields and new event/command fields.

Events negotiate major 1, minor >= 1 and a bounded list of required features.
An unknown required feature or different major returns FailedPrecondition.
A newer minor is accepted only when its required features are supported.
Negotiation repeats before each Go batch, including after reconnect. Event type
subscriptions come from core provisioning; the request cannot expand them.
Unknown payload versions are retained, retried/reported and **never ACKed** by
the provided consumer. Missing handlers also stop consumption without data loss.

IDs and resource versions use positive signed int64 IDs and nonnegative int64
versions, matching PostgreSQL bigint. Do not send them through floating-point
JSON numbers: use Protobuf directly or decimal strings at JSON boundaries.
Tests include 9007199254740993 and 9223372036854775807.

Protobuf unknown fields may be skipped by a receiver. Go preserves them on
normal decode/encode; Prost does not promise to retain unknown fields when a
message is decoded and encoded again. Therefore domain `payload` is an opaque
byte string throughout outbox, gRPC and inbox. Do not decode and re-encode it in
transport code. Request fields unknown to an old service are not permission to
change security semantics: new mandatory checks need version/feature negotiation.

The event content digest is SHA-256 of `lmm.event.v1\0`, then big-endian u32
length + UTF-8 key, length + type, u32 schema version, i64 resource ID, i64 resource
version, then u32 length + original payload. The assigned delivery ID is not part
of this digest. It binds event content, **not authority** or a user's identity.
Command request fingerprints use a bounded, domain-defined canonical encoding;
Protobuf serialization in general is not a canonical business representation.

## Atomic publication and command deduplication

A Rust business handler must follow this sequence inside **one transaction**:

1. Validate service identity, actual user credential and resource permissions.
   Lock/recheck relevant grants and business versions inside that transaction.
2. Call `begin_command` with service ID, authenticated actor ID, full method and
   an idempotency key. On Replay, return the saved result only after current
   authorization succeeds. Reusing a key with different canonical content fails.
3. Apply the domain write. Call `EventStore::publish(&mut tx, event)` near the end
   using a stable, globally namespaced business event key.
4. Complete the command inbox with its result, then commit. **On any error,
   including Busy or Full, roll back the entire business transaction.**

An unfinished command inbox cannot commit: a deferred PostgreSQL constraint
checks that its response was stored in the same transaction. Retransmission does
not create a second business effect when handlers use this sequence. The event
publisher alone does not deduplicate business writes: calling a business update
again and merely reusing an outbox key is incorrect. Neither helper replaces
actual business authorization. Do not automatically retry non-idempotent writes.

Publication never calls or waits for Go. Committed events with no subscribers
are retained and attached when a matching subscription is provisioned. A newly
provisioned subscription can read matching retained history, but cannot recover
payloads already compacted after all earlier subscribers confirmed them. Provision
required consumers **before** enabling a producer. Subscriptions are immutable:
identical provisioning is idempotent; attempts to replace ownership/types fail.
There is no public registration, unsubscribe or arbitrary replay cursor RPC.

## Delivery, leases and confirmation

Pull selects unacknowledged, due delivery rows using row locks and SKIP LOCKED.
It does not assume sequence ID order equals commit order. In-flight receipts,
attempt counts and next-attempt times are committed before the response. If a
response is lost, the lease expires and the same event becomes eligible again.
The receipt contains a random 32-byte token; core stores only its SHA-256 digest.
A new delivery replaces the token. A stale token cannot ACK the new delivery.

Acknowledge validates the current service, configured subscription owner and
receipt. Repeating an already committed ACK with its exact token is successful,
even after restart. A lost ACK response therefore causes no repeated effect.
Retry releases the current lease and stores a capped exponential delay with
jitter (up to approximately 64.25 seconds). Repeating the same release does not
keep postponing its due time. Failed events are not discarded after N attempts.
There is no silent dead-letter deletion. Operations must alert on poison events.

Go's `SQLInbox.Apply` inserts a unique `(consumer_id,event_id)` receipt and runs
the supplied handler in the **same extension-owned SQL transaction**. Failure or
process death rolls both back. A duplicate checks the digest and skips the
handler; conflicting content fails. Only return success to `DrainEvents` after
this transaction commits. ACK then happens after local commit. The separate
`contracts/proto/tests/go` module contains the test SQL driver; the application
client accepts an existing `*sql.DB` and cannot open core's database itself.

This provides at-least-once delivery and one committed local database effect per
inbox key. It is **not** universal exactly-once delivery. Sending email, HTTP calls
or external payments in a SQL handler cannot be rolled back. Put those intents
in the extension's own outbox and use destination idempotency. Events can arrive
out of order or concurrently; domain handlers must check resource versions and
implement any required order. Transport does not promise a global order.

## Limits, retention and availability

Defaults: one payload <= 16 KiB; one batch <= 8 events and <= 48 KiB estimated
wire content; RPC messages <= 64 KiB; RPC handlers <= 8 concurrent calls; client
calls have existing 2-second deadlines and 32-slot admission. Handler time is
bounded to half the negotiated lease, at most 15 seconds. Default lease is 30
seconds (administrative range 1–300 seconds). Only one batch is held in Go memory.

The event delivery store uses a separate two-connection pool, 400 ms acquisition,
1.5 s statement timeout, 250 ms lock timeout and 2 s idle transaction timeout.
It must not consume the public request pool or the read-only identity RPC pool.
Service credentials are validated before storage or request-version checks.
User credentials are checked separately for user operations. An ACK authenticates
an event consumer, not a user payment. The current server binds its one configured
service credential to `extensions`; do not share that credential with mutually
untrusted extensions. Multiple service principals require separate configured
credential mappings; callers cannot pick a service identity in request metadata.

Global pending payload defaults: 100,000 events / 256 MiB. Each provisioned
subscription also has a count limit. Admission takes a NOWAIT capacity lock and
returns Busy on contention instead of waiting behind another business write.
This single counter is a deliberate correctness-first serialization point, not a
claim of maximum write throughput. Publishers must retry a whole idempotent
transaction on Busy. Do not hold its admission lock while doing remote work.
When a required consumer is full, new related writes fail with Full and roll back.
The process and unrelated/read-only operations remain available; it is impossible
to guarantee unlimited offline writes, finite disk space and no lost events at
the same time. Provision capacity and alerts before enabling critical producers.

When every recipient ACKs, the raw payload is removed and pending counters are
released. Event keys, digests, ACK receipts, command results and Go inbox receipts
are retained to prevent replays. **The pending limit is not a bound on all disk
usage.** Retained metadata grows; no automatic expiry is implemented. Capacity,
unacknowledged age, retry count, storage growth and failed version negotiation
need operational monitoring. Never delete deduplication records without an
explicit business replay horizon and an archival/restore design.

## Final integration checklist (intentionally not performed by task 06)

Install `apps/core-rust/schema/events.sql` once through the new fresh-db
initializer, after the core database is created. Install `SQLInboxSchema` in a
separate extension database. Keep Go's role unable to connect to core SQL.
Provision exact service-owned consumers and event types from trusted core
configuration. Connect a separate `EventStore`, then attach it using
`RpcServer::with_events(store, "extensions")`. Main does not attach it implicitly;
without it the event methods return Unavailable and capabilities do not advertise
event availability. Do not turn an event-extension failure into public server
shutdown. The existing control-only initialization remains functional.

Tasks 01–04 must supply approved domain event messages and use the transaction
hooks above; task 06 does not modify their business handlers. Task 03 must use
core-verified usage/quotes, and task 02 must enforce original-operation and asset
rules on refunds/transfers. Register future command services only after those
checks and domain-specific replay tests exist. Source coordination is recorded
on PR #675. There is no production deployment or legacy migration in this PR.

## Reproducible verification

Use `.github/workflows/mk06-protocol-events.yml` or run in a disposable local
PostgreSQL environment with the repository's pinned Rust/Go tools:

```sh
bash scripts/generate-core-protocol.sh --check
python3 -B scripts/test-core-boundaries.py
(cd apps/core-rust && cargo fmt --all --check && cargo clippy --locked --all-targets -- -D warnings && cargo test --locked --all-targets)
(cd apps/api-go && go vet ./... && go test -race ./... -count=1)
(cd apps/core-rust && cargo build --locked --example protocol-events-fixture)
(cd contracts/proto/tests/go && go build -mod=readonly -o /tmp/mk06-go-fixture .)
MK06_GO_FIXTURE=/tmp/mk06-go-fixture python3 contracts/proto/tests/process_recovery.py
```

SQLx tests require DATABASE_URL and create isolated test databases. The process
script accepts only localhost PostgreSQL, generates random `mk06_*` databases
and a dedicated extension role, then drops only those test resources. It starts
real Rust and Go executables over a private Unix socket, sends real Protobuf,
SIGKILLs Go during a SQL handler, restarts Rust around an unacknowledged delivery,
discards an application ACK response then retries after restart, and verifies the
same running Go client reconnects. It also publishes while Go is absent and tests
invalid service/user credentials, version negotiation, unknown fields and large
IDs. The ACK test injects loss at the application boundary, not a network proxy.

All effects use a **test counter**, not real balances. Passing these tests proves
the transport/inbox mechanisms under these faults; it does not prove actual
payment, ledger, budget, subscription or identity business events are integrated.
