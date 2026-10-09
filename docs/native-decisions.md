# Native OpenAI Decisions API

Research date: 2026-10-09. This endpoint is available in the Go backend after the change is merged and deployed. A merge alone does not deploy production.

## Protocol

The gateway adds `POST /v1/decisions`, using the same path upstream. It does not
call Chat Completions, Responses, or TypeSafe System One to simulate decisions.
The current public beta supports `gpt-6-luna`.

The body contains `model`, `input`, an ordered `questions` array, and optionally
`safety_identifier`. The supported question types are `predicate`, `choice`, and
`score`. User input may contain text and images. Choice values retain string or
boolean types. Responses keep `answers`, probabilities, fractional scores,
per-question refusals, and the provider's `usage` fields.

Official references, inspected on the research date:

- https://developers.openai.com/api/docs/guides/decisions
- https://developers.openai.com/api/reference/resources/decisions/methods/create
- https://developers.openai.com/api/docs/pricing

## Integration

The route uses the existing token authentication, request limits, channel
selection, security text checks, reservation, settlement and refund owners.
The new relay format and mode are separate from chat. Only native OpenAI channel types
are admitted; both cached and database selectors filter incompatible channels before selecting priority. The existing transport supplies credentials;
no new unauthenticated proxy or separate wallet was added.

The request keeps native JSON fields. The gateway-only `group` field is omitted
from the upstream body. Channel model mapping is applied to a copy. Parameter
overrides are rejected because they could change input cost after reservation.
Global chat-conversion and request-filter options do not rewrite this body.
Responses are validated before delivery and returned as original bytes.
The response memory ceiling is 32 MiB; this is a gateway policy, not a vendor limit.

## Pricing boundary

Decisions has its own rate. The documented base rate is $0.10 per million input
tokens. There are no separate cache-read, cache-write, or output-token fees.
Regional processing and long-context input uplifts still apply. Do not reuse
ordinary chat pricing or claim the base rate covers every processing setting.

The internal price key is `<requested-model>@decisions`, for example
`gpt-6-luna@decisions`. This is a **price lookup key**, not a model ID to send to
OpenAI. API keys and channels continue to authorize the requested model ID.

Configure this key in the existing model-pricing administrator page with
`billing_mode = tiered_expr` and an independently maintained expression. For the
official rates verified on 2026-10-09, the USD expression is:

```text
(len > 272000 ? tier("long", p * 0.2) : tier("standard", p * 0.1)) * ((header("x-lmm-billing-upstream-host") == "eu.api.openai.com" || header("x-lmm-billing-upstream-host") == "us.api.openai.com") ? 1.1 : 1)
```

These numbers are configuration examples, not runtime defaults. The existing
currency conversion, group and trust factors still apply. Change or synchronize
the independent key when the official tariff changes. Synchronizing the chat
key never changes this price; generic price synchronization supports this key's
expression and retains its input-length and host conditions. Missing expression
configuration fails closed. Explicit input-only `ModelRatio` entries remain
supported for administrator-defined flat tariffs; per-call prices are rejected.

`p` and `len` use the provider's inclusive `usage.input_tokens`. Cache and output
fields are preserved in the response but add no separate charges. Prepayment
estimates input only, with no generated-output allowance. The expression and
currency basis are frozen per request; a configuration change affects new
requests. Consumption logs include the independent `billing_price_key`, expression
hash, matched tier, and regional multiplier trace.

`x-lmm-billing-upstream-host` is synthetic billing metadata, derived from the
selected channel's base URL. Client headers cannot forge it, and it is not sent
upstream as a routing header. A retry that selects a different host reevaluates
the frozen expression and raises the reservation before sending. Channels behind
regional proxies must explicitly include their configured proxy host in the
expression; the gateway cannot infer an upstream region hidden by a proxy.
Absent or invalid input usage is rejected before delivery. Explicit zero does
not become an estimate. A retry cannot reuse another billing group's price.

The configuration and ledger tests cover short/long boundary values, US/EU
processing, spoofed host headers, live configuration changes, wallet and partial
subscription funding, token balances, authoritative zero and idempotent settlement.
They are distinct from real provider execution and invoice reconciliation.

## Example after application, configuration and validation

This example has not been tested against production.

```bash
curl --fail-with-body https://api.lmm.best/v1/decisions \
  -H "Authorization: Bearer $LMM_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-6-luna",
    "input": "I was charged twice for one order.",
    "questions": [{
      "type": "choice",
      "name": "support_queue",
      "instructions": "Select the support queue for this report.",
      "choices": [
        {"value": "billing", "description": "Payments and charges."},
        {"value": "technical", "description": "Application faults."}
      ]
    }]
  }'
```

## Required full-repository checks

Run the normal repository checks with Go 1.25.1 or newer and the project's
dependencies. Specifically exercise authorization, channel selection/retry,
model mapping, per-key budgets, free groups, subscription/wallet settlement,
downstream disconnects, upstream errors, zero usage and missing usage. Check
parallel requests and confirm that there is no repeated settlement or refund.
Check mixed-provider groups: both database and cached channel selection filter out incompatible providers before priority selection.

The Rust preview backend, frontend endpoint catalog, generated OpenAPI catalog,
and compatible
non-OpenAI channel selection were not changed. Their support is not claimed.

## New core migration

The former Rust backend has been removed. Rust behavior described in older
implementation notes is not evidence for the replacement. The [new core](core-migration.md)
is not business-ready; the existing Go paths remain authoritative until parity
and migration checks pass.
