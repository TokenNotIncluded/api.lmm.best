# Core contract v1 (WIP)

`funding-cases.json` is executed by both `pkg/accountfunding` in the current Go
backend and the new Rust core. These are selection rules, not proof of billing.
A missing/null order means owner-only; an empty order is invalid. Neither platform
admin status nor team membership alone is a spending grant. Team IDs and personal
IDs are different namespaces even when their numbers match.

The future internal API belongs under `/internal/v1/`. There is no unauthenticated
business RPC in this scaffold. Transport authentication, per-module permissions,
per-user/team authorization, body limits, deadlines, idempotency and audit records
must exist before an extension can issue a mutation. Never trust payer IDs or role
claims supplied by a browser, module or model.

Core-owned data: users, API keys and revocations, account membership/spending,
wallet/subscription/budgets, prices, routes, channel secrets and charge records.
Extensions have no credentials for these tables. Their mutable data must be in
separate storage/schema roles. Do not deploy two writers during migration.

One accepted model request pins its actor, key owner, one payer, price version,
subscription/wallet allocation and budget period. Retry, cancellation, SSE/WS,
async callbacks and refunds refer to the original durable request record. Only
explicit insufficient-funds/applicable-quota results may try the next configured
payer, after verifying that the prior reservation was fully rolled back. Unknown
outcomes, revoked authority, disabled accounts and database errors fail closed.
Never charge a second payer to recover an uncertain first reservation.

Go will consume a durable, bounded core outbox at least once. Consumers deduplicate
by event ID. A Go outage cannot block model traffic or lose accepted money changes;
backlog limits and recovery must be tested before this contract is activated.
