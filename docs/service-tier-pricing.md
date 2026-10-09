# Fast and Ultrafast pricing

This is an LMM-specific opt-in tariff for official OpenAI text requests, not a
universal 2x/6x multiplier on existing model prices. Upstream Fast rates vary by
model and token dimension. `/fast` in a client is not a new relay URL: clients
still use `/v1/responses` or `/v1/chat/completions` with `service_tier`.

## Setup and one-click refresh

On the updated Go backend, open **System Settings → Models → Fast and Ultrafast
pricing** (`/system-settings/models/service-tier-pricing`). Root access is needed.

1. Click **Sync all official service-tier prices**. One catalog is fetched from
   `https://developers.openai.com/api/docs/pricing.md`, without an API key. The
   parser imports Standard fallback, Fast, Ultrafast, cache read/write and
   short/long-context prices. Test fixtures are not runtime fallback prices.
2. Select the existing groups allowed for each tier. Empty lists allow nobody.
   Wildcards and `auto` grants are rejected. Select the final paid routing group.
   New models covered by a refreshed catalog need no per-model expression rule.
3. Set the two sales multipliers, enable and save. Both default to **1.20**:
   official accelerated cost plus 20%, not standard model price times 1.20.
   Multipliers below 1 are rejected. Acceleration defaults off and activation
   requires a fresh catalog. Enabling alone grants no group permission.

Refresh changes only the provider cost catalog. It never changes enabled state,
group grants, sales multipliers, ordinary model prices or existing price locks.
**Refresh is manual, one click, not a scheduled background sync.** Prices expire
after 24 hours. Failed/malformed refreshes retain the old snapshot; expired
snapshots cannot authorize new premium requests.

## Pricing and funding

`official tier token cost × sales multiplier × max(1, effective group ratio)`

The effective group ratio includes existing group/trust calculations. Discounts
below 1 cannot reduce this new tariff below its cost-plus price. Ordinary model
prices and locks still govern ordinary requests. Old per-model tier expressions
are not applied again to the new tariff.

Each request snapshots prices and the USD credit basis, fully reserves wallet
funds without the trusted-user bypass, then charges measured token dimensions
once. Premium requests reject `subscription_only`; other preferences use the
wallet. Output limits are enforced before transmission. When omitted, the
existing 8192-token estimate becomes an actual output cap for premium requests.

The final HTTP/WS boundary validates after mapping, conversion, passthrough,
parameter/header overrides and compatibility retries. A changed model, group,
destination or output budget cannot reuse a cheaper quote. `fast` and `priority`
select Fast. The legacy `OpenAI-Service-Tier: ultrafast` header is treated as a
paid request; conflicting/repeated headers are rejected. Ordinary official calls
explicitly send `service_tier: default`, preventing paid project-default inheritance.

For HTTP, SSE and Responses WebSocket requests, actual provider response metadata
selects the tariff, including downgrade to Standard. WebSocket connections have
no shared paid-tier header: each `response.create` needs its own reservation.
Logs include requested/actual tier, catalog hash/time, group, multiplier and the
frozen credit basis. Decimal costs use rational arithmetic and round upward only
once at the final credit conversion.

## Scope and remaining risks

This version enables **global api.openai.com text Responses and Chat Completions**.
Regional/FedRAMP endpoints, Azure, third-party channels, unknown model aliases,
hosted tools, audio, background requests and unknown long-context prices are
blocked for managed acceleration. Client-executed function/custom tools remain
available. Anthropic Fast mode and other providers need separate support.

The **Rust preview backend rejects accelerated OpenAI requests** until equivalent
pricing/funding/settlement exists. It does not claim pricing parity. The settings
API/UI remains Go-owned.

This prevents unpriced acceleration, not all possible business losses. Public
prices may change before refresh; taxes, payment fees, FX and private provider
terms are not inferred. Stateful/remote inputs can exceed an initial estimate.
Missing/invalid provider usage still needs reconciliation: a completed premium
request without usage retains its own reservation, never an ordinary-model
historical average. Existing explicit-zero and interrupted-stream rules remain.
`reconciliation_required` marks estimated/unpriced results. Compare sample logs
with official usage/billing before granting customer groups. Tests make no paid
provider requests and do not deploy or enable this feature.

## API and verification

Root-authorized routes:

- `GET /api/ratio_sync/service_tiers`: policy, catalog, freshness and groups.
- `PUT /api/ratio_sync/service_tiers`: validate/save the whole policy once.
- `POST /api/ratio_sync/service_tiers/sync`: atomically replace a validated catalog.

Storage keys: `ServiceTierPricingPolicy`, `ServiceTierPricingCatalog`.

Run `go test -race ./pkg/servicetier ./relay/helper ./relay/common` from
`apps/api-go`, affected relay/service/controller suites, web type checking and
`service-tier-api.test.ts`. Rust guard tests are under
`routes::relay_openai::service_tier`. No ordinary pricing or live settings are
changed by these tests.

## New core migration

The former Rust backend has been removed. Rust behavior described in older
implementation notes is not evidence for the replacement. The [new core](core-migration.md)
is not business-ready; the existing Go paths remain authoritative until parity
and migration checks pass.
