# Credit currency initialization

The current contract is **1 USD = 500000 integer wallet credits**. The canonical
unit definitions and payment rules are in the
[payment money contract](../../../docs/payment-money-contract.md).
Implementation: [common currency units](../common/currency_units.go) and
[database initialization](../model/currency_units.go).

## Current initialization

`CreditsPerUSD`, `QuotaPerUnit`, `LegacyPricingQuotaPerUnit`, and a configured
`PublicCreditsPerUSD` must equal 500000. Initialization creates missing required
options in a transaction. It does not derive the denomination from current CNY
FX, display preferences, or `TopUpPlatformUnitsPerCNY`.

`migrate --apply` initializes missing options; it is **not** an automatic repair
of an existing different denomination. `migrate --verify` and verify-mode startup
read and validate the database without changing it. Existing non-500000 values
require an explicit, audited financial migration and are rejected by startup.
Do not fix that rejection by manually replacing options or removing validation.

Initialization never rewrites balances, model prices, paid or pending order
snapshots, refund credits, or ledger entries. An authorized balance migration
must use the project's maintenance and migration process, with a single writer,
a recorded plan, and reconciliation. A documentation or code update does not
perform or authorize that operation.

Ordinary and bulk setting updates cannot change the fixed pricing scale. Runtime
refresh and startup reject a scale that differs from its saved basis. Do not
allow an older writer to change financial settings during an upgrade.

## Historical compatibility is not the current denomination

Older initialization logic multiplied a saved quota scale by the then-current
CNY exchange rate and recharge ratio. Some isolated tests retain non-1 `K/QPU`
values to verify historical arithmetic. Those fixtures do not show that current
production startup accepts those values, and they must not redefine the contract
above. Historical records keep the amount, currency, and basis captured for them.

Some stored payment configuration and old request routes still use batch units.
They require a configuration/input adapter, not another wallet currency. Their
full migration and retirement are not complete. New integer-credit quotation
must use the actual credited amount as its authority. A compatibility projection
must not independently choose a preset discount or coupon threshold.

Explicit custom gateway prices and legal promotions remain invoice pricing.
They can make the amount paid differ from the nominal value of credited funds;
neither changes the denomination or makes CNY equal to USD. Never replace an
explicit custom rate with 1 as a substitute for converting its units.

## Display and presets

`wallet_display_currency` is a per-user preference: automatic, CREDIT, CNY, or
USD. It does not change a balance, debit, payment currency, or captured invoice.
USD uses the fixed credit denomination. CNY display additionally uses current
CNY/USD FX. OpenAI-compatible USD billing fields remain real USD.

Existing non-TOKENS preset configuration retains its old batch input format;
TOKENS configuration names raw integer credits. At the fixed production scale,
`3.5` USD corresponds to 1750000 credits and `0.000002` USD to one credit.
Preset parsing rejects fractional-credit and out-of-range amounts. User display
preferences do not convert stored preset keys. `3.5` and `3.50` are the same key;
conflicting discounts must be rejected. Quote, coupon preview, and coupon
application must derive qualification from the same credited integer, while
retaining configured group/channel/preset/coupon factors and their rounding order.

Historical tips, withdrawals, subscriptions, refunds, and model-usage records
must not be reinterpreted using today's FX or a changed price. Consult the
payment money contract before changing their units or snapshots.

## Verification

Use the Go version declared by `apps/api-go/go.mod` and an isolated database.
Focused commands from the repository root:

```sh
cd apps/api-go
go test -race ./pkg/paymentpricing ./controller -run 'Test(CanonicalTopUp|SharedSettlementPricing|QuoteTopUp|ParseSettlementPricing|SettlementQuote|ExactSettlement|CreditRate)'
go test -race ./common ./model ./controller -run 'Test(CreditUnits|WalletDisplay|StatusCredit|BillingQueries|QuotaQuery|SelfSettlement|ConcurrentLocale|GetStatus)'
```

The optional PostgreSQL initialization test uses `TEST_POSTGRES_DSN` and
`TEST_POSTGRES_ISOLATED_SCHEMA=1`. Point both only at an explicitly isolated test
database. These focused commands do not replace full Go tests, payment-callback
and refund reconciliation, or source-matched release evidence. A test listing in
this document is not a claim that it ran for the current change.
