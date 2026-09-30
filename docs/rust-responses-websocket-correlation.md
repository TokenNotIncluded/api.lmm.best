# Rust Responses WebSocket correlation (candidate transport)

This is partial progress on #382. The Rust Responses WebSocket route is mounted
in the production composition only with the fail-closed unconfigured service.
No production WebSocket upgrade/provider/billing adapter is enabled. This contract
is exercised through the explicit test service, not through a production
billing/provider adapter. Go behavior is unchanged; cross-runtime parity and
production enablement are still outstanding.

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
  unchanged. Omitted client identity keeps the previous pass-through behavior.
- An explicitly identified cancellation for another stream is rejected locally
  before forwarding or settling anything. Legacy cancellation without an identity
  still targets the one active response.

Authorization order, one-active-turn admission, channel/model locking, reservation,
observation, settlement/reconciliation ownership, and error redaction are unchanged.
No stream identity participates in pricing, rate limits, routing, or authorization.

## Remaining work for #382

Go needs its own correlation implementation and runtime parity tests. The Rust
production policy/billing adapter and listener enablement need independent review.
Asynchronous provider errors for pending controls (including explicitly identified
late events from an earlier turn) still use the existing service observation
boundary; this slice does not add a pending-control registry or redefine provider
terminal-event classification. Those semantics must be addressed before claiming
complete per-control correlation or production-ready cross-runtime equivalence.

Run the focused contract tests with:

```sh
cargo test --manifest-path apps/api-rust/Cargo.toml --locked --test responses_websocket
```
