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

The market home page pins the free `metamcp` entry, its actual parameter
schema and a connection button. The descriptor comes from the same Go
function used by MCP `tools/list`, not a second catalog definition.

When native OAuth is enabled, the connection panel recommends browser login
before the manual-token fallback. Copy a server URL into a compatible MCP
client and select OAuth, or use the displayed Codex command:

```sh
codex mcp add lmm --url 'https://api.lmm.best/mcp/market?mode=compact'
# If the client does not open browser authorization automatically:
codex mcp login lmm
```

Compact mode exposes only `metamcp`. Use `search`, `details`, `load`,
`authorize`, then `invoke`. Search returns summaries; inspect the selected
service for full schemas. For uncertain execution, use `call_status` rather
than repeating a paid request. Full mode is available without the query
string and also exposes legacy and individual tool entries. Both modes use
the same exact tool/version grants, request IDs, prices and budgets. Compact
mode does not need a `tools/list` refresh after loading.

`/mcp` belongs to another feature. The marketplace keeps `/mcp/market`
instead of replacing that route. The customer signs in to LMM, not to the
merchant's Monid or AgentKey account. The merchant key never reaches the
client. Login alone does not authorize spending: choose the OAuth client in
the market and grant exact tool/version access and finite budgets. Loading
a tool does not grant payment permission. Management is free; invocation
uses the selected tool's pricing.

The server provides `POST /api/oauth2/register` and
`/.well-known/oauth-protected-resource/mcp/market`. Public clients use an
authorization code with S256 PKCE and explicit browser consent. Their
resource is exactly the issuer plus `/mcp/market`, and scopes are limited to
`market:discover`, `market:invoke`, `market:manage`. They cannot acquire
model, account, group or relay permissions. Existing trusted Pi/DSH clients
retain their previous resource and permission checks.

Registration supports canonical HTTP `127.0.0.1` and `[::1]` native callbacks,
exact `http://localhost:port/path` callbacks, and exact public HTTPS web
callbacks with a path. Only IP-loopback listener ports may vary. IPv4, IPv6
and localhost are not aliases. HTTPS host, path and port must match the
registration exactly. Query strings, fragments, userinfo, encoded or
noncanonical paths and non-loopback HTTP callbacks are rejected. Explicit
`application_type` must agree with every callback; mixed native/web lists
are rejected. Existing trusted adapters keep their IP-only callback policy.

Public clients use `token_endpoint_auth_method: none`. Omitted `grant_types`
retain code + refresh support. Explicit `authorization_code` alone is also
supported: no refresh token is returned or stored, and refresh exchange is
rejected for that client. Code + refresh registrations retain token rotation,
replay rejection, revocation and existing IDs. Distinct grant contracts never
share an ID. This release does not advertise client-ID metadata documents,
fetch arbitrary client URLs or support confidential client secrets.

Discovery, registration, token/revocation and MCP endpoints support browser
CORS without credentialed cookies. Browser consent/login routes do not share
that policy. Unauthenticated MCP requests return a resource-metadata
challenge; invalid tokens return 401. A valid OAuth token without the needed
market permission returns 403 with `insufficient_scope` and the additional
scopes needed. A compatible client can request fresh, explicit consent.
The challenge never upgrades the old token, loads a tool, changes a budget
or executes a call. Clients requesting all three market scopes can still use
one login; an explicitly discovery-only authorization stays read-only until
another consent flow succeeds within its registration's allowed scopes.

Registration is rate and size limited. Duplicate/case-alias JSON keys, null
policy fields, trailing values and alternate credentials are rejected.
Unknown standard metadata is ignored and never fetched. Deterministic IDs
reuse IP-loopback registrations across listener ports. A writer-locked,
per-issuer cap limits storage to 4096 registrations. At capacity, known
clients still work and new registrations return an error. External client
display names are explicitly marked as unverified labels.

## Deployment and verification

This is a Go backend and Web change; it does not claim Rust parity. Keep the
existing native OAuth setup (`OAUTH_SERVER_ENABLED=true`, canonical
`OAUTH_SERVER_ISSUER`, explicit `OAUTH_SERVER_GROUPS`) and existing HTTPS/proxy
checks. The group allowlist remains required for the existing model clients;
new market-only clients do not receive its groups. Apply the normal startup
schema migration before the verify-only/runtime phase. OAuth-enabled schemas
include the registration tables, consent client-name snapshot and the
`refresh_disabled` column (default false for existing registrations).

Do not deploy the new frontend alone: the preset configuration, pricing
adapter, schema fields and OAuth endpoints require the matching backend.
An older server without the capability keeps the old page and manual-token
connection instead of showing a nonfunctional meta-tool or login command.

The manual `Tool market provider verification` workflow runs OAuth/provider
core tests on SQLite and PostgreSQL, marketplace integration tests with vet,
a Go build, Web typecheck, all marketplace tests and a production Web build.
Browser review renders the actual development UI at desktop/mobile widths
in both themes, with external requests blocked and synthetic fixture data.
It covers meta-tool visibility, provider switching, and compact/full OAuth
connection controls. HTTP tests cover exact callbacks, access-only clients,
CORS and scope-upgrade challenges. Protocol tests compare the shared
meta-tool descriptor with the actual compact `tools/list` response.

These tests do not spend provider credits or establish that a production
Codex browser session, Monid key, or AgentKey account has passed testing.
Provider and protocol documentation used during implementation:

- https://docs.monid.ai/api/inspect.html
- https://docs.monid.ai/api/run.html
- https://docs.monid.ai/api/runs.html
- https://docs.agentkey.app/api-reference/describe-tool
- https://docs.agentkey.app/api-reference/execute-tool
- https://docs.agentkey.app/connect/manual
- https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization

## Release acceptance

Automated fixtures cover both providers' publication, quote and settlement
paths; Monid asynchronous success, provider errors, unrelated run rejection,
restart, replay and reservation expiry; IPv4, IPv6, localhost and HTTPS
browser OAuth flows; and provider switching without credential reuse.
None of these checks claims to validate a production merchant account or a
paid provider request.

`PER_RESULT` remains an explicit unsupported billing mode, not a free fallback.
The documented run API does not provide a common enforced monetary ceiling.
A provider-specific `maxItems` parameter is not a safe general billing bound.
Do not enable this mode by inferring charge counts from arbitrary output arrays.
User-bound upstream OAuth requires separately registered provider applications;
it is not needed for the supported merchant-key/customer-LMM-OAuth flow.
