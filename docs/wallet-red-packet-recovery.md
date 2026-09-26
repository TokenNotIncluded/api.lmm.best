# Wallet and red packet recovery

## Wallet confirmation

Checkout responses expose the local `trade_no`; legacy Epay `out_trade_no` and
Creem/Waffo/Pancake `order_id` are also accepted. The browser binds that identifier
before a same-tab redirect. It never infers payment from a newer history ID,
a similar amount, an increased balance, or checkout URL parameters.

Pending presentation receipts are scoped to user ID, retained for up to 24 hours
(up to ten outstanding attempts), and checked against that user's authenticated
billing history. Receipts contain no gateway token or authentication material.
They do not credit balances. A local receipt is consumed only after the confirmed
order's animation has been visible; reduced-motion users receive a static result.
The cloud's final size follows the latest actual balance, including concurrent
usage. Order confirmation is resumed on return/focus without a continuous
24-hour polling loop.

The balance card precedes the checkout form. A passive version notice compares
the loaded entry asset with a freshly fetched HTML entry on resume. It never
forces a reload while a payment or form is in progress. This makes old tabs
observable; it is not evidence that CDN or origin drift caused an earlier report.

## Removing inactive red packets

Administrators can remove exhausted, expired or disabled packets with a
confirmation dialog. A packet with claim history that is still enabled and has
stock cannot be deleted; the backend rechecks that condition under the same
packet-row lock used by claims. End timestamps are inclusive.

Packets with history are soft-deleted. They disappear from the management list,
stop accepting claims, and retain the packet, claimed inventory, source codes,
and claim audit. The old share URL remains read-only so authenticated recipients
can retrieve only their own previously received rewards. Unclaimed inventory
bindings are released without deleting the underlying codes. Never-claimed
packets retain their existing physical-delete semantics. Repeat deletion is
idempotent. Existing administrator authorization and audit logging are unchanged.

### Deployment

This change needs both web and Go updates; it is not frontend-only. Run the
project's normal database migration/apply procedure before starting the new Go
backend in verify mode. `RedPacket` adds the nullable indexed `deleted_at` column;
both primary migration inventory and route schema verification include it.
Do not expose an older Go binary to the migrated database after deleting a packet:
old code does not apply the soft-delete scope. A backend rollback must first
account for tombstones or keep the new red-packet handlers disabled.

All regression fixtures use synthetic accounts, codes and orders. They do not
create, pay, delete or redeem anything in production.
