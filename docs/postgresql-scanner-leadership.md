# PostgreSQL scanner leadership integration

The Go service coordinates three periodic scanners that must run once across a
blue/green fleet:

- automatic channel balance refresh;
- Codex OAuth credential refresh;
- subscription expiration, quota reset, and cleanup.

`LMM_API_INSTANCE_SLOT` is the explicit multi-slot switch. When it is unset,
`main.go` preserves the source-compatible single-instance loops. When it is set
(for example, `blue` or `green`), startup fails closed unless the primary
database is an initialized PostgreSQL pool, and the lifecycle runs these
synchronous leader loops:

```go
controller.RunAutomaticChannelBalanceUpdateWithLeadership(ctx, frequency)
service.RunCodexCredentialAutoRefreshTaskWithLeadership(ctx)
service.RunSubscriptionMaintenanceScanWithLeadership(ctx)
```

The root runtime registry owns those calls. Shutdown cancels their context and
waits for lease release before process-local buffers are flushed and PostgreSQL
is closed. The detached `Start*WithContext` functions remain only for source
compatibility; new lifecycle code should use the synchronous `Run*` APIs.

Each lease holds a dedicated `*sql.Conn`. Followers retry nonblocking
`pg_try_advisory_lock` with bounded randomized jitter and never enter the
scanner callback. Heartbeat or parent-context failure cancels the leader
callback. Unlock occurs on the same PostgreSQL session; if unlock is uncertain,
the physical connection is discarded instead of returning to the pool.

PostgreSQL advisory locks are coordination, not fencing tokens. They cannot
cancel or undo a provider request already accepted upstream. The scanner
contexts prevent most post-lease database writes, but channel balance writes
still lack a per-channel epoch/CAS predicate. A response accepted immediately
before lock loss could therefore race the new leader near a handoff. Strict
hard fencing requires a per-channel version included in the final update.
