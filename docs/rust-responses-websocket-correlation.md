# Responses WebSocket correlation (Go and Rust transport)

Both transports implement the stream/control correlation contract for #382.
The Rust Responses WebSocket route is mounted
in the production composition only with the fail-closed unconfigured service.
No production WebSocket upgrade/provider/billing adapter is enabled. This contract
is exercised through the explicit test service, not through a production
billing/provider adapter. Production Rust provider/billing enablement remains
separate work; this change does not enable it.

## Rust channel capability boundary (#375)

Rust keeps `GET /v1/responses` closed for every channel type, including OpenAI,
Codex, Advanced Custom (58), Sub2API (59), and New API (60). OpenHuman (61) is retired and no longer eligible. The
production listener still mounts `UnconfiguredResponsesWebSocketService`.
A complete, valid WebSocket upgrade request receives HTTP 503 with
`error.code=service_unavailable` before upgrade, channel selection, upstream
connection, quota reservation, or settlement. This is the runtime's unconfigured
service error, not a channel-specific unsupported/non-retryable error.

The Rust channel management API accepts `setting` and `settings` as JSON-object
strings and stores each supplied string without filtering individual fields.
Manually including `responses_websocket_enabled` therefore preserves that field
when the whole setting string is supplied, but Rust does not read it to enable
WebSocket routing. An update replaces the supplied setting string; this statement
does not imply merging omitted settings or validate the Web editor's
create/update/reload serialization. Rust's `/api/status` does not advertise
`responses_websocket=true`; the Web capability reader requires explicit `true`.

The fail-closed upgrade regressions use an actual TCP listener and the standard
WebSocket client's full Upgrade/Connection/key/version handshake. One exercises
the unconfigured production service directly; another uses the existing test
service's call counters to verify that the rejected handshake never enters
per-turn authorization, provider start/observation, settlement, or session cleanup.
The real `/api/status` router regression checks both capability envelope shapes.
These tests cover the disabled runtime boundary, not a Rust provider/billing
adapter or per-channel eligibility matrix.

Enabling Rust later requires a real policy/provider/billing service, the shared
channel eligibility and toggle rules, and regressions for channel settings and
connection-relevant route/auth changes. The current transport retains one
upstream object per session and rejects replacement; it does not reconnect when
Advanced Custom configuration changes. The correlation fixtures below continue
to use a synthetic channel and do not establish production channel support.

## Envelope boundary

- `stream_id` is an optional string on the client WebSocket envelope. Omitted,
  null, or empty identities do not add a field to gateway-generated errors.
- Flat and nested `response.create` requests remove `stream_id` from the
  normalized HTTP request object. The outgoing WebSocket create envelope may
  carry the top-level `stream_id`. Create `event_id` remains local correlation
  metadata and is excluded from both the HTTP DTO and upstream create envelope.
  Channel overrides cannot replace the stream identity. A nested response
  identity is not authoritative.
- Invalid metadata, malformed create, authorization/start failure, rejected
  overlap, and failed control writes use the incoming envelope identity when
  available. Invalid JSON cannot supply a reliable identity.
- Observer and settlement failures use the active turn identity, captured before
  finalization clears the active turn. A successfully finalized turn cannot lend
  its identity to the next turn or idle control errors.
- Provider `response.*` and `error` events lacking an identity receive the active
  identity. Explicit provider identities are preserved, even if different from
  the current turn; text/binary frame kind and unrelated event payloads remain
  unchanged. Explicit foreign identities bypass active-turn observation, so they
  cannot charge or settle another stream. Omitted client identity keeps the
  previous pass-through and active-observation behavior, including providers that
  supply their own identity.
- An explicitly identified cancellation for another stream is rejected locally
  before forwarding or settling anything. Legacy cancellation without an identity
  still targets the one active response.

Authorization order, one-active-turn admission, channel/model locking, reservation,
settlement/reconciliation ownership, and error redaction are unchanged.
No stream identity participates in pricing, rate limits, routing, or authorization.
Go keeps billable partial settlement independent of successful-request rate-limit
commit: failed, incomplete, cancelled and disconnected turns release the success
slot while preserving the existing charge for billable output. Successful
`response.completed`/`response.done` turns commit the success slot once.

## Asynchronous controls and late events

Both runtimes retain the envelope identity for identified controls and cancels.
Provider `error.event_id` and top-level `event_id` references are matched against
pending controls and recent resolved controls. A unique match supplies the
control's incoming identity, preserves any explicit provider identity, and bypasses
active-turn observation/settlement. Conflicting references and unknown nested
client references bypass observation without inventing an identity.
The nested reference names the rejected client event and takes precedence over
an unrecognized provider output `event_id`. A reference to the active
`response.create` settles that turn and releases its success reservation, even
when a cancel is pending. If the top-level identity names a different retained
control or active create, neither turn nor control is consumed. A late nested
create reference cannot settle a subsequent turn.

When the active response ID is known, a different control target `response_id`
can identify its own rejection. The cancel rejection codes `response_not_found`,
`response_not_active`, and `response_already_completed` identify a unique compatible
pending cancel. Conflicting explicit stream/response targets do not qualify for
this inference. A cancelled turn is finalized by its response terminal event,
not by a rejection of the cancel request.
Control matching uses the nonempty top-level `response_id`, falling back to
`response.id` just as turn observation does. An implicit cancel cannot consume an
error for a known foreign or completed response, including before the new turn's
response ID is known. Its pending record remains available for its own rejection,
so that later rejection cannot become an active-turn failure. Exact client-event
references and explicit cancel targets retain their existing priority; an unseen
response ID can still match an implicit cancel while the current response ID is
unknown.

Controls owned by a finalized turn move into recent resolved history, preventing
completed turns from exhausting the pending registry. Controls with no event or
response reference are forwarded as before; unreferenced data chunks do not fill
the registry. A pending registry limit of 32 controls and 64 KiB of retained
identity strings rejects additional controls locally before forwarding them.
Recent resolved controls and completed response IDs each have the same bounded
history. Completed create identities have a separate history with the same
32-entry/64-KiB limits. Identified creates whose `event_id` plus `stream_id` exceeds
64 KiB are rejected before rate limiting or reservation, ensuring every accepted
identified create fits in that history. A retained create or control event ID
cannot be reused by either a create or a control.

A late error naming a completed create at top-level cannot settle the current
turn. A known current `response_id`, or an explicit current `stream_id` different
from that completed create's stream, can prove that the top-level ID is a provider
output ID instead. Such stronger identities pass through the usual foreign-stream
and foreign-response checks before observation. Reusing the same stream ID does
not prove a new turn; without a known current response ID, the completed create
reference is ignored. An old nested `error.event_id` remains a client reference
and always bypasses current-turn observation, even with an apparent current stream.

Explicit response/stream identities from earlier turns bypass current-turn
observation. Unidentified provider errors retain the existing active-turn failure
behavior: a provider must supply a usable reference for reliable control
attribution. A top-level provider `event_id` not present in retained history may
be the provider's own output ID; it is not assumed to identify a client control.
An omitted or empty top-level `response_id` falls back to `response.id` in both
runtimes, so an empty field cannot hide a completed response from late-event checks.
History is bounded, so references older than that history need a distinct stream
or known current response identity. IDs evicted by either count or bytes may be
accepted again; clients should use unique create/control event IDs and distinct
stream IDs if late events can outlive that window. No transport can distinguish
a wholly unreferenced late error from a current-generation error on timing alone.

## Validation and production boundary

Go and Rust tests cover flat/nested envelope isolation, create/error identity,
malformed metadata, overlap, cancellation, sequential and omitted IDs, explicit
provider identity preservation, pending-control errors, foreign/late events,
active-create error references, conflicting client references, bounded tracking,
completed-create references, identity reuse boundaries, and legacy controls. Go
additionally verifies that the HTTP
Responses DTO never serializes `stream_id` and that partial billing does not
consume a successful-request rate-limit slot.

The Rust production policy/billing adapter and listener enablement require
independent review. Passing transport fixtures does not prove production upgrades,
provider interoperability, reservation or settlement through such an adapter.

Run the focused contract tests with:

```sh
cd apps/api-go
GOMAXPROCS=2 go test -race -p 2 ./relay -run 'ResponsesWS|ResponseCancel|CancelTerminal|BuildResponsesWS|NormalizeResponsesWS'
GOMAXPROCS=2 go vet -p 2 ./relay
# From the repository root:
cargo test --manifest-path apps/api-rust/Cargo.toml --locked --test responses_websocket
cargo test --manifest-path apps/api-rust/Cargo.toml --locked --lib status_should_match_the_current_go_shape_through_the_real_router
```
