# Guest pickup email

Guest purchase with an empty pickup email remains available without a code.
Only an entered pickup email, or a merchant's email-required purchase, needs
address verification. Guest pickup cannot require an account login.

## Ownership and one new table

`MerchantStoreGuestEmailModels()` exports only
`merchant_store_guest_email_verifications`. Its composite primary key is
`guest_id varchar(36)` plus `email_hash varchar(64)`. Remaining columns are
`challenge_id varchar(36)`, `code_ciphertext text`, `expires_at bigint`,
`attempts integer`, `sent_at bigint`, `sent_times text` (JSON timestamps), and
`verified_at bigint`. Every model field is private JSON; no raw address,
inventory, pickup link, guest bearer, or code is stored in plaintext.

The access implementation owns the real durable guest and immutable
`MerchantStoreOrder.GuestID`. No `User` row is synthesized and no account email
API is called with buyer ID zero. Every send, confirmation, and status call
resolves the exact 256-bit `X-Store-Guest` bearer and current expiration.

Codes use the existing persistent encrypted-secret mechanism with a separate
`guest-email-verification` purpose bound to the guest ID, exact address hash,
and fresh random challenge ID. Account codes, order-search proofs and pickup
tokens cannot substitute. Normalize only the address domain; preserve local
part case.

Under one guest row lock: one active challenge, a ten-minute window, five
committed failed attempts across all entered addresses, sixty-second resend
cooldown, and ten sends in a rolling hour. A resend rotates the code and
challenge without extending the active window or clearing the failure budget.
Changing address consumes the previous pending challenge. Historical verified
address facts remain, so later verification of B cannot redirect an order
already frozen to A. Successful confirmation consumes its challenge once.

## API

All routes retain ordinary store IP policy, no-store responses, bounded request
bodies and the existing critical/email rate limits. No authentication refresh
or global account token is issued.

- `POST /api/store/guest/email/status`, body `{ "email": "..." }`, returns
  `{ "verified": true|false }` for only the header's guest.
- `POST /api/store/guest/email/verification/send`, body `{ "email": "..." }`,
  returns `{ "sent": true, "challenge_id": "uuid", "expires_at": unixSeconds }`.
  The six ASCII digits, address and bearer never appear in this response.
- `POST /api/store/guest/email/verification/confirm`, body
  `{ "email": "...", "challenge_id": "uuid", "code": "001234" }`, returns
  `{ "verified": true }` only after verification.

Required properties must be strings. Null, unknown identity properties and
trailing JSON are rejected. The frontend keeps the email/challenge in local
component state, rechecks status on address changes, and submits checkout only
after a nonempty chosen address is verified. The empty-email case skips this
component. The core checkout calls `storeRequireGuestCheckoutEmail` under its
existing guest lock, after frozen-order replay and before creating a new order.

## Delivery

The existing paid-order encrypted address/hash freezes the destination; no new
outbox column is needed. The unified server-only
`GetMerchantStoreEmailDeliveryPayload(id, leaseToken)` requires the exact active
unexpired lease and locks product, order, then outbox. It checks the immutable
order guest, its original verified address, current paid/refund-pending status,
and encrypted token/hash before producing a private sending snapshot.
Registered buyers retain the former enabled/nondeleted account check.

The raw guest session may expire after payment; this does not discard the paid
email obligation. No recovered guest bearer is needed. Email uses the existing
bounded administrator-configured SMTP sender and safe HTML/plain multipart
layout. Ownership emails contain only the code. Delivery email contains the
frozen product name, variant name, quantity and order number, a pickup button,
and the retained product description/links. It contains no stock text or
pickup code.

Cancelled, expired, pending, reconciliation-pending and fully refunded orders
cannot produce a new delivery payload. Their queue lease becomes terminal.
Partially refunded orders may receive the link, but the claim endpoint still
excludes refunded/held stock. A refund racing a message already in SMTP cannot
recall that message; the protected link does not grant refunded inventory.
An exhausted final sending lease becomes failed even when the queue has no
other available row. SMTP errors persist only a fixed code; acknowledgment and
retry remain lease bound.

## Integration and qualification

This source requires the separately signed access/guest implementation and the
reviewed actual phase-5 capability preparation. New guest verification writes
require that real capability; do not alter the capability constant in a test,
fake a user-zero account, or call a seeded option a positive qualification.
Paid delivery remains a frozen obligation and is not blocked by a newer writer
gate. Schema registration does not activate the capability automatically.

Central must enumerate its actual combined model groups (access, catalogue,
email and earlier refund/coupon groups) and qualify their schema before
activation. A numerical estimate is not a schema proof. Older outbox workers
cannot correctly handle guest orders: two-host serving/rollback/keeper
capability-floor qualification must prevent restoring them after activation.

Source-only handoff explicitly leaves generated route contracts and all
compile/positive runtime checks pending until the signed dependencies are
combined. Focused tests cover guest/address/purpose isolation, fixed failure
budgets, single consumption, lease and snapshot privacy, expired bearer paid
delivery, closed/refunded queues, preserved account-disable checks and safe
multipart layout. SQLite's single connection is not PostgreSQL lock proof.
No test sends real mail or calls a payment/refund provider.
