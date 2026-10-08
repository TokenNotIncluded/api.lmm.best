# Optional external shop imports

This integration implements `extore.commerce-import.v1` for the existing merchant shop. It belongs to a later backend/frontend release. It does not change the frozen Go 96 / Web 135 release, production databases, deployed services, release tags or the manual product/card workflow.

The protocol source was read from `TokenNotIncluded/extore/docs/commerce-import-protocol.md`, file blob `0d81aab4a95ab856d8021e99c6babaf8e05f53be`, and its JSON Schema is vendored under `apps/api-go/internal/commerceimport/`. The client permits additive response fields, keeps the original listing and confidential card receipt, and rejects unknown schema, duplicate JSON names and invalid numeric values. Variant IDs are mandatory for this integration even though the published Variant schema omits them from `required`. Missing/null reference prices remain unknown.

## Enable it on a later release

The administrator uses **System settings → Operations → External shop imports** to save:

* `MerchantStoreCommerceImportEnabled`: `false` by default.
* `MerchantStoreCommerceImportTrustedOrigins`: JSON array of up to 50 distinct public HTTPS origins; no path, credentials, query or fragment.

`MERCHANT_STORE_COMMERCE_IMPORT_ORIGINS` is an optional comma-separated deployment override. When set, it overrides the saved switch and list; an empty override disables new connections. The settings page identifies an active override. Credentials are protected with the existing `MERCHANT_STORE_ENCRYPTION_KEY` / `CRYPTO_SECRET` persistent cipher.

Any enabled, signed-in account can configure its own public client ID and use **Product management → Import from an external shop**. A normal browser login is needed to authorize a connection; personal API credentials cannot substitute for the browser session. Merchants register the callback shown by this application with their chosen trusted shop. No store, SKU, currency or platform hostname is built into the runtime.

Transport independently checks public DNS answers at connection time, rejects the whole answer set if any address is private/reserved, and dials a validated literal IP while retaining TLS hostname verification. It ignores proxy environment variables and cookies, never follows redirects, and fixes machine endpoints to the selected issuer. Listing media is retained as source metadata and is not automatically downloaded or published.

## Workflow and boundaries

1. Create the connection and explicitly authorize `products.read`. A separate action requests `cards.issue`; requested permissions are a ceiling, and refused permissions are never restored locally.
2. Authorization uses Code + S256 PKCE. A one-time encrypted server session binds the merchant, connection, issuer and dashboard session. State, issuer, all callback query multiplicities, complete registered redirect target and existing query values are checked before an exchange or denial is consumed.
3. `/api/user/auth/store-commerce-import/callback` serves a dedicated document with no app shell, images, analytics or third-party resources. It immediately replaces the URL and POSTs to the same-origin completion endpoint, where the existing Strict HttpOnly login cookie is validated without rotating it. Both endpoints send no-store, no-referrer and restrictive CSP headers. Verifier, access/refresh tokens and full card codes never appear in the merchant API response or browser storage.
4. Catalog preview shows source fields and unsupported mappings. The merchant confirms each stable external SKU's actual integral credit price and local visibility. USD entry converts exactly at **1 USD = 500000 credits**; CNY/EUR/other source prices are reference text only. This integration performs no FX migration or automatic reference-price conversion.
5. Import saves a draft using the existing product/variant business paths, preserves original source JSON, revision, reference-price text and external identity `(issuer, shop_id, product_id)`, and keeps stable SKU associations on repeat imports. The existing submission/review path still governs publication. Imported stock does not arise from upstream `quota`, `remaining`, output definitions or a specification's name.
6. Merchant-confirmed restock selects an exact imported SKU and current revision. `mode=stock` products retain the manual text/card stock workflow. Request count is constrained by discovery metadata (v1 at most 100); local sellable quantity limits remain independent.

Connection leases are durable database CAS records. Token loading, a persisted single-use refresh-attempt fence, network rotation and atomic token replacement happen under the same lease. A refresh/code exchange timeout leaves the credential/session consumed and requires new authorization; there is no blind retry or process-local-mutex-only protection. Refresh responses may not change grant identity, expand scopes or extend the grant deadline.

## Restock recovery

Before any issue call, the original request bytes and idempotency key are encrypted and committed as `pending`, and an issue-attempt/uncertainty marker is persisted. Recovery uses the original key and exact original bytes. A fresh key cannot bypass an unresolved batch for the same external SKU, including another connection or a new grant.

The received confidential response is encrypted before stock ingestion. Batch identity `(connection_id, grant_id, batch_id)`, canonical validated issuance fields and the exact local product/variant association are checked; unique batch recording, stock creation and `imported` status commit in one database transaction. Changed JSON formatting or additive response extensions keep the same batch identity without increasing stock, while changed card codes or other issuance fields conflict. The complete first response remains encrypted for recovery. Local receipt recovery takes priority over another issuer request. Failed inventory insertion retains `received` state; stale or irrecoverable responses require merchant investigation.

The first explicit upstream validation rejection can prove no issuance. A rejection received after a timeout or another uncertain attempt cannot prove that the original call failed. `issuance_uncertain` preserves this distinction; the UI and database both honor it. `manual_recovery`, expired response windows and a changed grant cannot trigger another issue request. Upstream quotas are historical issuance snapshots, not live quota balances or sales inventory.

Disconnect attempts grant revocation while the issuer remains in the current trusted-origin list, including after the administrator disables new imports. Removing an issuer from that list prevents sending it credentials, so remote revocation then needs merchant verification. Disconnect clears local credentials/sessions/recovery responses and stops integration operations. It preserves imported/unsold/sold card stock, products/SKU mappings, orders, wallet and ledger records. A failed revocation request still disconnects locally. Disconnect or a local refund does not claim that an external card has been invalidated; v1 does not expose redemption/refund eligibility, so that requires manual verification with the merchant.

Imported orders' existing local refund view exposes `external_redemption_status="unknown"`. Buyer, seller and administrator panels display the manual-verification notice, including completed refunds and disconnected connections. This is local source information; it adds no Extore v1 field, redemption query, cancellation action or change to refund amounts/eligibility. Ordinary and historical pre-capability-8 orders retain their existing view.

## Schema and retention

Capability 8 registers exactly six independent integration tables. A capability-7 runtime excludes those tables from historical qualification. On an isolated restored clone, the later release operator can review and run:

```sh
lmm-api merchant-store-writer-gate prepare-commerce-import --expected-current=7 --reviewed-commerce-import-ready
lmm-api merchant-store-writer-gate verify-commerce-import
lmm-api merchant-store-writer-gate activate-commerce-import --expected-current=7 --reviewed-commerce-import-ready
```

Preparation performs shop-only DDL and does not raise the floor. Activation checks schema/index readiness and changes only the durable floor. Existing deployment fences and serving/retained-writer qualification remain required. These commands are not part of the Go 96 deployment.

`RunCommerceImportMaintenance` is registered in the server's managed runtime loops and runs a bounded cleanup pass every minute, including when new imports are disabled. Each secret category processes at most 200 rows per pass. Expired sessions and grant credentials are erased; recovery-response ciphertext is erased at its recovery deadline. Requests freeze their **original** grant deadline. Known imported/nonissued tombstones survive through that deadline plus seven days before cleanup; uncertain issuance retains only the nonsecret audit/SKU blocker once private request material expires. Cleanup never deletes stock, orders, product mappings or financial rows. Each merchant is limited to 50 pending/active connections; a disconnected connection's later reactivation cannot bypass the limit.

## Verification scope

Targeted tests cover a real isolated HTTPS protocol fixture (TLS/SNI, public DNS pinning, redirect/proxy denial, bounded schema parsing, PKCE and full callback checks), authenticated account/session isolation, confirmed drafts/repeated imports, fixed credit semantics, persisted exact-body recovery, SKU stock ownership, local receipt retention and bounded cleanup. Independent app processes compete for the same persistent refresh lease and the same PostgreSQL batch; only one refresh owner and one fresh stock import are allowed.

These are local synthetic-account and isolated-database checks. No real merchant authorization, payment, external card issuance or production migration was performed. Current protocol limitations include live approved-variant quota lookup, a nonconfidential issuance-status query after receipt expiry, and redemption-aware refund/card cancellation. Those capabilities are not inferred from v1 responses.
