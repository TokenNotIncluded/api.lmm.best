# Native TypeSafe Jev relay

The Go backend exposes Jev's synchronous decision API directly. It follows the
[official TypeSafe plugin 1.0.0](https://github.com/QuantumNous/new-api-plugins/blob/97a16e9a8d98b73ffb76e1c5a00b4d0f855f4d0a/plugins/tasks/typesafe/1.0.0/plugin.js)
contract without installing a JavaScript plugin host. Rust remains a preview
backend and does not implement this provider.

## Configure a channel

Create a **TypeSafe (Jev)** channel, enter the provider API key, select the
appropriate group, and enable any of these models:

- `jev-1.13.0`
- `jev-latest`
- `jev-preview`

The default upstream is `https://api.typesafe.ai`. A trailing `/v1` in the
configured address is accepted. Local channel type 62 identifies TypeSafe;
type 61 is reserved for historical OpenHuman records. OpenHuman creation and relay support have been removed; existing records are never renumbered or converted automatically.

For a second New API or LMM gateway, use **Compatible Relay** (type 60) and
that gateway's base URL and API token. Jev requests on this channel use the
upstream `/typesafe/v1/systemone` path. Native TypeSafe channels instead use
the provider's `/v1/systemone` path. Model mappings can expose a local alias
that resolves to one of the three supported Jev model IDs.

Use the channel test's **Judgment** endpoint. This sends a small structured
decision request; Jev does not support streaming or Chat Completions.

## Call the API

Both routes accept the same JSON body and normal LMM API token:

- `POST /v1/systemone`
- `POST /typesafe/v1/systemone`

For a TypeSafe SDK that appends `/v1/systemone`, set its base URL to either
`https://YOUR_GATEWAY` or `https://YOUR_GATEWAY/typesafe`.

```sh
curl https://YOUR_GATEWAY/v1/systemone \
  -H 'Authorization: Bearer YOUR_LMM_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "jev-1.13.0",
    "state": "The customer asks to reset a forgotten password.",
    "questions": {
      "account_support": {
        "type": "noul",
        "instructions": "Is this an account support request?"
      }
    }
  }'
```

`state` is required and accepts text, a JSON object or array, or `null`.
`questions` is a nonempty object of
named `noul`, `choice`, or `score` questions. Choice questions need 1–255
named criteria in an object; score questions need an array of 2–10 ordered
criteria. Instructions can be
text or structured JSON. Only `model`, `state`, and `questions` are forwarded.
`stream` may be omitted or `false`; `true` is rejected before provider work.
The response preserves the provider's `model`, `answers`, `usage`, and other
response fields. Each requested question must have an answer of the matching
type. See the [provider API reference](https://docs.typesafe.ai/api) for the
question and answer formats.

Both paths use the normal token authentication, model restrictions, request
admission, model rate limits, group/channel routing, model mappings, prompt
security checks, and billing owners. Existing OAuth grants remain limited to
their supported APIs; this change does not extend OAuth scopes to Jev.

## Billing and existing installations

The checked provider price on 2026-10-04 is **$0.042 per million input tokens**,
with no output-token charge. The model ratio defaults are `0.021` for each Jev
model (`1` means $2 per million tokens), and the default completion ratio is
`0`. These are configurable gateway defaults, not a production pricing update.
See [TypeSafe's current prices](https://docs.typesafe.ai/models).

Existing installations with saved ratio maps must add these entries through
their normal model-pricing settings. Loading this implementation does not
rewrite stored prices or remove model price locks. Local aliases also require
their own gateway pricing. Deliberately configured per-request prices and
tiered expressions retain their usual meaning.

Before upstream work, billing reserves the configured price for 65,536 input
tokens and zero output tokens. A successful response with an integer
`usage.input_tokens` in `0..65536` settles against that quantity. Explicit zero
is accepted and settles to zero under ordinary input-token pricing. Missing,
fractional, negative, or out-of-range input usage keeps the conservative
reservation and marks the usage as estimated in the consume log; it is not
treated as a free request. Upstream rejection or transport failure follows the
normal relay error/refund lifecycle.

Wallets, subscriptions, group ratios, price locks, token quotas, and usage logs
continue to use the existing billing system. Failed response delivery does not
replay completed upstream work. Test actual answers, usage, and account charges
with an authorized provider account before treating mock validation as live
provider acceptance.

## Upstream provenance

This is a native implementation of the TypeSafe task plugin's behavior,
reviewed at `QuantumNous/new-api-plugins` commit
`97a16e9a8d98b73ffb76e1c5a00b4d0f855f4d0a` (plugin version `1.0.0`). The upstream
plugin repository is Apache-2.0 licensed. No plugin engine, executable plugin
source, registry, or plugin-channel numeric configuration is imported.

Provider access and resale permissions are separate from software support.
The [TypeSafe standard MCA](https://typesafe.ai/legal/mca) distinguishes
customer applications from standalone resale; separate agreements can change
the applicable terms.
