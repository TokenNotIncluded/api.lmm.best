# LMM native-client OAuth HTTP profile (stage 2, disabled by default)

Status: implementation in progress; not deployed or independently audited.

## Fixed client contract

- Trusted deployment issuer: `OAUTH_SERVER_ISSUER`, e.g. `https://api.lmm.best`. Never inferred from a request. Enabled only with `OAUTH_SERVER_ENABLED=true` and explicit JSON `OAUTH_SERVER_GROUPS` allowlist. No automatic/implicit group routing.
- Public native clients: `lmm-pi` (`LMM for Pi`) and `lmm-dsh` (`LMM for DSH`). Token families remain bound to the client that created them.
- Registered redirect template: `http://127.0.0.1/oauth/lmm/callback`; request uses the actual ephemeral port. Bind loopback before opening the browser. Verify exact returned `state` and `iss`.
- `resource`: `${issuer}/api/oauth2` (required for authorize, code exchange, refresh).
- Current initial `scope`: `catalog:read balance:read usage:read models:invoke mcp:bounties mcp:drawing` (space-separated). `usage:read` permits the native client's own daily activity aggregate; it never exposes request content or other accounts. The two `mcp:*` scopes authorize the built-in `/mcp` and `/mcp/drawing` servers in the same login. No secret, OIDC, DPoP or dynamic registration.
- Compatibility: the server also accepts the known historical application profile `catalog:read balance:read models:invoke`, with or without the complete built-in MCP pair, as well as the current application profile with or without the complete MCP pair. It rejects arbitrary partial/unknown combinations. A historical client grant is never widened with `usage:read` or MCP scopes it did not request; upgrading the client and reauthorizing is required to obtain new capabilities.
- Discovery: `GET /.well-known/oauth-authorization-server`; protected resource metadata: `GET /.well-known/oauth-protected-resource/api/oauth2`.
- `GET /api/oauth2/authorize`, `POST /api/oauth2/token`, `POST /api/oauth2/revoke`.
- `GET /api/oauth2/catalog`, `GET /api/oauth2/balance`: single `Authorization: Bearer lmm_at_…`; no alternate credentials or query parameters.
- `GET /api/oauth2/usage/activity`: requires `usage:read` and the same bearer header. It returns UTC daily aggregates for only the authorized account. Optional `from` and `to` Unix-second bounds default to the last 365 days, use an exclusive end, and are limited to 366 days. The response contains request/token/quota totals only; request bodies, model names and request IDs are never returned.
- Built-in MCP resources use the same OAuth access token: `mcp:bounties` authorizes `/mcp`, and `mcp:drawing` authorizes `/mcp/drawing`. Their unauthenticated challenge points to the protected-resource metadata above. Personal MCP tokens remain supported for backwards compatibility.
- Browser-only continuation/consent live under `/api/user/auth/oauth2/` to validate the existing HttpOnly refresh cookie **without widening its path or issuing dashboard credentials to Pi**. Login may happen in another tab; explicitly continue afterwards. A fresh random OAuth binding is installed after login. Final consent rechecks the current browser refresh identity; account/session/security-version changes invalidate it.

The consent adapter adds **only the explicitly displayed current group snapshot** to granted scopes as `group:<base64url-without-padding(UTF8(group))>`. These scopes are returned in the token response. Client must persist the returned scope instead of assuming it equals the current six-scope profile. Existing authorization never gains future application scopes, MCP scopes, or groups. Refresh may narrow scope; omitted scope preserves it. Removed groups are filtered from catalog and rejected at relay on every request. Registry allowlist changes require a restart; same-name groups are administrative security identities and must not be reused for a different privilege boundary.

Refresh rotation has no grace: the client journal serializes refreshes, stores only credential summaries and prevents replay after failed rotation. A crash that loses the replacement token requires login again. `/lmm-revoke` is wired to server revocation.

## Catalog v1

Response: `{schema_version:1, resource, updated_at:<Unix seconds>, groups:[...], models:[...]}`.

Group: `{id:<base64url UTF8 group>, name, scope, multiplier:<number|null>}`.

Model: `{id:"lmm:<group-id>:<base64url UTF8 upstream-model>", group_id, group, upstream_model, name, apis:["openai-completions"|"openai-responses"|"anthropic-messages"], pricing:{currency:"USD", unit:"million_tokens"|"request"|"expression"|"unknown", price_basis:"configured_base_rates"|"dynamic_estimate"|"tiered_expression"|"unknown", group_multiplier:<number|null>, trust_multiplier:<number|null>, input:<number|null>, output:<number|null>, cache_read:<number|null>, cache_write:<number|null>, request:<number|null>, final_cost_depends_on_usage:true, updated_at:<Unix seconds>}, native_cost:{input,output,cacheRead,cacheWrite}|null}`. Static token prices are nominal USD converted from platform credits and already include group/trust multipliers. Native admission also requires exact capability metadata from the installed official Pi provider directory for the same upstream model ID and advertised API; cross-model fallback is not used. When Pi requires numeric local cost fields for a `tiered_expression` entry, the client may use the exact official Pi catalog model's public cost only as a visibly labelled UI estimate; it is not LMM pricing and never affects settlement.

Unknown prices are JSON null, never fabricated zero. Numeric prices already include group and trust multipliers; the client must NOT multiply them again. Static token prices are nominal USD converted from platform credits. When dynamic pricing is enabled, token prices use the maximum current `GetRequestMultiplier(model, channel_id)` across the model's eligible routes, including each route's configured cost floor, with `price_basis:"dynamic_estimate"` and the catalog `updated_at`; any missing route cost keeps the estimate null. It is never a locked quote. Request-based prices remain unsupported for native model admission. Tiered expression models may use the exact official Pi model's public reference cost under the labelled exception above without calling or bypassing a protected pricing endpoint. Context window, reasoning support and token limit are not inferred from a model name; absent metadata is unknown. Final wallet settlement is authoritative and concurrent spending can change balance immediately.

## Relay

Use the existing `/v1/chat/completions`, `/v1/responses` or `/v1/messages` only when the catalog advertises the corresponding API. Set `Authorization: Bearer <OAuth access>` and `X-LMM-Group: <catalog group_id>`; JSON `model` is **upstream_model**, not the synthetic catalog id. Group header is mandatory, unique and canonical base64url. Do not send x-api-key, x-goog-api-key, query key, websocket credentials or a dashboard token. Unsupported endpoints and any failed OAuth validation are rejected without API-key fallback. No cross-group retries. Existing real TokenId precharge, settlement and refunds remain in use via an internal OAuth-managed billing record; clients never receive its key.

## Balance v1

`{schema_version:1, currency:"platform_credit", balance:<number|null>, quota:<integer>, quota_per_unit:<number>, updated_at:<Unix seconds>, authorization_limit:null}`. The client accepts legacy `currency:"USD"` responses but always displays platform credits. Own wallet only, not a promise of all subscription entitlements or a concurrent-spend reservation. Authorization limit null means no separate OAuth grant cap (normal account billing still applies).

## Operational gate

Do not describe this as published or production accepted until HTTP security, internal-key isolation, per-request authorization, real authorization/invocation/stream/cancellation/revocation/refresh and billing reconciliation tests, reverse-proxy log redaction, supply-chain review and client interoperability are verified. Proxy must not log OAuth query strings, POST bodies, response bodies or headers. Backend handlers use bounded contexts, independent per-IP budgets and no-store/no-referrer/CSP. Configure TLS termination to strip forwarding headers and forward only to the trusted loopback listener; do not trust arbitrary X-Forwarded-Proto as transport proof.
