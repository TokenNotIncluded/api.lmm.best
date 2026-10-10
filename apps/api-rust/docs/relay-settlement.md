# Relay settlement and recovery

The ordinary OpenAI executor uses the Go model/group/tool prices and funding
preference only when composed with `with_option_pricing()`. The fixed-quota
constructor remains for older isolated fixtures; it is not equivalent pricing.
`QuotaPerUnit` defaults to **500000**, as in current Go.

## Financial states

`0014_relay_settlement.sql` adds `relay_settlement_records` and the current Go
managed subscription fields. Each record keeps a server-derived price snapshot
and, once observed, the final usage and integer cost. It contains no provider
credential, request body, or generated text.

| State | Evidence and permitted action |
| --- | --- |
| `reserved` | Budget is held; no final usage intent has committed. Keep the record and reservation. |
| `settling` | Final cost and usage are durable. Retry only the financial transaction. |
| `settled` | Funding, token, usage counters and consume log committed together. Replays do not debit again. |
| `refunded` | A failed/cancelled request without observed consumption returned its reservation exactly once. |

Recovery never repeats an upstream request. It does not infer zero consumption
from a reservation's age. A crash or database outage before final intent commits
can leave a `reserved` record with no sufficient evidence for automatic refund
or final charging. Those records require provider/accounting evidence and an
audited reconciliation decision; they are **not automatically recovered**.

The compatibility marker in `logs` is retained while a request is pending, but
it is not the financial authority for the new executor. Both the settled ledger
state and consume log are updated in the final database transaction.

## Funding behavior

The four Go preferences are respected: `subscription_first`, `wallet_first`,
`subscription_only`, and `wallet_only`. Unknown values default to
`subscription_first`. Selection uses the first active grant that covers the
budget, otherwise its largest eligible remaining grant. It never silently
combines separate subscriptions. A strict active subscription prevents wallet
overflow; subscription-only requests cannot fall back to wallet funding.

Subscription overflow budgets count against concurrent requests before the
wallet is debited. Actual overflow is charged at settlement. Grant resets
increment `quota_version`; refunding an older version cannot erase usage in the
new period. Managed Go subscription records are retained for replay protection.

Non-free prices reserve at least one quota unit. Unlimited tokens skip only
their balance check and retain quota usage accounting. Known generation cost is
preserved when settlement fails; the provider result is not regenerated.

## Process lifecycle

After schema verification, create the service and keep a clone of its
`settlement_tracker()`. Start `spawn_settlement_reconciler(shutdown, policy)`:
its first pass is immediate and later passes scan bounded batches of `settling`
records. Failed recovery retains evidence and logs a warning without disabling
the management API. Multiple processes may run this worker: user and ledger row
locks serialize finalization, and terminal states prevent repeated debits.

On shutdown, first signal the worker's watch channel so no new recovery work
starts. Let HTTP graceful shutdown finish or cancel response bodies. Then wait
for `settlement_tracker().drain_until(deadline)` using the **same** shutdown
deadline. Dropping a response can enqueue a refund after HTTP work ends; an empty
HTTP inflight counter does not prove that this refund committed. Already started
financial tasks remain owned when their caller/worker is cancelled.

A successful drain means tasks finished, not that every stored intent settled.
Storage failures remain visible in the ledger. A deadline failure must be
reported as incomplete drain, not successful financial completion.

Compose `with_valkey(client, crypto_secret, timeout)` with the same secret used
by token authentication. Every committed reserve/refund/settlement deletes the
Go `user:{id}` and HMAC-derived token cache, with a ten-second token mutation
fence that prevents stale readers from republishing old quota. Recovery resolves
the token key from PostgreSQL; raw keys are not persisted in the ledger. The
whole cache operation has a deadline. Failure logs a warning and leaves the
committed financial result intact; it never repeats a debit or turns successful
generation into another provider request.

## Response model diagnostics

The PostgreSQL OpenAI executor observes provider-origin model declarations in
Chat/Completions and HTTP Responses/Compact, including streamed responses. Its raw
provider JSON/SSE tracker reads `model` or a Responses event's `response.model`
before downstream response conversion. Models synthesized by a converter are
not observations.

The selected attempt freezes the client-requested name and the model in the
actual outbound request. This native executor currently forwards the original
request body without channel model-mapping rewrites, so these two names normally
match. Diagnostics do not introduce mapping, change the selected channel, or
change the existing price snapshot, token counts, funding, quota, retry policy,
or provider response bytes.

Useful observations are stored in the existing consume-log `other.response_model`
and durable settlement metadata as exactly three strings:
`requested_model`, `upstream_model`, and `returned_model`. An ordinary exact
response adds no metadata. Case differences, provider paths, compatible dated
variants, and genuinely different models retain the declaration for inspection.
No mismatch boolean is persisted; compatibility is derived from the names with
the [shared comparison rule](../../../docs/relay-response-model.md) and
[test vectors](../../api-go/relay/common/testdata/response_model_compatibility.json).
The first useful compatible alias survives later matching or empty events. A
genuine mismatch can replace that alias and then survives every later event.
Settlement recovery carries the same diagnostic without repeating provider I/O.

| Rust runtime path | Diagnostic scope |
| --- | --- |
| Native OpenAI Chat/Completions and HTTP Responses/Compact JSON and SSE | Provider observations and consume-log/durable metadata. |
| Native Claude / Gemini JSON and SSE | Provider responses are forwarded, but `PgAnthropicGeminiRelayBackend::record_outcome` has no usage-log settlement owner yet. These routes do not emit this consume-log diagnostic. |
| Cross-protocol relay | Remains subject to the existing ownership/capability gate; this feature does not open it. |
| `GET /v1/responses` WebSocket | The ordinary listener still mounts `UnconfiguredResponsesWebSocketService`; no production diagnostic is claimed. |

## Remaining parity work

This path does not yet establish complete Go parity for `tiered_expr`, every
multimodal usage normalization, or WebSocket/video executors. Unsupported
pricing is rejected before provider I/O; that rejection is not feature parity.
Two additional ordinary-price cases still require differential coverage and
implementation: `EnableFreeModelPreConsume=true`, and retaining the initial
model-price snapshot when an upstream retry overlaps an option edit. Current
tests establish their listed cases, not universal pricing equivalence.
Memory/performance comparisons must include tokenizer warmup, real authorized
relay and streaming/cancellation, and the database cost of durable intent.
