# Credit currency initialization

Wallet balances are integer credits. `CreditsPerUSD` is an immutable option,
created once from one authoritative database snapshot of the legacy settings:

`CreditsPerUSD = QuotaPerUnit × USDExchangeRate × TopUpPlatformUnitsPerCNY`

Existing balances, prices, paid or pending order snapshots, refund credits and
financial ledger entries are not rewritten. Retained legacy price tables use
`CreditsPerUSD / QuotaPerUnit` to expose equivalent real USD prices. Subsequent
exchange-rate or recharge-setting edits do not change the anchor.

`LegacyPricingQuotaPerUnit` captures the retained pricing calibration in the
same transaction. After initialization, ordinary and bulk option writes may
only re-save the numerically identical `QuotaPerUnit`; they cannot change it.
The baseline itself is read-only. Canonical USD bridges reject a node whose
runtime calibration differs from this baseline, and startup verification
rejects authoritative database drift (including writes by older binaries).
Runtime settings refresh also refuses a mismatched calibration and preserves
the running node's fixed debit scale instead of applying an older writer's edit.
This prevents a USD price edit on one node from being charged using another
node's different legacy scale. No new pricing change should be made through
an older binary while the fleet is rolling between currency versions.

Before starting a new binary in verify mode, run its normal `migrate --apply`
command against the intended database. Apply mode initializes the option;
`migrate --verify` and verify-mode server startup only read and validate it.
Missing or invalid initialization blocks startup and financial conversions.
There is no fallback that equates one CNY to one USD. Concurrent apply nodes
create a single option and all publish the durable winner. Older binaries may
ignore the additional option; no existing schema or legacy price scale changes.

`wallet_display_currency` is an owner-scoped user preference: empty follows the
interface language (Chinese uses CNY; other languages use USD), or the user can
choose CREDIT, CNY or USD. It does not alter payment `settlement_currency`,
balances, prices or charges. OpenAI-compatible billing fields and token quota
queries always return real USD, regardless of display choice or current FX.

Public relay `amount_usd` tips also name real USD: convert with `CreditsPerUSD`,
round half away from zero to integer credits, and reject out-of-domain amounts.
The retained tip ceiling and withdrawal minimum remain the historical integer
credit policies (`LegacyPricingQuotaPerUnit × 100` and `× 10`), rather than
silently increasing either by the migration exchange rate. The config endpoint
returns both raw credit thresholds and their real USD equivalents. Historical
pending tips and withdrawals keep their original integer ledger entries; a
withdrawal transfers only the unwithdrawn credits and cannot be repeated.

Model-usage SVG amounts use the same immutable anchor. The models layout accepts
`currency=CREDIT`, `CNY` or `USD`; without it, `lang=zh` and `zh-TW` choose CNY
and other languages choose USD. Displaying CNY uses the current exchange rate.
An unavailable anchor or required exchange rate rejects the monetary response.

Focused validation:

```sh
cd apps/api-go
go test -race ./common ./model ./controller -run 'Test(CreditUnits|WalletDisplay|StatusCredit|BillingQueries|QuotaQuery|SelfSettlement|ConcurrentLocale|GetStatus)'
cd relaykit
go test -race ./dto -run '^TestWalletDisplayCurrency'
```

The optional PostgreSQL initialization test uses `TEST_POSTGRES_DSN` and
`TEST_POSTGRES_ISOLATED_SCHEMA=1`, creating and removing its own test schema.
Only point these at an explicitly isolated test database.

### Decimal recharge presets

`payment_setting.amount_options` accepts exact positive JSON decimal numbers. The retained non-TOKENS configuration is in legacy USD batches; user CNY/USD display preferences do not convert these configured values. Existing TOKENS configurations still name integer ledger CREDIT. With the fixed production scale, `3.5` USD grants exactly `1750000` CREDIT; `0.000002` USD grants one CREDIT. Values that would produce fractional CREDIT, overflow the safe wallet integer domain, or contain invalid JSON types are rejected without rounding. AmountOptions and AmountDiscount decode atomically, including existing persisted decimal configurations.

Discount keys accept the same precise decimal amounts, with integer keys preserved for old JSON. `3.5` and `3.50` denote the same preset; conflicting factors are rejected. Discounts still match the exact preset, preserving the existing group/payment/coupon calculation and rounding order. This does not rewrite old orders, credit balances, payment records, or discount levels. The JSON/visual editors display the configuration unit explicitly and retain invalid JSON rather than treating it as an empty catalog.
