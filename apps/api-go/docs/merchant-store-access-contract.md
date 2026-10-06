# Store access and merchant terms

This source adds a capability-5 contract. It does not activate that capability,
apply production DDL, or prove a capability-5 deployment. The current base still
has binary capability 3. The central migration owner registers and qualifies
the final combined schema before exposing the feature.

Before floor 5, SQL visibility uses the existing `test_mode` column and ordinary
product/order saves omit all access columns. Public products remain browsable,
purchasing requires an account, and access/guest/terms writes fail closed. Terms
reads return an unconfigured, nonrequired view without reading new tables.

At floor 5, visibility is `public`, `registered`, or `private`. Registered means
an enabled real account; a guest token is not a registered account. Private
products are visible and purchasable only by their enabled merchant owner,
including draft/pending/published owner purchases. Public and registered
purchases require published status. All financial, stock, sale quota, variant,
promotion, and purchase-limit checks still apply. Saved collections additionally
retain temporarily paused products but this never permits checkout.

`test_mode` is a compatibility alias for private/public. The new canonical field
and alias are saved together, and contradictory input is rejected. New products
default to `purchase_login_required=true`. Allowing guests is incompatible with
account-only pickup; the model rejects that configuration and checkout.

## Merchant terms

- `GET /store/my/terms` and `PUT /store/my/terms` are authenticated merchant
  endpoints. Save accepts `{content, expected_version}`. Content is nonempty
  UTF-8 merchant-authored Markdown, at most 65536 bytes. A change creates a
  server UUID version. A stale expected version returns `STORE_CONFLICT`.
- `GET /store/products/:id/terms` follows the product's visibility. It returns
  `{version, content, required, configured, accepted, updated_at}`. Acceptance
  belongs to the authenticated account, or the exact `X-Store-Guest` identity.
- Checkout supplies `seller_terms_version` and explicit `accept_seller_terms`
  when no agreement for that subject/seller/version exists. The current seller
  row is locked with checkout, and the order freezes version, text and time.
  Missing, outdated or unaccepted terms return `STORE_SELLER_TERMS_REQUIRED`.
- All merchants, including official accounts, need their own current terms.
  Nonofficial purchases also retain the original first-use platform explanation.
  Its guest acceptance is `{version, accepted:true}` posted to
  `/store/guest/disclaimer/accept`; `GET /store/disclaimer` reports the exact
  subject's platform agreement. Merchant revisions never replace this agreement.

## Guest authority

`POST /store/guest/session` returns `{guest_id, token, expires_at}`. The ID is an
opaque UUID and is not an authorization credential. The bearer contains 256
random bits, is stored only as a hash and expires after 90 days. Send it only in
`X-Store-Guest`, never a URL, log or persistent checkout intent. Session creation
does not create a user, email address or account wallet.

Guest checkout uses **`POST /store/guest/orders`**, whose buyer is always the
header's guest identity even if an account cookie/Bearer is also present. The
original `/store/orders` remains an authenticated-account endpoint and rejects
a guest header. Shared browsing/terms/disclaimer reads with a guest header use
guest authority, so a stale account cookie cannot disclose registered/private
products or reuse account agreements.

Guest order endpoints are `/store/guest/orders/:id` and its `pay`, `cancel`,
`reconcile` and `pickup-link` actions. Every action re-resolves the unexpired
bearer and exact order GuestID. Pickup link responses use `{pickup_url}`.
Order-number summaries also require that proof for guest orders, except for
the original seller/current root or existing separately verified mailbox proof.
Private pickup-secret refund access remains order scoped and does not claim
cards as a side effect. Balance purchasing is always forbidden for guests.

Request-key recovery is read only: authenticated accounts use
`GET /store/orders/by-request-key/:request_key`; guests use
`POST /store/guest/orders/lookup` with `{request_key}`. A valid subject's genuine
absence returns 404; missing/invalid guest authority returns 403. An absence
does not prove an in-flight checkout cannot later commit. Keep the original
request key for retries. A guest limit is per durable guest session, not proof
of a natural person or account.

## Schema and remaining qualification

New models are `MerchantStoreGuest`, `MerchantStoreSellerTerms` and
`MerchantStoreTermsAcceptance`. Product columns are `visibility` and
`purchase_login_required`. Order columns are `guest_id`,
`seller_terms_version`, `seller_terms_content`, `seller_terms_accepted_at`.
Guest ID/hash/expiry and subject/kind/seller/version facts are server controlled.

This initial source keeps guest email checkout closed until the independently
signed verification/outbox helper is integrated. Capability-5 positive tests,
guest paid and promoted-free checkout, verified guest mail delivery, final
provider guest identity mapping and native rollback eligibility remain separate
acceptance work. A pending provider operation is not evidence of payment or
refund success.
