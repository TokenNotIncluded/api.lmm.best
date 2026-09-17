# Referral retry preparation

This is source preparation for PR #354 (continuing #344), not a production rollout.

A response error is not proof that the moderation database transaction rolled back. The browser now retains one immutable primitive-valued payload and request ID throughout the dialog, for network errors and `success:false` alike. A synchronous busy ref prevents double-click requests before React rerenders. Reloading or closing the dialog is not an automatic retry; inspect account and ledger state before starting a new operation.

The server's exact duplicate-request path still performs no reward, penalty, account status or authentication-version mutation. It now retries current-state cache publication and token cache invalidation, instead of returning success merely because the event exists. Neither operation publishes an old event's disabled status over a newer appeal.

Each new ban records `revoke_through_auth_version` from its locked user row before the auth-sensitive mutation. Session cleanup selects only still-active sessions at or below that original version, in bounded batches. It locks the selected rows before publishing the existing deny fences and rechecks the version predicate on update. Retrying an old ban after appeal can finish old-session cleanup but cannot revoke a newly issued post-appeal session. The event's original boundary is retained on duplicate requests, not replaced with the current user's version. A legacy event with a zero boundary fails cleanup rather than performing an unbounded revoke; it requires explicit reconciliation. New schema migrations must include the added event column before activation.

This mechanism is retry-driven, not a background outbox. A crash or error after committing requires the original authorized request to be retried. The normal authentication-version fence remains in place. The generic revoke-all implementation and production operations are unchanged.

## Validation

Local: five isolated request-identity tests passed after strict TypeScript compilation; changed TSX parsed; Go files passed gofmt/parser validation. The local Go toolchain is 1.23.2 while the application requires 1.25.1, so no full local model test success is claimed.

Added Go fault-injection regressions require CI execution: cache-publication failure, token-invalidation failure, session-cleanup failure; exact retry keeps one moderation event and unchanged ledger/auth version; an old ban retry after appeal preserves a same-second new session; a second cleanup batch failure resumes only remaining old sessions and rejects a missing version boundary. Complete current-head CI, production-shaped tests and full payment/refund/moderation review remain release prerequisites. No live payment, production database write, tag, release or deployment is authorized by this preparation.
