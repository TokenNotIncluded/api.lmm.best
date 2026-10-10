# Tool market extension — task 08

This package is for the fresh-install Rust-core / Go-extension branch. It does
not import a legacy model, open a core database, grant a team role, or edit a
public protobuf definition. It implements `Name()` / `Handler()` but is **not
registered or enabled by this change**. Registration belongs to final integration.

## Integration gates

Construct the module with `New(store, authority, Config{...})`.

- `Authority` is mandatory. Its integration adapter must call Rust for the live
  user credential, account membership and requested action on **every request**.
  Accepted action names are `toolmarket.read`, `toolmarket.manage`,
  `toolmarket.authorize`, and `toolmarket.execute`. The existing three read-only
  RPCs must not be treated as proof of these new permissions. No permissive
  default or local role store is provided.
- `Funds` is an optional adapter interface, not a protobuf proposal. No real
  adapter is included because the shared money RPC is not yet available.
  With `Funds == nil`, every known paid call fails with HTTP 503
  `funds_interface_unavailable` **before contacting the tool**. Unknown or expired
  prices fail with `price_unavailable`; neither becomes a free call.
- A future funds adapter must pass the actual user credential to Rust. Rust must
  independently validate the payer, permission, immutable quote/price source,
  reservation and idempotency key. Never enable paid calls with an adapter that
  merely returns success. Production money integration and reconciliation remain
  blocked on tasks 02/03/06 and the final integration stage.
- `CallbackURL` is a fixed HTTPS URL configured by the operator, not a request
  field. The public gateway must route it to this module's `GET /oauth/callback`
  and supply the current user's Rust-verifiable credential. It must also supply
  the host's service authentication on the internal hop. Do not put either
  credential in a URL or expose the host token to the browser. The callback
  validates a Secure, HttpOnly, SameSite=Lax `__Host-` browser binding cookie.

The host strips its service credential. This module requires separate, single
`X-LMM-User-Credential` and `X-LMM-Account-ID` headers. A submitted account ID is
only a requested target; Rust must authorize it. Native MCP `tools/call` checks
the **actual metatool permission again**, not just the outer MCP endpoint.

## Private storage

`InitStore(absolutePath, key)` explicitly creates a new encrypted store and
refuses an existing file. `OpenStore` only opens the installed version; neither
startup nor reads migrate, repair or clear data. Use a dedicated directory with
mode 0700 and a separate, operator-provided 32-byte encryption key. Keep the key
outside this store and outside the core's configuration/volumes.

The complete snapshot, including API keys, access/refresh tokens, OAuth state
and execution results, uses AES-256-GCM authenticated encryption. Transactions
use a private temporary file, file sync, atomic rename and directory sync. A
process-held file lock excludes a second writer. A directory-sync failure after
rename makes the store refuse later operations until reopen. Audit records and
execution intent are in the same transaction as the corresponding state changes.

This initial backend is **Linux, single-writer, bounded local storage**, not a
shared PostgreSQL database or a horizontally scalable backend. Snapshots are
limited to 63 MiB of plaintext; full storage fails closed. There is no automatic
pruning of audit records or idempotency keys. Operators must account for capacity
before enabling the module. Do not delete uncertain execution records to make
space. A future backend can replace this storage without accessing core tables.

## API and workflow

All paths below are relative to the host mount
`/extensions/v1/toolmarket`. JSON inputs reject unknown fields, duplicates,
missing required fields, null input fields and bodies over 1 MiB.

1. `POST /servers/register`: `endpoint`, `auth` (`none`, `api_key`, `oauth`).
   Registration creates an owner-only draft connection. API-key connections can
   select only `Authorization`, `X-Api-Key`, or `Api-Key`. OAuth connections use
   a pre-registered public `client_id`, optional pinned `issuer`, and explicit
   minimum `scopes`. Endpoint and authentication settings are immutable: create
   a new server rather than changing an installed release's destination.
2. `POST /credentials/key` or `POST /oauth/start`: authorize the returned
   `installation`. API keys are never published or inherited by buyers.
   OAuth start returns an authorization URL and sets the browser binding cookie.
3. `POST /servers/discover`: discover tools using that authorized installation.
   Validate the entire bounded discovery result before replacing the draft's
   tools. A failed page, cursor cycle or unsupported schema commits no partial
   tool list. Only the publisher can update its server's discovered definition.
4. `POST /releases/create`: `server`, new immutable `version`, and a `prices` map
   containing exactly one entry per tool. `POST /releases/publish` sets the
   release's `published` flag. Release content cannot be overwritten.
5. `POST /catalog` searches published release summaries. `POST
   /installations/create` installs a selected `release`; the buyer must authorize
   its own connection. `POST /installations/list` includes disabled installations
   without exposing credentials, schemas or loaded handles.
6. `POST /installations/enable` takes `installation` and explicit `enabled`.
   `POST /installations/upgrade` pins a new release of the same server. Upgrades
   clear local credentials and loaded handles. Disabling or credential changes
   invalidate handles and outstanding OAuth flows. An already dispatched
   external side effect cannot be recalled; completed execution records remain.
7. `POST /oauth/refresh` rotates a connection's tokens. An uncertain refresh is
   not automatically replayed; begin a new authorization. `POST
   /credentials/revoke` removes local credentials and pending states first, then
   attempts provider revocation of both refresh and access tokens. The response
   distinguishes local revocation from unsupported/failed upstream revocation.

Installing, publishing or unpublishing does not copy credentials. Credentials
are bound to an installation owned by **both user and account**. Two users in
the same team do not share tokens. Unpublishing blocks new public installations;
existing pinned installations remain usable until disabled by their owner.

## Quotes and paid execution

MCP discovery has no assumed external price. Prices in this implementation are
**publisher-supplied rules and evidence, not provider-verified price feeds**.
`price_rule` exposes that evidence with the tool details. No provider name or
model-generated estimate creates a price.

A rule uses `kind: unknown | free | fixed`, integer `amount` in millionths of
USD per invocation, `multiplier_bps` (10000 = 1x, range 10000–1000000), an
operator-entered HTTPS `source`, and Unix-second `valid_until`. Explicit free
rules require zero amount plus current evidence. Fixed quotes round up using
integer arithmetic and reject overflow-risk values. Variable/metered upstream
pricing has no automatic estimator in this release: leave it unknown.

`market.load` returns the immutable quote ID and a user/installation/version/
generation/schema-bound handle. Execution needs both values and an
`idempotency_key`. An accepted fixed-price provider invocation is priced per
call, including a provider's `isError` result; the rule is not token-based.

Execution order is durable intent → Rust reserve → recheck installation →
provider call → persist result → Rust commit. Idempotency keys are scoped to
user/account across installations. Reusing a key with different arguments,
installation, tool or quote fails. A completed retry returns its saved result.
A settlement retry only calls the idempotent Rust commit; it never calls the
provider again. `POST /executions/status` reads the caller's saved operation.

An uncertain reserve, disconnected provider call or crash leaves
`execution_in_doubt`. The module does **not** invent exactly-once guarantees for
external side effects, automatically retry calls, or release a reservation when
the provider might already have executed. `started` records require reconciliation
through the future core contract. Never retry an uncertain call with a new key.

## Lightweight metatools and protocol limits

`POST /mcp` exposes only four native MCP tools: `market.search`, `market.details`,
`market.load`, and `market.execute`. The same operations are available under
`POST /meta/search`, `/meta/details`, `/meta/load`, and `/meta/execute`.

Search defaults to 5 results, caps at 20, supports an offset and `has_more`, and
returns only installation ID, tool name, version and a shortened description.
It never returns all tool parameters. Details/load return **one** schema. All
metatools have distinct, closed parameter schemas. Provider descriptions and
results are marked as untrusted data, not instructions.

The pinned external protocol is MCP **2025-11-25**, with initialization, private
per-operation session handling, JSON and bounded SSE responses, and paginated
tool discovery. This is not a claim to support every MCP version/extension.
No local stdio processes, legacy HTTP+SSE fallback, sampling, elicitation,
long-lived server subscriptions, or automatic SSE resumption are implemented.
A failed or unsupported exchange stops without retrying a tool side effect.

The input-schema validator supports object, array, string, number, integer,
boolean and null types; properties, required, boolean additionalProperties,
items, enum, minimum/maximum, string lengths and array lengths. Numbers compare
exactly within bounded input/exponent limits. Unsupported constraints (including
`$ref`, patterns and schema composition) reject discovery, rather than being
ignored. Additional properties follow JSON Schema's default unless explicitly
forbidden. Metatool schemas explicitly forbid them.

OAuth discovery supports protected-resource metadata from a Bearer challenge or
well-known locations, and OAuth/OIDC authorization-server metadata fallbacks.
It validates the resource, pinned issuer, S256 support and fixed callback;
consumes state before exchange; supports refresh and revocation. This initial
client requires a pre-registered public client and same-origin authorization/
token/revocation endpoints. Dynamic client registration, confidential clients,
client-metadata hosting and automatic scope escalation are not implemented.

Production outbound connections require HTTPS on port 443, no URL credentials,
query secrets, redirects or environment proxies. All DNS answers are checked
against local/private/special networks, and the checked IP is dialed directly.
Metadata/token requests obey the same policy. Requests have deadlines, body/
stream/page/tool limits, at most 8 active requests, and per-user/account rate
limits. Audit errors never echo provider errors, tokens, codes or arguments.

Protocol references:
- https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
- https://modelcontextprotocol.io/specification/2025-11-25/basic/transports

## Validation and remaining acceptance

All tests use local mock MCP/OAuth servers. The unsafe local transport override
is unexported and exists only in tests; no runtime configuration disables the
production network policy. No real paid provider was called.

Local validation used Go 1.23.2 with the package's standard-library-only sources:

```sh
cd apps/lmm-extensions/internal/modules/toolmarket
GO111MODULE=off GOTOOLCHAIN=local go vet ./...
GO111MODULE=off GOTOOLCHAIN=local go test -race -count=5 -timeout=60s -cover ./...
GO111MODULE=off GOTOOLCHAIN=local go test -race -count=1 -json ./...
```

Result: 39 top-level tests and 18 subtests passed; zero failures. The repeated
race-enabled run passed five times. Statement coverage: 79.8%.

Coverage includes discovery, native MCP entrypoints, OAuth cookies/callbacks,
wrong/expired/reused state, provider denial, malicious callback parameters,
PKCE, refresh, revoke-vs-callback races, same-team token isolation, schema
constraints, provider errors, timeout non-retry, SSRF/redirect rejection,
concurrent duplicate payment, settlement retry, and encrypted-store reopen.

The repository declares Go 1.25.0. Its full module/dependency build, host
registration, real Rust authority/funds adapters, browser gateway routing,
full-process Rust isolation and live provider interoperability were **not**
validated by the isolated Go 1.23.2 run. At final integration, run the repository
checks and at least:

```sh
cd apps/lmm-extensions
go test -race ./internal/modules/toolmarket -count=1
go vet ./...
go test -race ./... -count=1
```

Keep the PR as Draft. Do not enable paid calls, merge or deploy from this task.
