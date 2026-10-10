> 历史记录：以下为 v2 复测，不是 v3 当前状态。当前结论见 README.md。

# DP-22 follow-up: admission acknowledgement experiment

Date: 2026-10-10 UTC. Target: `wip/rust-core-go-extensions`; **not `main`**.

## Scope and result

The first isolated experiment reproduced a race: an Nginx `auth_request` chose A,
Nginx had not delivered the request to A, and the rollout controller drained A after
switching the route to B. That request received 503 without calling the mock upstream.

The revised **Python + Nginx reference only** now registers an opaque selection ticket
*under the same lock as route commit*. Nginx passes the ticket to the chosen mock
core. The mock core acknowledges it only **after** inserting a persistent transport
record and incrementing its own in-flight count. The controller retains A while any
pre-switch ticket remains without an acknowledgement. Once the last ticket is
acknowledged, A receives SIGTERM and drains its already admitted streams.

The race regression expects the original chosen A request to finish exactly once,
while old streams continue. The controller does not retry a POST when the ticket
cannot be confirmed. A lost or invalid acknowledgement means the old generation
stays alive; it is **not** assumed safe to drain after a fixed delay.

## Limitations / reasons not to merge the Rust rollout

- The Rust server still has *no* admission-ticket acknowledgement endpoint.
- The Nginx configuration is a disposable fixture; it is **not** production ingress.
- A request selected but never delivered may leave a ticket outstanding forever.
  Cleanup needs a proven terminal-no-execution acknowledgement, not guessed TTL.
- This protocol does not replace authenticated service identity, signed requests,
  a durable rollout coordinator, or an actual reverse-proxy lifecycle.
- The fixture uses SQLite for transport markers. This does not demonstrate the
  real ledger, settlement, pricing, refunds, or unknown-outcome recovery.
- No Rust compiler/toolchain was available for this recheck, so Rust compilation
  and Rust integration are **unverified**. No full 1c1g test was performed.
- Until end-to-end integration and regression pass, leave paid model routes disabled
  and do not push the runtime patch as an accepted zero-error rollout fix.

The prior README and evidence capture the *original* reproduced failure. This file
records the later candidate workaround and does not change the baseline evidence.
