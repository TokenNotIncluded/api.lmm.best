# Price-lock response consistency

The Go provider's single-model `PUT /api/option/` lock operation returns an
optional `pricing` object alongside `success`, `warnings` and `locked_models`.
Its values use the existing JSON-encoded option format. The eleven keys are
`ModelPriceLock`, `ModelPrice`, `ModelRatio`, `CompletionRatio`, `CacheRatio`,
`CreateCacheRatio`, `ImageRatio`, `AudioRatio`, `AudioCompletionRatio`,
`billing_setting.billing_mode` and `billing_setting.billing_expr`.

The snapshot is captured from the database while the existing pricing-policy
row is locked. It describes the pricing protected by that lock transaction,
not a subsequent read of a possibly stale process cache. It is returned only
when both the transaction and runtime publication succeed. Ordinary option
writes do not attach it. No unrelated option, credential or user record is
included. Existing permissions and locked-price write protection are unchanged.

The frontend still checks live price-lock support before each write. When the
write returns a snapshot, it validates the complete key set, each encoded map,
and the requested model's lock state. It then replaces only the cached pricing
fields and uses those accepted values to discard the locked model's drafts.
There is no additional GET between the committed response and that update.
Unrelated query-cache changes are retained.

Older Go/Rust providers that omit `pricing` retain the follow-up GET path;
that read must succeed, still advertise price-lock support and report the
requested lock state. A present but malformed snapshot cannot fall back to a
potentially stale GET. A write/refresh error invalidates cached options and
returns no successful draft result: a transport error is not proof that a
write was rolled back.

`GET /api/option/` also waits for the complete process-local option publication,
including periodic database reloads, before copying options and deriving
`CompletionRatioMeta`. Existing endpoint filtering still excludes sensitive
fields. This does not turn independently cached replicas into a global
linearizable read service; another administrator may commit a later change.

No schema migration, version change, release or deployment is required by this
source change. Existing installations need the updated Go binary before the
snapshot response is available. The checked legacy path remains necessary
when frontend and provider versions differ.
