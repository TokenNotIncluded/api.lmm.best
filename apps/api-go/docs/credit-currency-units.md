# Credit currency initialization

Wallet balances are integer credits. `CreditsPerUSD` is an immutable option,
created once from one authoritative database snapshot of the legacy settings:

`CreditsPerUSD = QuotaPerUnit × USDExchangeRate × TopUpPlatformUnitsPerCNY`

Existing balances, prices, paid or pending order snapshots, refund credits and
financial ledger entries are not rewritten. Retained legacy price tables use
`CreditsPerUSD / QuotaPerUnit` to expose equivalent real USD prices. Subsequent
exchange-rate or recharge-setting edits do not change the anchor.

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
