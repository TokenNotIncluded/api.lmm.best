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
account to sign in. Buyers can always enter a pickup code and pickup email;
the seller's switches make these fields required rather than hiding or disabling
them. A nonempty pickup code always protects that order, including when the
product does not require a code. A nonempty valid pickup email always requests an
emailed delivery link. Seller order views and public catalog APIs contain neither pickup credentials,
inventory text, gateway keys nor buyer email addresses. Pickup codes are
password-hashed. Delivery text is never sent in the fulfillment email.

Only the exact pickup page/API and dedicated signed shop payment callbacks
bypass the inbound IP policy. Pickup requests
retain token, account, pickup-code and rate-limit checks. Product browsing,
checkout, seller settings and administrator operations retain the IP policy.
Pickup pages and responses use `Cache-Control: no-store` and
`Referrer-Policy: no-referrer`; their credentials and referrers are excluded
from Nginx access logging.

The IP exception does not extend to sign-in, OAuth or standard session refresh.
Cold-open pickup uses the exact 43-character-token route
`/api/user/auth/store-claim/{token}`, within the existing refresh cookie's path.
It validates that existing session directly against the current database,
including expiry, revocation, authentication version, active account and the
order's buyer. It never issues a bearer token, rotates a session or widens the
cookie's path. A supplied bearer always takes priority; an invalid bearer or
another account cannot fall back to a cookie. Cookie-based collection requires
positive same-origin evidence and rejects cross-site Fetch Metadata.
The exact pickup page also skips the application's normal setup/session
bootstrap, so cold-open collection does not call the standard refresh endpoint
or populate a global authenticated account. Other shop pages keep that bootstrap.
Anonymous GET returns safe order metadata and an authorization boolean only;
private delivery text is available only from explicit, authorized POST.
Without a valid buyer session, the page explains how to sign in from an allowed
network and lets the buyer save the private link. Merchants wanting pickup
without any login may disable account-only collection and require a pickup code.

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

New Linux DO invoices reuse the platform's explicit `PayMethods` LDC pricing
and exact Linux DO ePay gateway. Direct and paired pricing share the recharge
parser; shop checkout uses only the base quote, without recharge discounts.
The historical shop field `linuxdo_units_per_usd` remains stored but no longer
calculates new invoices. Frozen invoices retain their original account and
amount. See [merchant payment policy](merchant-store-payment-categories.md)
for the category switches, pricing evidence and Go85 rollback constraints.

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
cannot settle two orders. Receipt identity freezes the provider's public account
and environment as well as its transaction reference. Platform receipts remain
global across merchants using that account; external receipts are isolated by
merchant and provider account. Rotating a private key does not create a new
receipt namespace. Request-key replay returns the original checkout;
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

When the payment adapter verifies a real payment but the original recipient,
merchant wallet or reserved stock cannot complete settlement, it records the
verified receipt and a fixed safe issue code on the order. The buyer sees that
payment is confirmed and delivery needs review. That evidence prevents a stale
negative lookup from releasing the reserved stock or fee. Retrying the same
receipt can fulfill the original obligation once its cause is resolved; a
different order cannot reuse it. A contradictory late payment after a verified
closed order remains an explicit exception without re-debiting a wallet or
silently reassigning inventory.

## Promotion, consent and mail

The default promotion price is 500,000 credits per 30-day month. Root
may change the price of future promotion purchases. A confirmed promotion has a
static recorded cost and expiry, appears ahead of ordinary products, and shows
a sparkle with an explanatory tooltip. Repurchase extends an active period.

Before the first nonofficial purchase, the account explicitly accepts the
versioned merchant disclaimer. Subsequent purchases consult that persisted
acceptance. The notice remains available in the shop. Updating its version
requires a fresh acceptance. Official-product purchases do not require the
third-party notice.

Requested fulfillment emails are queued transactionally after payment and sent
through a durable leased outbox. Workers use bounded SMTP connections, retry
without exposing raw SMTP errors, and stop with the application's lifecycle.
Checkout accepts `pickup_email` and encrypts an order-bound address snapshot;
changing the buyer's account email does not redirect that order's mail. The
product field `email_pickup_link` now means the buyer must fill this field.
The order field of the same name records whether the buyer actually supplied an
address. Email domain names are normalized to lowercase; local parts retain
their case. Outbox rows contain neither plaintext addresses nor pickup links.
Unconfigured SMTP leaves mail queued until configuration is available.

Older orders without an address snapshot retain the verified-account email flow.
A shop-specific durable verification fact must match that buyer and the current
address; legacy account flags do not establish ownership. Their delivery waits
until verified, while normal web pickup remains available. An account address
change invalidates its verification fact and challenge. Multiple API nodes
cannot concurrently own the same email lease.

## Order search

New public order numbers are `MS` followed by 30 uniformly random base62
characters (32 total, approximately 178.6 random bits). They use `crypto/rand`,
remain within payment-provider length limits, and are independent of the
buyer's request key. The database unique index arbitrates collisions with at
most five retries. The internal order ID retains buyer-scoped idempotency;
replaying a request keeps the originally assigned public number. Queries use
exact case matching even under MySQL's default case-insensitive collation.

`GET /api/store/order-search/{trade_no}` returns only a safe status summary.
It contains no buyer email, buyer/seller ID, internal order ID, amount, gateway
data or delivery text. Only the authenticated original buyer may also receive a
paid order's pickup link. Older deterministic numbers remain usable by the
authenticated original buyer or seller; anonymous and unrelated requests get
the same not-found response as an absent order.

Email search requires a separate mailbox ownership challenge:

1. `POST /api/store/order-search/email/send` with `{email}` returns
   `{challenge_id, expires_in, resend_after}`. Sending does not inspect whether
   the address has orders, so valid mailboxes have the same flow and response.
2. `POST /api/store/order-search/email/confirm` with `{challenge_id, code}`
   returns `{search_token, expires_in}` after successful verification.
3. `POST /api/store/order-search` with `{search_token, offset?, limit?}` returns
   `{items, offset, limit, has_more}` for that verified address only. Defaults
   are offset 0 and limit 30; limits must be between 1 and 100.

Six-digit random codes expire after ten minutes, allow five guesses, and are
single-use. Resending invalidates the old challenge and code without resetting
the attempt budget or expiry. The response reports the actual remaining
seconds. Each address has a one-minute cooldown and a rolling limit of ten
sends per hour; the send route also has an IP limit independent of optional
critical rate-limit settings. Search authorizations expire after fifteen
minutes and cannot verify an account, change payment state, or cancel an order.
Challenge and authorization tokens are stored as hashes; emails and codes are
encrypted and use a purpose separate from account email verification.

Only orders with a frozen checkout address are included in email search. Older
orders can still be found through the original account or existing private
pickup link. A verified mailbox may receive paid orders' pickup links, but
collection still requires every existing pickup-code and login check. Responses
use `no-store` and `no-referrer`; the browser keeps search proof in memory only.
Expired authorizations and inactive challenges are cleaned without resetting
live address rate limits.

## Release and database boundary

This feature adds 15 tables prefixed `merchant_store_`; it does not rebase
credits, rewrite existing wallet balances, reset payment settings, or delete
historical records. Stock and credential encryption use
`MERCHANT_STORE_ENCRYPTION_KEY`, with the existing `CRYPTO_SECRET` as a strong-key
fallback. Preserve the encryption key with the database backup. Missing or weak
key material fails closed instead of saving plaintext inventory or credentials.
The fallback reads the explicit environment value, never the process's randomly
initialized `common.CryptoSecret`. Both API nodes must use the same stable key
material. A configured primary key takes precedence; an invalid primary fails
instead of silently falling back. Compare only a purpose-derived hash in
deployment evidence and never print or commit the encryption material.

The normal migration registration includes the new shop models. Before a
production release, require an isolated database clone with full preservation
comparison, review the exact additive DDL, prove the new version can apply and
verify, and prove the previous Go version can still verify the existing schema.
Do not reuse an older pre-shop schema proof or execute a historical financial
rebase as part of the shop migration. Go/Web release, signed artifacts,
deployment acceptance and real provider/payment acceptance are separate gates.

The dedicated loopback PostgreSQL tests use a fresh synthetic database and
independent random schemas. They exercise real row-lock contention, receipt
ownership, pending limits and wallet rollback, and compare two synthetic legacy
tables around the shop migration. This is not a substitute for a complete
production-data clone, all-existing-table preservation or previous-version
verification. Run it only with an explicitly supplied disposable loopback URL.

## 店铺主页与公告

每个商户的公开主页位于 `/store?seller_id=<用户 ID>`，商品目录继续由服务器按访问者身份筛选，测试商品和仅自己可见的商品不会进入公开目录。即使暂时没有公开商品，也可以展示商户简介、公告和头图。商户在「设置 → 店铺资料」编辑自己的内容；头图支持 HTTPS 图片或经过商品媒体校验器检查的 SVG，安全的视觉动画会保留。简介和公告使用现有 Markdown 渲染器。

头像复用平台账户的 Gravatar：服务端对规范化账户邮箱计算 SHA-256，只公开头像图片 URL。账户邮箱、余额、凭证和其他账户设置均不属于公开主页数据；销售邮箱只来自当前访问者可见商品的公开联系方式。

商户资料单独保存在 `users.setting.merchant_store_home`，使用版本号和用户行锁避免同时编辑互相覆盖。通用账户资料/邮箱/角色更新不写入 `setting`，设置表单保留当前店铺资料。全站商店公告保存在独立选项 `MerchantStoreAnnouncement`，当前管理员和超级管理员可编辑；其他商户只能编辑自己的公告。以上功能复用现有数据表，不需要新增数据库结构。
