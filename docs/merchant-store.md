# Merchant shop

The Go backend owns shop orders, inventory and settlements. Rust is not part of
this feature. The public catalog is `/store`; delivery pages are
`/store/claim/{opaque-token}`. Browsing does not require an account. Checkout,
seller settings and order histories require an enabled platform account,
including accounts whose API console has not yet been activated.

## Listing and delivery

Every account may prepare a product draft and submit it for administrator
review. Administrators may review their own products. Editing approved product
content, price, payment methods or delivery requirements returns the product to
draft. Restocking does not change an approved product's description. The
official badge is derived from the seller's current enabled administrator role;
the browser cannot assign it. A superadministrator seller is exempt from sale
fees; an ordinary administrator seller is not.

Card-key, plain-text and custom-text templates use encrypted text inventory.
Bulk paste and file import treat each nonempty line as one item. Sequential
delivery uses the import position, including items imported in the same second.
Random delivery chooses from available items. Checkout reserves inventory in a
transaction, preventing concurrent buyers from receiving the same item.
Fulfillment releases the purchased text only after trusted payment settlement.

Pickup links use 32 cryptographically random bytes encoded as 43 URL-safe
characters. The database stores a hash for lookup and an encrypted token for
the buyer's order history. A product may additionally require its purchasing
account to sign in, an independently chosen pickup code, or emailed delivery
links. Seller order views and public APIs contain neither pickup credentials,
inventory text, gateway keys nor buyer email addresses. Pickup codes are
password-hashed. Delivery text is never sent in the fulfillment email.

Only the exact pickup page/API and dedicated signed shop payment callbacks
bypass the inbound IP policy. Pickup requests
retain token, account, pickup-code and rate-limit checks. Product browsing,
checkout, seller settings and administrator operations retain the IP policy.
Pickup pages and responses use `Cache-Control: no-store` and
`Referrer-Policy: no-referrer`; their credentials and referrers are excluded
from Nginx access logging.

The IP exception does not extend to sign-in, OAuth or session refresh. For a
product requiring buyer login, a buyer on a restricted network needs an already
valid session or must first sign in on an allowed network. The pickup page
explains that requirement and lets the buyer save the private link. Merchants
wanting pickup from any network may disable the login requirement and enable
pickup-code protection instead.

## Prices, payment and fees

Integer credits are the price and wallet source of truth. One US dollar always
equals 500,000 credits. Product prices, order quantities, fees, promotion costs
and transfers stay within the wallet's JavaScript-safe integer bounds. Gateway
cash amounts are separately frozen in minor units together with their currency,
exchange rate and merchant gateway snapshot. A callback compares that frozen
quote; it must not recalculate wallet credit from a current dollar amount or
exchange rate. A successful browser return URL never settles an order.

Seller payment methods are independently selected and default to off:

| Method | Recipient of the cash or balance | Platform wallet behavior |
| --- | --- | --- |
| Platform balance | Merchant platform wallet | Debit the buyer and credit the seller, less the seller's fee |
| Platform Waffo Pancake | Platform gateway | Credit the frozen sale price to the merchant wallet after verified payment |
| Platform Linux DO | Platform gateway | Require an explicit LDC settlement unit and configured rate; do not assume CNY or USD |
| Merchant Epay | Merchant's gateway | Cash goes directly to the merchant; no sale-price credit is created |
| Merchant Waffo Pancake | Merchant's gateway | Cash goes directly to the merchant; no sale-price credit is created |

The shop's root setting `linuxdo_units_per_usd` explicitly states the number of
Linux DO credits (LDC) paid for one USD of product value. It defaults to empty,
which disables this platform method. It never borrows the recharge page's
implicit conversion or treats LDC as CNY. Root may set a positive decimal for
future quotes; already issued orders keep their frozen amount and rate.

Merchant gateway credentials are encrypted in tenant-owned shop rows, never
written to the platform's payment options. Merchant external gateways may be
enabled only when their wallet balance is strictly greater than 5,000,000
credits (10 USD). An enabled gateway alone does not enable it for a product;
both seller gateway settings and product payment methods must allow it.

The default platform fee is 100 basis points (1%). It is charged to the seller,
not added to the buyer's price, and transferred to the configured
superadministrator account. Root may change the fee for future orders. The
order freezes its fee and recipient at checkout. A non-balance checkout reserves
the seller's fee immediately, so a later merchant purchase cannot consume the
fee needed to fulfill an already paid order. Insufficient seller fee balance
pauses trading. Merchant platform balances cannot be withdrawn and may only be
used for platform consumption.

Disabling a merchant stops new orders without cancelling already issued payment
obligations. A verified payment still delivers to the original buyer and credits
the original merchant wallet. The frozen fee recipient is never silently
replaced by the current setting; an unavailable original wallet or recipient
requires reconciliation while its reserved inventory and fee remain protected.

Gateway callbacks have a dedicated shop namespace and require a valid
signature, frozen merchant binding, order reference, amount, currency and
provider transaction identity. They never enter the existing top-up
settlement path. Provider receipts have a unique owner so one provider payment
cannot settle two orders. Request-key replay returns the original checkout;
different parameters with the same request key conflict.

## Reservations and reconciliation

An unissued payment reservation may be cancelled or expire locally, returning
its inventory and held seller fee in the same transaction. Issued payment
sessions must not release stock or fees based solely on a local timeout: the
buyer may have paid while a callback was delayed. Such orders remain visible as
awaiting reconciliation. Limits on pending orders per buyer and product prevent
a single buyer from creating unlimited unpaid reservations. Gateway amount
precision, minimum payable values and payment-session expiration are enforced
by the adapter; a zero cash quote cannot deliver paid inventory.

Pancake reconciliation uses its frozen upstream checkout expiration, a grace
period, and a complete verified payment ledger before concluding that an unpaid
session is closed. Unknown, incomplete or still-pending provider state keeps the
reservation intact. Generic Epay providers have no common trustworthy close
protocol; their unresolved issued orders remain pending provider verification.
The platform must not describe a local expiration as a verified refund.

## Promotion, consent and mail

The default promotion price is 500,000 credits per 30-day month. Administrators
may change the price of future promotion purchases. A confirmed promotion has a
static recorded cost and expiry, appears ahead of ordinary products, and shows
a sparkle with an explanatory tooltip. Repurchase extends an active period.

Before the first nonofficial purchase, the account explicitly accepts the
versioned merchant disclaimer. Subsequent purchases consult that persisted
acceptance. The notice remains available in the shop. Updating its version
requires a fresh acceptance. Official-product purchases do not require the
third-party notice.

Optional fulfillment emails are queued transactionally after payment and sent
through a durable leased outbox. Workers use bounded SMTP connections, retry
without exposing raw SMTP errors, and stop with the application's lifecycle.
Emails use the existing buyer account email, never a seller-provided recipient.
A shop-specific durable verification fact must match that buyer and the current
address. Legacy email fields or registration settings do not establish ownership.
Until verified, optional email delivery waits while normal web pickup remains
available. Six-digit verification challenges are encrypted, expire in ten
minutes, allow at most five attempts and have a one-minute send cooldown. A
successful code is single-use; an address change invalidates both the fact and
challenge. Multiple API nodes cannot concurrently own the same email lease.

## Release and database boundary

This feature adds 13 tables prefixed `merchant_store_`; it does not rebase
credits, rewrite existing wallet balances, reset payment settings, or delete
historical records. Stock and credential encryption use
`MERCHANT_STORE_ENCRYPTION_KEY`, with the existing `CRYPTO_SECRET` as a strong-key
fallback. Preserve the encryption key with the database backup. Missing or weak
key material fails closed instead of saving plaintext inventory or credentials.

The normal migration registration includes the new shop models. Before a
production release, require an isolated database clone with full preservation
comparison, review the exact additive DDL, prove the new version can apply and
verify, and prove the previous Go version can still verify the existing schema.
Do not reuse an older pre-shop schema proof or execute a historical financial
rebase as part of the shop migration. Go/Web release, signed artifacts,
deployment acceptance and real provider/payment acceptance are separate gates.
