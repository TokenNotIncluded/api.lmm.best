# Model price locks

In **Settings → Model Pricing**, use the button beside a model's price to
lock or unlock its saved pricing. Models start unlocked; lock changes are
saved immediately. Unlock before submitting new prices.

The Go production provider protects the model's per-call price, input/output
ratios, cache read/write ratios, image/audio ratios, and tiered billing
mode/expression. Individual saves, bulk updates, upstream imports, restoring
default ratios, and assistant changes all use the same write protection.
Deleting a locked price or introducing another billing mode is also ignored.

Locked entries keep their saved values. Other models in the same update are
saved normally. The response remains successful and includes `warnings` and
`locked_models` for ignored changes. Unchanged locked entries produce no
warning. Clients reload actual saved options instead of treating ignored
drafts as applied. Invalid request structure or database failures retain
normal error behavior.

## API and deployment

`ModelPriceLock` is a persisted JSON-encoded model-to-boolean option with
default `{}`. Missing or false entries are unlocked. Existing root-only
option permissions apply. `GET /api/option/` advertises
`capabilities.model_price_locks: true`; clients check this before toggling
locks so an old provider cannot silently accept an unsupported request.

Use an atomic single-model update through `PUT /api/option/`:

```json
{"key":"ModelPriceLock","model":"gpt-4o","value":true}
```

Use `false` to unlock. This preserves other models' locks even when another
administrator has changed them. Full-map option updates remain supported,
for example `{"key":"ModelPriceLock","value":"{\"gpt-4o\":true}"}`;
read and merge the current map before replacing it. A request combining
unlocking and repricing still preserves the previously locked price.

Locks protect exact configuration keys in the table. Lock a wildcard entry
itself to protect shared wildcard pricing. Existing user-group ratios and
the separate [dynamic pricing multiplier](./dynamic-pricing.md) continue to
apply. This change targets the Go production provider; Rust remains a
separate preview implementation. Upgrade Go before enabling the new web
controls. No data migration is required for existing installations.
