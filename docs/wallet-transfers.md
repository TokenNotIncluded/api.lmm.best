# Wallet transfers

Authenticated users create a transfer from their wallet balance. Creation and
wallet debit commit in one database transaction. A 256-bit random bearer token
produces a private `/transfer#TOKEN` link and QR code. Anyone holding it can sign
in and explicitly claim it; the sender cannot claim their own transfer.

The amount uses integer wallet quota units. The creation request carries an
idempotency key scoped to the sender: retries cannot debit twice, and reusing
that key with another amount is rejected. Claiming and cancellation serialize
on the transfer row and conditionally transition from `pending`. The balance
credit/refund commits with that transition. Repeated claims by the same
recipient and repeated cancellation are idempotent. Overflows roll back the
whole operation. Caches are invalidated only after commit.

Owner history is paginated, newest first, with creation/claim/cancellation
timestamps and a snapshot of recipient ID, username, display name and email.
Recipient inspection exposes amount/status/timestamps and ownership flags;
it does not expose identities or credentials. The claim page tells recipients
which identity fields are shared before they accept. Wallet history refreshes
while open, and on focus. Unclaimed transfers can be cancelled and refunded.
Transfers have no automatic expiry.

Treat both links and QR images as bearer credentials. The UI warns against
public sharing. The token stays in a URL fragment, and API inspect/claim use
request bodies. Login handoff uses tab-scoped session storage and a redirect
without the credential in its query string. Ordinary GET requests never claim.

## Deployment

Deploy the Go backend with native `migrate --apply` before activating the web
release. `WalletTransfer` belongs to both primary migration inventories; route
startup verifies its schema without DDL. This additive table is compatible
with the previous backend schema. During rollback, old code cannot claim or
cancel transfers; retain the table and its records for subsequent recovery.
Do not manually delete records or refund outside the transactional state machine.

Validation uses synthetic accounts in SQLite and isolated PostgreSQL schemas,
including concurrent create retries and competing claim/cancel operations.
Production verification must not mint balances or move real users' funds.
