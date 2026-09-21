# Subscription settlement and reconciliation

Subscription BillingSession reserves subscription quota and token quota in the
same database transaction, including additional Reserve calls. Final settlement
persists the actual cost, then commits subscription, wallet and token changes
atomically. A failed transaction leaves the request in `settling`; it does not
claim full payment and must not cause upstream generation to be resent.

`PostTextConsumeQuota` retains the existing meaning of Log.Quota, user used quota,
channel used quota and request count: they measure usage, not collected payment.
Its `Other.billing_settlement` snapshot separates `actual_quota` from committed
`charged_quota`, `subscription_quota`, `wallet_quota` and `token_quota`.
`unsettled_quota` is the outstanding debit; `refund_pending_quota` is a refund
still owed after an unsuccessful downward adjustment. `complete` is true only
after successful settlement. Database error details are restricted to
`Other.admin_info.billing_settlement_error`; the user-visible content states
that settlement was not completed.

The log is a snapshot, not a live reconciliation record. Operators can query
`model.GetSubscriptionBillingResult(requestID, userID)` and retry only
`model.SettleSubscriptionBilling(requestID, userID, originalActualQuota)`.
Do not rerun PostTextConsumeQuota, which would duplicate usage statistics/logs,
or resend the model request. Repeated settlement of the same amount is
idempotent; changing that amount is rejected. `unrecorded` means the session
could not confirm persistence of settlement intent; investigate database state
before compensation. Managed ledger records are retained for replay protection.

Wallet splitting is enabled only for synchronous subscription_first requests,
and only when the chosen subscription and every active subscription permit it.
subscription_only and wallet_first fallback do not split. Async TaskRelayInfo
requests also do not split: their task refund records currently hold only one
funding source. If an async submission exceeds available subscription quota,
settlement returns an error and the durable record remains `settling` with the
actual cost and retained prepayment. A caller must observe that error; a task
submission is not evidence that full settlement succeeded. Enabling mixed
funding for tasks requires persisting both funding amounts in task bookkeeping
and updating task refunds/recalculation first.

No new table is introduced. The seven added SubscriptionPreConsumeRecord columns
are billing_managed, token_id, token_consumed, wallet_overflow, actual_quota,
wallet_consumed and reserved_version. UserSubscription.quota_version increments
on scheduled, manual, batch/voucher resets and paid renewals. Reserve across a
version change is rejected. Old-period negative settlement/refund adjusts the
token but does not subtract from the new period's subscription usage. Positive
overage consumes current capacity under the normal overflow policy. Soft-deleted
tokens remain available for settlement/refund by the original owner and ID;
new reservations still reject them.

Both CLI apply and CLI verify derive their required models from
mainMigrationModels; verify builds a per-column inventory from those models.
The PostgreSQL migration regression verifies each missing column is rejected,
then verifies the upgraded schema and preservation of legacy records.

Spendable token and wallet balances now commit synchronously even when
BatchUpdateEnabled is enabled. Batch mode still applies to usage counters.
All token reservations are authorized and persisted in the database. Limited
tokens require sufficient balance; unlimited tokens bypass only that balance
check and still persist their usage. Redis is not an authorization source.
Post-commit cache notifications invalidate
rather than add a delta, so delayed notifications after hydration cannot count
a credit twice. Tests exercise Redis Lua storage with concurrent wallet and
subscription callers and BatchUpdateEnabled, including stale cache snapshots.

Do not roll this change out concurrently with old writers: stop admission on
old processes, drain requests and FlushBatchUpdates, then stop those processes
before activating the new binary. An old binary can still hold uncommitted
Redis-only token reservations; the new binary cannot recover another process's
in-memory batch queue. This is an upgrade prerequisite, not an automatic
production action performed by the change.

Rolling back to an old binary does not make it safe to resume writes. The old
binary does not understand managed `settling`/`settled` records or quota versions,
and its cleanup can delete managed records needed for reconciliation and replay
protection. Before rollback, stop admission and all writers/cleanup workers,
drain in-flight work and queued updates, then inspect and reconcile outstanding
settlements with code that understands this ledger. Preserve managed records
and verify balances, token usage and period ownership before considering any
resumption of writes. Simply switching binaries or retaining the added columns
does not establish N-1 runtime compatibility; old writes and cleanup must remain
disabled until an explicit compatible recovery plan has been validated.
