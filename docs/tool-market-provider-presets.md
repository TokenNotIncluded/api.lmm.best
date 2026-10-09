# Provider presets, live prices and MCP OAuth

## Merchant setup

In the tool-market service editor choose **Monid** or **AgentKey**. Enter the
provider API key, choose a multiplier (default `1.2`, range `1` to `100`), then
read definitions. The preset locks the reviewed HTTPS endpoint and Bearer
mode. Review the selected tools and the maximum charge per paid call. Save,
validate and publish through the existing review process. A preset does not
bypass review, payment authorization or quota limits.

Only the existing encrypted credential store retains the key. Draft JSON and
query caches never contain it. Published versions retain their own endpoint,
credential binding, multiplier and ceiling. Changing them creates a new
reviewed version; it does not rewrite an existing user's consent.

| Provider | MCP endpoint | Free pricing lookup | USD price |
| --- | --- | --- | --- |
| Monid | `https://mcp.monid.ai/v1` | `monid_inspect` | `price.amount.value` plus `price.amount.currency`, or legacy numeric `amount` plus `currency` |
| AgentKey | `https://api.agentkey.app/v1/mcp` | `describe_tool` | `cost.usd_per_call` |

Monid exposes discovery, inspection and execution. AgentKey exposes tool
search, listing, description and execution. Account, key, balance and history
tools are excluded. The `agentkey_account` execution sentinel is also denied;
merely hiding its tool-list entry would not protect the merchant account.
The model and execution paths enforce the same allowlist even when a caller
submits a custom draft directly. An execution gateway without provider pricing
is rejected. Provider host aliases cannot bypass that rule.

## Price and settlement rules

For a supported per-call quote:

```text
charge = ceil(upstream USD price × reviewed multiplier × ledger credits per USD)
```

Amounts use exact decimal arithmetic. Tiny nonzero prices do not round to
free. Currency conversion uses the site's established ledger basis, not a
new credit scale and not an assumed AgentKey-credit exchange rate.

Execution fetches a fresh quote before any wallet hold or paid dispatch. The
operation must match the inspection result and its input schema. All existing
grant, client and account budgets still apply. The stored `price_quota` is a
**ceiling**, not a fixed charge, for a tool with `provider_pricing`. The quote
and multiplier are saved on the call for audit. Replaying the same request
returns the existing call instead of re-inspecting or executing again.

Calls are denied when the quote is missing, malformed, not in USD, above the
reviewed ceiling, or when the publisher's proceeds after the platform fee
would be below the quoted cost. The server never increases the multiplier
silently. This is a quoted-cost check, not a promise about a merchant's final
invoice, subscription discounts, failed-call charges or currency movements.

**Current limits:** Monid `PER_RESULT` prices are recognized but execution is
blocked because no provider-enforced maximum charge is available before
execution. A final cost receipt does not impose an advance spending bound.
Other variable-price forms and AgentKey descriptions without a USD price are
also blocked, not treated as free.

Monid asynchronous per-call runs retain their run ID and operation target
in the database. Recovery reads only that run, using the publication-bound
merchant credential. Workers claim a 15-second poll lease and never repeat
execution. A completed 2xx response uses the same result validation and
idempotent settlement as synchronous execution; a provider error releases
the hold. Restarting the service does not lose the run association.

The reservation has a fixed 15-minute deadline. If the result cannot be
verified by then, existing recovery releases the reservation and retains an
unknown execution state. A late result never retroactively debits the buyer.
This is not upstream cancellation: merchants may still incur an upstream
cost on an unresolvable run. Shared caller/workspace metadata and echoed input
are not returned to buyers. Protocol errors and malformed result bodies use a
generic error instead of forwarding unfiltered merchant data.
A completed provider HTTP error is not reported as a successful tool result.

Other MCP services retain the custom-service editor and existing pricing
modes. Adding a preset requires an explicit reviewed endpoint, a safe tool
allowlist, a price parser and tests. OAuth support at an upstream provider is
not the same as user-bound upstream OAuth integration in LMM: this release's
merchant connection uses an API key. It does not obtain Monid application
credentials, choose a user workspace or retain users' upstream refresh tokens.

## Default customer connection

When the site's existing native OAuth server is enabled, the connection page
recommends browser login before the manual-token fallback:

```sh
codex mcp add lmm --url https://api.lmm.best/mcp/market
# If the client does not open browser authorization automatically:
codex mcp login lmm
```

`/mcp` already belongs to another feature. The marketplace keeps `/mcp/market`
instead of replacing that route. The customer signs in to LMM, not to the
merchant's Monid or AgentKey account. The merchant key never reaches Codex.
Login alone does not authorize paid spending: choose the new OAuth client in
the market and grant exact tool/version access and budgets. Tool management
permission does not grant arbitrary tool execution or wallet access.

The server adds `POST /api/oauth2/register` and
`/.well-known/oauth-protected-resource/mcp/market`. Public native clients use
an authorization code with S256 PKCE, explicit browser consent and refresh
rotation. Their resource is exactly the issuer plus `/mcp/market`, and their
scopes are limited to `market:discover`, `market:invoke`, `market:manage`.
They cannot acquire model, account, group or relay permissions. Existing
trusted Pi/DSH clients retain their old resource and permission checks.

The registration endpoint accepts canonical HTTP `127.0.0.1` and `[::1]`
native callbacks. IPv4 and IPv6 templates remain separate; a registration
for one does not authorize the other. It does not accept arbitrary HTTPS web
IDE callbacks or DNS aliases such as `localhost`.
It never retrieves caller-supplied metadata URLs. Registration is rate and
size limited; deterministic IDs reuse registrations across callback ports.
A writer-locked, per-issuer cap limits storage to 4096 registrations. At
capacity, known clients still work and new registrations return an error.
External client display names are explicitly marked as unverified labels.

## Deployment and verification

This is a Go backend and Web change; it does not claim Rust parity. Keep the
existing native OAuth setup (`OAUTH_SERVER_ENABLED=true`, canonical
`OAUTH_SERVER_ISSUER`, explicit `OAUTH_SERVER_GROUPS`) and existing HTTPS/proxy
checks. The group allowlist remains required for the existing model clients;
new market-only clients do not receive its groups. Apply the normal startup
schema migration before the verify-only/runtime phase. OAuth-enabled schemas
include the registration tables and consent client-name snapshot.

Do not deploy the new frontend alone: the preset configuration, pricing
adapter, schema fields and OAuth endpoints require the matching backend.
An older server keeps manual-token connection available instead of showing a
nonfunctional browser-login command.

Tests use local mock MCP servers and local browser HTTP request fixtures.
They do not spend provider credits or establish that an actual Codex browser
session, a Monid key, or an AgentKey account has passed production testing.
Provider documentation used during implementation:

- https://docs.monid.ai/api/inspect.html
- https://docs.monid.ai/api/run.html
- https://docs.monid.ai/api/runs.html
- https://docs.agentkey.app/api-reference/describe-tool
- https://docs.agentkey.app/api-reference/execute-tool
- https://docs.agentkey.app/connect/manual
- https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization

## Release acceptance

Automated fixtures cover both providers' publication, quote and settlement
paths; Monid asynchronous success, provider errors, unrelated run rejection,
restart, replay and reservation expiry; IPv4 and IPv6 browser OAuth flows;
and provider switching without credential reuse. The browser review renders
the actual development UI at desktop and mobile widths in both themes, with
external requests blocked. Its data and keys are synthetic. None of these
checks claims to validate a production merchant account or a paid provider
request.

`PER_RESULT` remains an explicit unsupported billing mode, not a free fallback.
The documented run API does not provide a common enforced monetary ceiling.
A provider-specific `maxItems` parameter is not a safe general billing bound.
Do not enable this mode by inferring charge counts from arbitrary output arrays.
User-bound upstream OAuth requires separately registered provider applications;
it is not needed for the supported merchant-key/customer-LMM-OAuth flow.
