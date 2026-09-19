/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */

export const LMM_ISSUER = 'https://api.lmm.best'
export const LMM_SOURCE = 'https://github.com/TokenNotIncluded/api.lmm.best'

// Copyable technical artifacts deliberately use English; the surrounding UI is localized.
export const PRICING_PROMPT = `Integrate LMM model pricing into this project. First inspect its framework, HTTP client, authentication and tests; reuse the existing stack and check dependencies before adding anything. Explain your changes in the language of our conversation.

Provider: https://api.lmm.best
1. Public catalog: GET /api/pricing. Access follows the site's visibility settings; handle 401/403 explicitly. The response is {success, data: [...], group_ratio, usable_group, supported_endpoint, pricing_version}. Validate success and data before rendering.
2. Preserve model_name, enable_groups, quota_type, billing_mode, model_price, model_ratio, completion_ratio and optional cache/audio/image ratios. Ratios are not already USD prices. Use the source pricing contract to convert standard rates; do not invent a flat price for tiered_expr or evaluate billing_expr with eval.
3. For an already registered OAuth client, GET /api/oauth2/catalog with Authorization: Bearer ACCESS_TOKEN and an approved grant including catalog:read. This endpoint returns the catalog directly: {schema_version, resource, updated_at, groups, models}; it does not use the public endpoint's success/data envelope. Do not send browser cookies or API-key headers with OAuth bearer credentials.
4. OAuth model pricing declares currency, unit and price_basis. Numeric prices already incorporate the supplied group/trust factors: do not multiply them twice. Treat null/unknown prices as unavailable, not zero. Keep per-request and per-million-token rates distinct, and show when final cost depends on usage.
5. Fetch through this project's backend when cross-origin browser access is not supported. Use bounded timeouts, cancellation, a short cache and Retry-After-aware bounded retries for 429/temporary failures. Scope personalized caches to the authorized account and grant. Show a recoverable error, never an endless loading/retry screen or fabricated prices.
6. Add mock HTTP tests for success, empty catalog, null versus zero prices, per-request versus token pricing, expression prices, 401/403, 404, 429, timeout and invalid JSON. Do not make paid model requests to test the catalog.

Source contracts:
https://github.com/TokenNotIncluded/api.lmm.best/blob/main/apps/api-go/controller/pricing.go
https://github.com/TokenNotIncluded/api.lmm.best/blob/main/apps/api-go/service/oauth_catalog.go
Deliver the implementation, configuration example without credentials, and test results.`

export const OAUTH_PROMPT = `Integrate LMM OAuth into this project where the provider contract supports it. First inspect the application type and existing auth libraries; check installed dependencies and reuse the current stack. Explain your work in the language of our conversation.

Trusted issuer: https://api.lmm.best
Discovery: GET /.well-known/oauth-authorization-server
Resource metadata: GET /.well-known/oauth-protected-resource/api/oauth2
Authorize: GET /api/oauth2/authorize
Token: POST /api/oauth2/token
Revoke: POST /api/oauth2/revoke
Resource: https://api.lmm.best/api/oauth2

Confirm prerequisites before writing an unusable login flow. The current public registry contains the native clients lmm-pi, lmm-dsh and lmm. A new project needs its own server-approved client ID, redirect and scope profile; do not borrow those IDs or invent a client secret. There is no public dynamic registration, OpenID Connect discovery, userinfo endpoint or ID token. If the goal is third-party website identity/SSO, explain this missing provider capability instead of pretending a resource access token identifies a website user.

For a registered native client:
- Use Authorization Code with PKCE S256 and a cryptographically random state/verifier. Bind the loopback callback listener before opening the system browser. The registered native callback template is http://127.0.0.1/oauth/lmm/callback with the actual ephemeral port; verify the exact registered contract for this client.
- Send response_type=code, client_id, redirect_uri, resource, the approved scope profile, state, code_challenge and code_challenge_method=S256. Validate both returned state and iss against the original state and trusted issuer.
- Exchange a single-use code with form-encoded grant_type=authorization_code, code, client_id, redirect_uri, resource and code_verifier. Treat tokens as opaque. Do not log authorization URLs, codes, state or credentials.
- Store credentials using this application's secure native storage, persist the returned scope and expires_in, and serialize refresh across processes. Refresh rotation has no grace period: do not replay an old refresh token after an ambiguous network failure; request reauthorization. Revoke on disconnect and erase local credentials.
- Use granted catalog:read/balance:read capabilities only where the approved client profile permits them. Do not substitute dashboard cookies or raw API keys for OAuth tokens. The built-in lmm read-only profile is catalog:read balance:read; model invocation and other clients require their own approved profile.
- Test callback cancellation, mismatched state/issuer, expired/replayed code, denied scopes, timeout, refresh concurrency, token revocation and unavailable discovery with mocks. Do not request a user's password or API key in chat.

Source contract:
https://github.com/TokenNotIncluded/api.lmm.best/blob/main/apps/api-go/service/oauth_server.go
https://github.com/TokenNotIncluded/api.lmm.best/blob/main/apps/api-go/router/oauth_server.go
Deliver code only for supported, registered flows; clearly list remaining provider prerequisites and test results.`

export const PRICING_EXAMPLE = `export async function getLmmPricing() {
  const response = await fetch(
    'https://api.lmm.best/api/pricing',
    { signal: AbortSignal.timeout(10_000) }
  );
  if (!response.ok) throw new Error('Pricing: HTTP ' + response.status);
  const catalog = await response.json();
  if (!catalog.success || !Array.isArray(catalog.data)) {
    throw new Error('Invalid pricing response');
  }
  return catalog;
}`
