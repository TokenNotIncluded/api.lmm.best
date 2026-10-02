# Responses WebSocket correlation (Go and Rust transport)

Both transports implement the stream/control correlation contract for #382.
The Rust Responses WebSocket route is mounted
in the production composition only with the fail-closed unconfigured service.
No production WebSocket upgrade/provider/billing adapter is enabled. This contract
is exercised through the explicit test service, not through a production
billing/provider adapter. Production Rust provider/billing enablement remains
separate work; this change does not enable it.

## Envelope boundary

- `stream_id` is an optional string on the client WebSocket envelope. Omitted,
  null, or empty identities do not add a field to gateway-generated errors.
- Flat and nested `response.create` requests remove `stream_id` from the
  normalized HTTP request object. The outgoing WebSocket create envelope may
  carry the top-level identity. A nested response identity is not authoritative.
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

When the active response ID is known, a different control target `response_id`
can identify its own rejection. The cancel rejection codes `response_not_found`,
`response_not_active`, and `response_already_completed` identify a unique compatible
pending cancel. Conflicting explicit stream/response targets do not qualify for
this inference. A cancelled turn is finalized by its response terminal event,
not by a rejection of the cancel request.

Controls owned by a finalized turn move into recent resolved history, preventing
completed turns from exhausting the pending registry. Controls with no event or
response reference are forwarded as before; unreferenced data chunks do not fill
the registry. A pending registry limit of 32 controls and 64 KiB of retained
identity strings rejects additional controls locally before forwarding them.
Recent resolved controls and completed response IDs each have the same bounded
history. Duplicate control event IDs in retained history are rejected locally.

Explicit response/stream identities from earlier turns bypass current-turn
observation. Unidentified provider errors retain the existing active-turn failure
behavior: a provider must supply a usable reference for reliable control
attribution. A top-level provider `event_id` not present in retained history may
be the provider's own output ID; it is not assumed to identify a client control.
History is bounded, so references older than that history need an explicit stream
or current response identity. No transport can distinguish a wholly unreferenced
late error from a current-generation error on timing alone.

## Validation and production boundary

Go and Rust tests cover flat/nested envelope isolation, create/error identity,
malformed metadata, overlap, cancellation, sequential and omitted IDs, explicit
provider identity preservation, pending-control errors, foreign/late events,
bounded tracking, and legacy controls. Go additionally verifies that the HTTP
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
```
