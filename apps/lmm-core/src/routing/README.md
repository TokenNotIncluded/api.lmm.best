# Rust routing and pricing — contract v1

Task 04, based on `b83723cdac3f148140806b8025cb5360a2f6a26d` of
`wip/rust-core-go-extensions` / parent PR #675. Working branch: `wip/mk-04-routing`.
This is a routing component, not a claim that the model HTTP API is ready.

## Ownership and integration

The module owns route selection, versioned prices, health gates, and the retry plan.
It makes no Go calls, forwards no HTTP requests, and writes no ledger entries.
Normal request selection uses memory only. PostgreSQL is used by Rust to publish
configuration and recover after restart, never for each routing decision.

Task 05 (`wip/mk-05-relay`) owns HTTP clients, credential resolution, protocol
conversion, streaming, cancellation, and reporting transport outcomes. Task 06
(`wip/mk-06-protocol-events`) owns Protobuf messages and field numbers. Neither a
new proto file nor a shared `lib.rs`, `main.rs`, HTTP entry point, or Go file is
changed here. `tests/routing.rs` compiles the module independently until the
integration owner adds `pub mod routing` to the core library. Once integrated,
remove that bridge so module unit tests are not compiled a second time.

`CONTRACT_VERSION = 1` is the Rust contract. It is NOT a Protobuf field number.

## Task 05 call sequence

1. Get an authenticated group from identity/billing. Do not trust a raw client
   group field. Routing does not grant group membership or choose the payer.
2. Call `Router::route(RouteRequest)` exactly once for the logical request.
3. Keep the returned `RoutePlan`, and reserve funds using its `PriceQuote`.
4. Call `plan.start()` for the first `Attempt`. Resolve `CredentialRef { id,
   revision }` through the Rust secret service, not through Go. The revision is
   immutable; never fall back to a different key when it is unavailable.
5. Use the endpoint, protocol, approved upstream model, payload policy and timeout
   from the attempt. Check `attempt.is_available()` immediately before sending.
6. On failure call `plan.retry(Failure)`, not `Router::route` again. Record the
   attempt number and route/target IDs. Keep upstream error bodies out of logs.
7. Settle one logical request with the pinned quote and normalized final usage.
   A retry is not a new per-request fee. Task 03 decides which incurred usage is
   chargeable and enforces reservation/settlement idempotency.

Core types and methods:

```rust,ignore
let mut plan = router.route(RouteRequest {
    model: "model-a",
    group: authorized_group,
    speed: Some(Speed::Fast),
    protocol: Protocol::OpenAiChat,
    replay: ReplayPermission::Denied,
})?;
let pinned_price = plan.quote();
let first = plan.start()?;
// Task 05 sends first, using its private credential resolver and HTTP client.
let second = plan.retry(Failure {
    kind: FailureKind::Connect,
    delivery: DeliveryState::NotSent,
})?;
assert_eq!(first.version(), second.version());
assert_eq!(first.quote(), second.quote());
```

Each attempt exposes `version`, `quote`, `number`, `speed`, `route_id`, `target_id`,
`upstream_id`, `canonical_model`, `group`, `endpoint`, `upstream_model`, `protocol`,
`payload_policy`, `credential_ref`, and `timeout_ms` as read-only methods. Private
fields prevent accidental replacement of a selected destination.

`PayloadPolicy::Passthrough` requires the exact same model name and wire protocol.
Aliases, model-name suffixes, or protocol changes require an explicit `Adapt`
target. The adapter may translate the approved model name/protocol; it must not
remove reasoning controls, change sampling, or substitute another canonical
model. The routing component itself never changes a body or inserts a service
level parameter. Task 05 must still enforce outbound host/IP policy, TLS, header
filtering, response limits, and redirects; this module is not an HTTP firewall.

## Group, speed and selection rules

A group is matched exactly. An alias belongs to one group and points directly to
one canonical route in that group. Duplicate names, shadowing and alias chains
are rejected. There is no cross-group or cross-model fallback.

`standard`, `fast`, and `ultrafast` must be explicitly allowed by the route, target,
and matching price rule. Missing fast/ultrafast configuration is an error, not a
standard-speed fallback. `model/fast` and `model/ultrafast` are recognized as
suffixes. Conflicting explicit speed and suffix are rejected. Canonical model
names and aliases cannot end with these reserved suffixes. For unchanged request
bodies, prefer a `/fast` or `/ultrafast` HTTP path mapped by task 05 to the explicit
speed field rather than adding a suffix to the JSON model field.

Smaller priority wins. Within a priority tier, selection is weighted random with
an unbiased integer draw using the existing `rand` dependency. Fallbacks are
weighted draws without replacement; only after a tier is exhausted is a lower
priority considered. Each route can have at most 64 targets, one target per
upstream, and at most 8 total attempts. A zero weight, disabled target, disabled
upstream, unhealthy/unknown state, incompatible passthrough, or unsupported speed
excludes a candidate. All unavailable means an error, never a fallback model.

The complete ordered candidate list is pinned at request start. Live health and
operational changes may remove candidates later, but cannot add a new target.
The candidate count is bounded by the configured attempt limit. A candidate
removed after planning is not replaced by a new lookup.

## Failure and replay policy

The default policy is one attempt and no retries. A configured retry cause may be
`Connect`, `Timeout`, `RateLimited`, or `ServerError`. Authentication failures,
client errors, invalid responses, and cancellation always stop the plan.

`NotSent` means no request bytes reached the provider. `Rejected` means the
provider is known not to have accepted work; a status code alone is not always
proof. A failure that might have been accepted is `PossiblyAccepted`. Retrying it
requires BOTH configuration permission and explicit request replay permission.
Task 05 must not invent idempotency support. `ResponseStarted` means response body
bytes were exposed to the client; retry is always prohibited, even when both
permissions are set. A denied retry ends the plan permanently.

Health is a Rust-owned runtime concern, separate from immutable pricing. New
destinations start optimistically healthy; a Rust probe can set them to `Unknown`
until tested. This module does not start a probe worker or infer a circuit-breaker
timeout. Task 05 may call `Attempt::mark_unhealthy` after an infrastructure failure.
A trusted Rust health monitor reopens destinations using `Router::set_health`
with the current release version. Production integration must supply that monitor.
An old credential/endpoint failure does not poison its replacement. Route/price-
only updates retain health and emergency stop state for unchanged destinations.

A configuration disable affects NEW requests, consistent with pinned plans.
For an immediate operational stop, use `set_operational_enabled`; it also blocks
not-yet-sent attempts in existing plans. Health reports do not clear that flag.
It cannot revoke bytes already sent to an upstream.

## Money and price evidence

`TokenRates` use integer **micro-USD per 1,000,000 tokens**. The `request` rate is
micro-USD per logical request. One micro-USD is USD 0.000001. There is no conversion
through the old Go quota constants. Group and speed multipliers use parts per
million: `1_000_000 = 1x`, `2_000_000 = 2x`. Each multiplier must be positive and no
larger than `1_000_000_000` (1000x).

`Usage.input`, `output`, `cache_read` and `cache_write` are DISJOINT. Task 05 must
subtract included cache tokens from a provider's total input count before passing
usage to this module. Providers whose cache counters overlap need normalization,
not direct addition. The quote sums all dimensions and the single request fee,
applies both multipliers in checked `u128`, and rounds UP once to micro-USD. A
result beyond PostgreSQL signed `bigint` or any intermediate overflow is an error.
No saturation, floating point, or silent overflow is allowed.

Task 03 should persist the routing version, price version, price rule ID, both
multipliers and normalized usage with its request accounting evidence. No ledger
or billing mutation is implemented by this module.

## Publication, persistence and recovery

The offline initializer must explicitly install `schema/routing.sql` in its fresh
schema transaction. This change does not register the schema in the shared
initializer and never upgrades a database at startup.

`PostgresRoutingStore::register_credential_reference` is for the Rust secret manager
after it durably writes a secret revision. It registers numeric IDs only; it does
not store or verify secret material. A route referring to an unregistered pair
fails its database foreign key. Resolve failures still fail closed at send time.

`stage_prices(PriceConfig)` validates and seals an independent price version without
moving traffic. `publish(RoutingConfig, price_version, expected_version)` validates
all route/price combinations, creates the immutable artifact and normalized
projections, seals them, and moves the active pointer in one database operation.
A row lock and expected version prevent lost updates. Reusing a price version with
different stored JSON is rejected. Existing artifacts/projections cannot be
updated, deleted, or appended after sealing. Normalized tables are generated from
the artifact inside the publication function, not maintained by Go.

The active pointer references one routing version, which references one price
version. `load_active()` reads that pair in one SQL statement. A recovered
`ReleaseDocument::into_router()` is sufficient to route without Go. A failure to
load a new release must keep the last good in-memory release; startup without a
valid release must report not ready. Do not silently create default prices.

After commit, task 06 should deliver the version notice to Rust nodes; each node
loads the active document and calls `ReleaseDocument::apply`. Delivery must be
retryable, and a periodic Rust reconciliation read should cover missed notices.
The durable commit and per-process install are NOT a distributed atomic commit.
Nodes can temporarily serve different complete versions, never a mixed pair.
Requests pin their own pair. On a local publication conflict, reload the durable
active version. An already applied notice should be acknowledged by comparing
both version numbers before calling `apply` again. Rollback uses a higher version
with previously approved content, never a decreasing pointer.

Runtime publication validates outside a short write lock, checks the expected
version again, then swaps one `Arc<Snapshot>`. Requests release the read lock before
route selection and hold no lock across network I/O. Retired snapshots live until
the last request releases them. This is not a claim of lock-free operation.

Only `Router::catalog(authorized_group)` is a public projection. It omits upstream
addresses, IDs, credentials and raw tuning documents. Release documents are
internal control-plane records. Errors retain no SQL or upstream diagnostic text.
`Attempt`, `Endpoint`, and `CredentialRef` have redacted debug output; credential
values have no field in the route configuration or decision types.

## Validation and benchmark

The integration test bridge includes pure routing tests and isolated PostgreSQL
tests. No live model provider or paid API is used. With the pinned Rust toolchain:

```sh
cd apps/lmm-core
rustfmt --edition 2024 --check src/routing/mod.rs tests/routing.rs
cargo clippy --locked --test routing -- -D warnings
# Mock/in-memory tests only, without PostgreSQL:
cargo test --locked --test routing -- --skip postgres_
# Full tests: DATABASE_URL must point to a DISPOSABLE PostgreSQL server.
# SQLx creates separate test databases; do not use production credentials.
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/postgres \
  cargo test --locked --test routing -- --nocapture
```

The dedicated `Rust routing contract` workflow performs these checks on draft PRs
targeting the reconstruction branch. It has no production or deployment access.
The ignored test below is an opt-in pressure benchmark, not a skipped correctness
test. It uses configured model cardinality, two weighted mock destinations per
model, concurrent workers, pre-measurement warmup and a start barrier.

```sh
cd apps/lmm-core
rustc -Vv
ROUTING_BENCH_REQUESTS=1000000 ROUTING_BENCH_THREADS=4 ROUTING_BENCH_MODELS=1000 \
  cargo test --release --locked --test routing routing_benchmark -- --ignored --nocapture
```

Record the commit, compiler, CPU, available memory, OS, thread count, model count,
and full output. Repeat with 1/2/4/8 threads and larger catalogs on the SAME machine.
The output measures route + first-attempt construction only: wall time, throughput,
and one-in-100 sampled p50/p95/p99 latency. It includes RNG/allocation costs but no
HTTP, database calls, tokenization, or settlement. Sampling/timing overhead is
included, and the fixed every-100 sampling pattern is not a full latency census.
Compare like-for-like runs; do not present these numbers as end-to-end API capacity.
No benchmark result is claimed until an actual run has completed.
