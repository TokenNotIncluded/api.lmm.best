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

Configure an exact `ModelRatio` entry through the existing administrator settings.
Do not replace the entire existing map. This implementation deliberately ships no default
rate, since it does not implement automatic regional/long-context tariff updates.
The current ratio convention has 1 = $2 per million input tokens; 0.05 therefore
represents the base $0.10 price only, before the existing group and trust factors.
This numeric example is not a recommended production selling price.

An absent price rejects the request; ordinary model ratios are not a fallback.
Per-call and expression prices for this key are not implemented. Input usage is
settled from the provider's `input_tokens`. Output/cache details remain visible
but do not add separate charges. Explicit zero is not replaced by estimated
usage; absent or invalid input usage is rejected before delivery. A retry cannot
switch to another billing group using the first group's price.

**Before production:** reconcile actual provider usage and invoices, including
any nonzero cache details, regional requests and long inputs. Do not treat the
local parser tests as financial integration tests or proof of zero loss.

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
administrator price UI, automatic regional/long-context pricing and compatible
non-OpenAI channel selection were not changed. Their support is not claimed.
