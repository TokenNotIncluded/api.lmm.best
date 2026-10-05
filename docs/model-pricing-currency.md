# Model pricing currency boundary

Wallet balances remain integer credits. The durable `CreditsPerUSD` anchor K
is independent of later CNY exchange rates or recharge purchase ratios. Stored
legacy model/tool prices and expression snapshots retain their original units.
Token `ModelRatio` is a calibrated credit-per-token rate; completion, cache,
modality and group ratios remain multipliers.

Public `/api/pricing`, `/api/assistant/pricing`, token `/v1/pricing`, OAuth catalog
and assistant cost tools label normalized money as USD with pricing schema 2.
A base token rate is `ModelRatio * 1000000 / K` USD per million tokens. A retained
absolute price is `legacy_price * legacy_credits_per_unit / K` USD. Expressions
scale the whole monetary output, retaining conditions, measurement variables
and tiers. Group and trust multipliers apply once. Stored maps, hashes and
in-flight snapshots are not rewritten by reads.

## Root pricing editor

Use only the distinct RootAuth routes:

- `GET /api/option/pricing`
- `POST /api/option/pricing/validate`
- `POST /api/option/pricing/bulk`

GET returns `success:true,data:{schema_version:2,currency:"USD",
storage_basis:"legacy_pricing_unit",revision,credits_per_usd,
legacy_pricing_units_per_usd,model_ratio_usd_per_million,values,
tool_price_defaults}`. `values` contains JSON-encoded maps for ModelRatio,
CompletionRatio, ModelPrice, CacheRatio, CreateCacheRatio, ImageRatio, AudioRatio,
AudioCompletionRatio, billing mode/expression, ModelPriceLock and tool prices.
Only ModelPrice, tool prices and whole expression monetary output are normalized.
Tool prices are USD per 1000 calls. Hardcoded tool fallbacks are returned
separately in `tool_price_defaults`; do not persist them as operator overrides
unless the operator actually edits those entries.

Both POST routes require `{schema_version:2,currency:"USD",expected_revision,
values:{option_key:json_string}}`. Submit only intended map keys. Success returns
the same complete canonical config as `data`, plus `warnings` and
`locked_models`. Validation is a preview; bulk returns the transaction receipt.
A stale revision is HTTP 409 and requires a fresh GET, never a blind retry.
Missing/wrong schema, currency or non-pricing keys are rejected. Old binaries
return 404: clients must disable monetary editing and must not fall back to
legacy `/api/option/bulk` or PUT with a USD value.

The revision covers all twelve retained maps, the immutable USD anchor and
legacy calibration. The USD writer checks authoritative database calibration
under the pricing transaction lock and rejects stale node caches. QPU is frozen
after currency initialization; the durable `LegacyPricingQuotaPerUnit` baseline,
stored QPU, fixed K and local cache must agree. The USD transaction locks QPU
before the shared price-policy row, matching other currency writers. Existing
model price locks still preserve locked values before validation. Unchanged
canonical values reuse the original stored JSON number/expression; unchanged
whole maps skip writes, so GET-to-save preserves stored bytes and debit.

Legacy option and ratio-import interfaces explicitly disclose their legacy
storage basis. They remain compatibility surfaces, not USD editing APIs.
A price-lock toggle may still use the existing atomic single-model PUT; obtain
fresh USD maps from the canonical GET after toggling. Do not consume its raw
legacy pricing receipt as USD.

## Usage records

New raw pricing metadata has `pricing_schema_version:2`,
`pricing_currency:"legacy_pricing_unit"`, `pricing_currency_basis`,
`pricing_unit_credits_per_unit`, and `pricing_credits_per_usd`. Expression
metadata has `billing_expr_currency_basis` and `billing_expr_usd_multiplier`.
Tiered and native voice records use their frozen calibration; `expr_b64`, hash,
tiers and raw debit stay unchanged. Token-rate display uses
`model_ratio_usd_per_million_multiplier=1000000/K` independently of legacy QPU.
Each structured tool surcharge also records its own legacy currency basis and
calibration because an additional tool charge can use a different calibration
from a frozen expression. Classic extra prices have
`tool_pricing_unit_credits_per_unit`; separate audio also has its own
`audio_input_pricing_unit_credits_per_unit`. Missing historical calibration does not authorize
assuming today's calibration. Actual total USD is always charged credits / K.
