# Payment runtime integration

`Runtime` supplies a serial recovery worker, payment-specific readiness,
bounded HTTP admission and coordinated drain. It is an in-process component,
not a fourth program. Constructing it does not enable payments or start IO.

## Construction and lifecycle

Call `NewRuntime(service, probe, RuntimeOptions{})` with the existing payment
`Service`. The probe is mandatory. It must verify the payment-only database's
runtime write permissions and the Core payment service's availability and
separately scoped authority. A generic host health response is not sufficient.
There is deliberately no default successful probe.

Default limits are eight orders per batch, eight concurrent HTTP requests,
a one-second interval and a twenty-second operation deadline. The worker is
serial. Work and request handlers keep their permits until they actually return;
an HTTP deadline does not free a slot occupied by a stalled handler.

After binding listeners and calling the existing `modules.Host.MarkReady`, call
`runtime.Start(ctx, host.Acquire)`. Readiness stays false until both the dependency
probe and the first worker batch succeed. A failed probe or batch clears it.
Canceled parents, stopped workers and expired readiness results cannot continue
to advertise readiness. `Start` is single-use and rejects duplicate workers.

Mount `runtime.Wrap(service.Handler())` only behind the existing authenticated
module router. Mount `runtime.Wrap(service.WebhookHandler())` only on the separate
callback ingress. Neither wrapper replaces authentication or provider verification.
`runtime.Ready()` is payment readiness; the generic host's `/health/ready` remains
host readiness. Apply server read/write/header timeouts, callback rate limits and
body/query log suppression at the ingress. The wrapper does not create sockets.

Shutdown order:

1. Stop admission with both `host.BeginDrain()` and `runtime.BeginDrain()`.
2. Stop listener admission. Wait for `runtime.Wait(ctx)` and `host.Wait(ctx)`.
3. Only after both waits succeed, close the payment database and Core clients.

A wait timeout does not authorize closing resources used by accepted work.
Drain stops new polling but does not cancel an already accepted worker batch or
callback. An unresolved write retains the existing lease and operation key.

## Core-only recovery

`Step` requires the original Go channel binding only for provider writes
(`checkout`, `refund_submit`). Core receipt lookup, lost-hold lookup and accepted
proof settlement do not perform provider IO and no longer require a Go adapter.
Rust still owns proof verification, intent binding, authorization and receipts.
Missing Go configuration does not turn an API observation into accepted proof,
release held refunds, issue another refund, or repeat an already applied credit.

## Executed evidence

Base input: PR #879 at `acdfe3d50439b79255bb7b91dd6022849c250849`.
The unchanged dependency files were copied through the connected GitHub reader
and checked against their Git blob hashes. No source shim or replacement Core
implementation was substituted for production Go code. The existing test
`fakeCore` and `memoryStore` remain explicit simulation boundaries.

Using the available Go 1.23.2, the following standard-library source subset was
compiled and tested with the race detector:

```sh
GO111MODULE=off GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test -race -count=1 \
  types.go service.go worker.go postgres.go runtime.go fixtures_test.go \
  channel_recovery_test.go settlement_recovery_test.go runtime_test.go
```

The four settlement regressions failed before the new worker change and passed
after it. The eight original channel regressions also passed. Runtime tests cover
startup, dependency failure/recovery, real `Service.Work` invocation, duplicate
start, bounds, accepted-request drain, canceled-but-still-running handlers,
worker panic isolation, host/parent shutdown, late admission and worker drain.
Runtime tests were repeated twenty times with `-race`; the same source subset
passed `go vet`.

These results do not substitute for the repository's pinned toolchain, full
module build, real PostgreSQL, Rust/Go operating-system restarts, real providers,
or browser tests. The prescribed mise command did not start: `mise` was missing
(exit 127). No Rust toolchain, A09 database lease or private fixture was available.

## Outstanding integration ownership

This file is an A05 interface handoff, not evidence of normal-host integration.
The shared `composition/runtime.go`, app configuration, callback listener and
module selection still require S11 integration. No shared file is overwritten by
this change. S11 must supply a real readiness probe; Core's current public
capabilities response advertises identity/events/commerce, not a frozen payment
readiness contract. Do not invent a payment feature flag or substitute a fixed
`true` result.

A03's currently visible PR #884 contains opening preflight work, not the frozen
`LegacyPaymentBasis` needed for historical refunds. Native and legacy refund
basis handling cannot be conflated. Rust still intentionally refuses refund-hold
release until provider dispatch is authoritatively bound. That refusal remains
unchanged. ePay's missing Rust verifier, other main-provider/subscription parity,
and the real signed-provider/dual-process fault runner also remain outstanding.

A05 is not complete and this change does not authorize live payments or deployment.
