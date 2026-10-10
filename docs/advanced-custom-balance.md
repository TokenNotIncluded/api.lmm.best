# Advanced Custom balance queries

The Go backend can query an Advanced Custom channel's balance with a declarative
balance route. This changes balance lookup only; inference remains channel type
58 with its existing routes and converters. The Rust backend currently supports
DeepSeek balance refresh only. It rejects Advanced Custom balance queries before
provider fetch or balance update. Use the Go backend for this feature.

Existing balance routes keep their defaults: `GET`, existing route authentication,
no request body, and automatic OpenAI extraction. A JSON object with
`"object":"credit_summary"` and a finite, non-negative numeric `total_available`
updates the stored balance. Other valid JSON is returned as formatted raw JSON
without updating the balance, including legacy invalid credit summaries. This
raw response is the existing administrator-facing API contract; it is not copied
into error messages or logs.

For POST or a different response shape, add `balance` to the balance route in the
Advanced Custom editor or its JSON view:

```json
{
  "advanced_routes": [
    {
      "incoming_path": "/v1/dashboard/billing/credit_grants",
      "upstream_path": "https://account.example.com/api/balance",
      "converter": "none",
      "balance": {
        "method": "POST",
        "body_template": "{\"api_key\":\"{api_key}\",\"currency\":\"USD\"}",
        "json_pointer": "/data/available_cents",
        "scale": 0.01
      }
    }
  ]
}
```

Only one balance route is allowed. It cannot use client model rules, a converter,
or `{model}` in its upstream path. `balance` is rejected on inference and model
discovery routes. Methods are limited to GET and POST; omission defaults to GET.
POST can have an empty body by omitting `body_template`. A present body template
must be valid UTF-8 JSON, at most 16 KiB, and is allowed only with POST.
`{api_key}` is substituted as escaped JSON string contents. Place it inside a
JSON string, as in the example. The rendered body must also be valid JSON and at
most 16 KiB. Templates cannot run code, scripts, expressions or extra requests.

`json_pointer` uses the [RFC 6901 JSON string representation](https://www.rfc-editor.org/rfc/rfc6901).
An empty or omitted pointer keeps automatic OpenAI extraction and raw JSON
responses. A configured pointer must start with `/`, fit within 1024 UTF-8 bytes,
and have at most 32 components. Escape `/` as `~1` and `~` as `~0`; for example
`/a~1b/m~0n/0` selects array element zero under keys `a/b` and `m~n`. Array indexes
must be unsigned decimal integers without leading zeroes. Referenced duplicate
object members are ambiguous and fail extraction. Missing paths, strings, null,
booleans, objects and arrays are rejected. `scale` requires a pointer and must be
finite and positive; omission means 1. Both the selected JSON number and the
scaled result must be finite and non-negative. Choose a scale that produces the
USD-denominated balance displayed by the channel UI.

Balance lookup has a five-second deadline including response-body reading and
accepts HTTP 200 with at most 256 KiB of JSON. It never follows redirects. The
credential target is the endpoint explicitly configured by the administrator:
a relative path uses the channel Base URL, while an absolute HTTP(S) endpoint
overrides it under the existing Advanced Custom route contract. URL userinfo and
fragments are rejected. A Host header override must match the configured
endpoint's authority. This prevents redirects or Host overrides from forwarding
credentials to another target. Existing administrator-managed private-network
endpoints, proxy settings and transport settings remain available.

Invalid configuration, transport failures, non-200 responses, oversized bodies
and configured extraction failures return bounded errors without the channel
key, template or upstream body. Failed lookups do not overwrite either the old
balance or its update timestamp. Successful extraction publishes both together.

Go controller, adaptor and DTO regressions cover these boundaries. The Rust real
integration gate includes
`channel_balance_store::tests::advanced_custom_balance_is_unsupported_without_fetch_or_update`,
which reads a persisted Advanced Custom row, asserts zero requests against a
local provider stub, and verifies the previous balance and timestamp stay intact.
