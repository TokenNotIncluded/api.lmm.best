# Read-only quota and price queries

Use the site's API origin (the same origin used by your client), over HTTPS.
These endpoints do not generate model output, recharge an account, change keys,
or deduct quota. Never embed a real key in a public page, URL, screenshot or log.

## Quota: `GET /v1/usage`

```sh
curl https://api.lmm.best/v1/usage \
  -H 'Authorization: Bearer API_KEY'
```

The successful response is a JSON object, without a `data` wrapper:

```json
{
  "valid": true,
  "currency": "USD",
  "remaining": 88.5,
  "used_today": 2.3,
  "used_total": 120.8,
  "total_quota": 209.3,
  "unlimited": false,
  "updated_at": 1789171200,
  "scope": "token",
  "day_timezone": "UTC",
  "used_today_source": "retained_consumption_logs",
  "consistency": "persisted_snapshot"
}
```

- **Scope is one API key**, not the shared account wallet or subscription balance.
  A key may have more allowance than the account can currently spend.
- Amounts are USD, converted from internal quota by the configured quota-per-USD
  factor. They are not internal quota integers. No display-currency conversion is applied.
- `remaining` is the persisted key allowance, `used_total` is its cumulative usage,
  and `total_quota = remaining + used_total`; changing a key's allowance changes
  this total. It is not the account's lifetime recharge amount.
- With `unlimited: true`, `remaining` and `total_quota` are JSON `null`.
  Unlimited means the key has no own cap; the account is still subject to billing.
- `used_today` sums this key's retained consumption logs since 00:00 UTC.
  It is `null` when consumption logging is disabled. Deleted logs and logging
  interruptions can make it incomplete. It is not an accounting reconciliation record.
- `updated_at` is the snapshot query time in Unix seconds, not the last purchase
  or use time. Queued accounting/cache updates and asynchronous logs may lag;
  the two data stores are not read in one cross-database transaction.
- Exhausted keys may query their remaining quota. Invalid, deleted, disabled and
  expired keys cannot. User bans and key IP restrictions are respected. Only
  `Authorization: Bearer ...` is accepted; no query-string keys or channel suffixes.
- No response contains the key, account email, or other keys' consumption.
- Limit: 30 queries per minute per account, across its keys and these query APIs.
  Poll once per minute; on 429 respect `Retry-After` and use backoff.

| Status | Meaning |
| --- | --- |
| 200 | Valid key and successful read |
| 401 | Missing, invalid, disabled, expired or deleted key/account |
| 403 | Client IP is not allowed by the key |
| 429 | Query rate exceeded (`Retry-After` seconds) |
| 500 | Database or quota configuration unavailable; do not interpret as zero balance |

Authentication/read failures use `{"valid":false,"error":"invalid_api_key"}`
or another generic error code. The shared rate limiter may use its standard
error envelope; rely on HTTP status and `Retry-After`.

## Prices: `GET /v1/pricing?model=gpt-4o`

```sh
curl 'https://api.lmm.best/v1/pricing?model=gpt-4o' \
  -H 'Authorization: Bearer API_KEY'
```

Omit `model` to list the key's configured models. Normal relay authentication
applies (including key state, allowance, IP and developer access restrictions).
This endpoint does not reserve quota or call an upstream model.

The response has `success`, `data` (one entry per model and permitted routing
group), `updated_at`, `scope: "token"`, `price_basis: "configured_base_rates"`
and `final_cost_depends_on_usage: true`. Automatic-group keys may see several
entries for the same model; these are alternatives, not additive charges.
Token model restrictions and usable groups limit the results. Unknown and
inaccessible models both return 404. Oversized model queries return 400;
temporarily unavailable pricing returns 503.

Each entry includes `model`, `group`, `currency: "USD"`, `billing_mode`, `unit`,
effective `group_ratio` (including the user's special group ratio and trust
discount), and `trust_discount_ratio`.

- `per_token` / `million_tokens`: `input_price` and `output_price` are USD per
  million text tokens. The configured `model_ratio`, `completion_ratio`,
  `cache_ratio` and `create_cache_ratio` retain their billing meanings.
- `per_request` / `request`: `request_price` is the configured USD base price
  per request after the group/trust factor.
- `tiered_expr` / `expression`: `billing_expression` is the live billing rule;
  no false fixed price is returned for a usage-dependent expression.
- `dynamic_pricing: true` means channel selection/time-dependent adjustments may
  change the final rate. Returned numbers are configured base rates, not a locked
  quote. Images, audio, tools, resolution/quality and cache-duration policies can
  add or alter charges. Final cost is determined by the actual request and usage.

Price configuration is read from the same live settings used for billing.
The available-model catalog is cached; newly enabled models can appear after
the normal catalog refresh. Responses are `Cache-Control: no-store`.
