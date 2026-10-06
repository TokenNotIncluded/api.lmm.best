# Merchant payment policy

Gateway accounts, keys, vendor product identifiers and settlement currencies belong to the merchant and remain shared across products. A product selects only currently enabled merchant methods. There is no per-product gateway or new schema.

`GET /api/store/payments/settings` retains channel `enabled` and adds `categories`, `category`, `category_enabled`, and `effective_enabled`. `PUT /api/store/payments/categories` requires both boolean fields `platform_enabled` and `external_enabled`; it accepts no merchant identifier. All enabled authenticated users, including L0 users, can create merchant products and configure their own methods. External channel eligibility still requires the existing credit balance threshold.

The two master flags are policy rows in the existing gateway table: `category:platform` and `category:external`. They contain no credentials. The central real-provider whitelist excludes both policy rows from channel lists, secret reads, product selections and checkout. When a category has no policy row, only existing explicitly enabled real channels provide compatibility enablement; a new merchant defaults off. Before changing a channel, the same merchant transaction persists any missing master using the pre-edit compatibility state. Enabling the first channel cannot implicitly turn on a new merchant's master. An explicit off remains off when a channel is changed.

Closing a category preserves credentials and private product selections. Public views expose only the remaining effective configured methods. Subsequent new orders and first invoice issuance are rejected. Already frozen invoices, verified payments and delivered inventory retain their original account, amount, currency and credit settlement obligations. Replaying an existing order or identical frozen binding does not create a new order or reprice it.

## Transaction locks and fulfillment

- New checkout: product row, sorted buyer/seller/fee-recipient user rows, then category/channel checks and inventory reservation. Idempotent existing-order return precedes current category checks.
- Category and channel edits: seller user row, then policy/channel write.
- First invoice binding: product row, order row, seller user row, then current category, channel and seller eligibility. An identical existing binding returns before current policy checks.
- Fulfillment continues through the encrypted order context in `loadMerchantStorePaymentContext`, signed callback/reconciliation verification, and `CompleteMerchantStorePayment`. It does not load current category or channel enablement. Changes to the platform catalog, payment keys or merchant switches do not rewrite frozen invoices.

Product selection validation reads current merchant policy without introducing a new wallet lock. Closing a master may leave a saved private selection inactive; checkout remains protected by the serialized seller checks. Merchants can remove inactive selections before saving the product again.

## Platform catalog and Linux DO pricing

Public `platform_payment_catalog` is a whitelist DTO derived from the existing operator payment catalog and runtime dedicated-gateway declarations. It contains display name, payment type, supported/configured status and a stable unavailable code. Unknown or unsupported settlement adapters cannot become store order providers. `platform_payment_methods` contains only supported and configured methods. The implemented platform set remains balance, Waffo Pancake and Linux DO; external merchant ePay and Pancake keep their original adapters.

New Linux DO invoices require the exact existing Linux DO ePay endpoint, credentials, enabled platform method and explicit LDC pricing in `PayMethods`. No CNY exchange fallback or dedicated shop rate applies. The historical `LinuxDOUnitsPerUSD` config field is retained for record compatibility and does not calculate new amounts.

Both recharge and shop pricing parse the same direct or paired platform pricing contract in `pkg/paymentpricing`. Recharge keeps its group, channel, tier and coupon adjustments. Shop invoices use the base contract and exact integer quotient/remainder ceiling to the minor currency unit. The platform credit denomination remains 500,000 CREDIT per USD.

New encrypted snapshots add optional `pricing_source=platform_pay_methods` and complete `platform_pricing` evidence. Existing `UnitsPerUSD` and `FrozenUSDFX` fields hold the original declared direct or settlement rate solely as legacy integrity fields. They are not a derived USD exchange rate and do not calculate new invoice amounts, wallet credit value or merchant proceeds. `amount_minor`, currency and frozen provider identity remain authoritative payment evidence. Go85's existing reader ignores optional evidence and validates the same frozen fields; it never recomputes the invoice amount from those legacy rate fields.

Global fees, fee recipient and promotion prices can be changed only by Root. Merchants and administrators retain the existing public fee/promotion quote DTO, which contains no fee-recipient identifier or credentials.

## Release and rollback

Do not activate the new master policy while an old Go85 worker can handle store checkout. Go85 does not know master rows and would ignore a closed category while a real channel remains enabled. Before rollback, disable the corresponding real channels, or prove that no explicitly closed master coexists with an enabled real channel. New LDC invoice issuance must also be stopped before returning to Go85, which still uses the historical dedicated shop rate. Previously frozen invoices remain readable and fulfillable on either version; there is no claim that arbitrary new configuration is runtime compatible with Go85.

The market AI release constraints still apply separately: keep both market AI modes off during mixed-version operation; before Go85 rollback, disable both modes and resolve all market pending/running jobs. This change adds zero tables, columns or indexes beyond the separate market AI migration.
