# Tool market extension

This package is for the fresh-install Rust-core / Go-extension branch. It does
not import a legacy model, open a core database, grant a team role, or edit a
public protobuf definition. It implements `Name()` / `Handler()` but is **not
registered or enabled by this change**. Registration belongs to final integration.

## Integration gates

Construct the module with `New(store, authority, Config{...})`.

- `Authority` is mandatory. Its integration adapter must call Rust for the live
  user credential, account membership and requested action on **every request**.
  Accepted action names are `toolmarket.read`, `toolmarket.manage`,
  `toolmarket.authorize`, and `toolmarket.execute`; the Web adapter additionally
  requires explicit `toolmarket.review` and `toolmarket.config` decisions. The existing three read-only
  RPCs must not be treated as proof of these new permissions. No permissive
  default or local role store is provided.
- Confirmation additionally requires `ConfirmationAuthority.VerifyToolConfirmation`
  with a server-built immutable `ConfirmationIntent`, verified human-session
  identity and short-lived proof of the exact intent. Missing proof fails
  closed. It is a local consumer interface, not an implemented Core RPC.
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

## Legacy Web compatibility first package

`web.go` and `web_types.go` preserve the service/version/tool DTOs and raw list
arrays consumed by `apps/web/src/features/tool-market/api.ts`, using main
`6efcb6fb0f6d` as the source contract. Optional host wiring is
`webcompat.RegisterToolMarket(mux, authenticator, module)`. This does not mount
the routes or provide a real Rust action-authority adapter by itself.

Construct this package with `Config{WebOnly: true}`. It requires no OAuth
callback and refuses a configured callback or funds adapter. The module's native
`Handler()` then returns 503 for every endpoint, so wiring the Web package cannot
accidentally enable native OAuth or MCP execution. The separate legacy Web
registration remains available after real Rust action authorization is supplied.

Implemented domain operations are public config, catalog search and released
detail; author draft create/read/edit, remote validation, submit, private free
activation, pause and deletion; administrator review; per-user favorites,
exact-version installations and the corresponding `mine` lists. A public read
with a supplied invalid session fails; it does not silently become anonymous.
Protected HTTP requests validate the Rust session and requested action before
reading the body. The domain rechecks action authority, exact owner and version;
network validation also rechecks authority before recording its result.

The Web format has a personal-user `owner_id`. This first package requires the
authorized account to be that user's personal account. It does not convert a
team membership into personal ownership. A future team Web contract needs an
explicit account reference and Rust team/action authorization.

Web records occupy an optional `web_compat` section in the same encrypted,
single-writer snapshot. Existing stores remain readable without changing their
native records or requiring a PostgreSQL connection. Editing creates a new
version; released versions and existing installations are never silently
upgraded. Private/shared visibility is reapplied to catalog, detail and favorites.
Remote schemas must match before approval or private activation; request input
cannot set a validation digest. The existing bounded HTTPS/DNS/MCP transport
and closed schema subset apply. Credentialed remote validation, provider and
metered pricing, AI review, OAuth, tokens, metatools, call history and business
record import are not implemented by this Web package.

The public Web config advertises `enabled=false`, `builtin_enabled=false` and
`execution_ready=false`. Only service deletion is advertised as a supported
optional capability. `quota_per_unit=500000` and `credits_per_usd="500000"`
describe the project's fixed credit unit, not a current foreign-exchange quote.
Web tool prices are safe integer ledger credits; the native `Price` API below
uses USD millionths and is a separate contract.

Web invoke, grant and budget operations always fail with HTTP 503
`TOOL_MARKET_FUNDS_UNAVAILABLE`, including nominally free Web executions. The
first package neither reserves funds nor contacts a tool for an invocation.
Config writes and unimplemented account resource kinds fail explicitly instead
of returning successful empty lists. Fresh synthetic tests are not a migrated
real catalog, and this package remains unassembled until Rust action authority
and host wiring have independent acceptance.

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
generation/schema-bound handle. Loading a schema is **not execution consent**.
Execution needs those values, a confirmed `grant`, and an `idempotency_key`. An accepted fixed-price provider invocation is priced per
call, including a provider's `isError` result; the rule is not token-based.

Execution order is durable intent plus atomic consent consumption → Rust
reserve → live authority/installation checks → MCP initialization → another
live authority and consent check → durable `dispatched` record → provider call
→ sanitize and persist result → Rust commit. Idempotency keys are scoped to
user/account across installations. Reusing a key with different arguments,
installation, tool or quote fails. A completed retry returns its saved result. A saved request hash is checked
before current installation/price validation. Thus expiry, disabling or a later
upgrade cannot reprice an accepted operation or make its completed result
unreadable. A settlement retry uses only the original stored charge and calls
the idempotent Rust commit; it never calls the provider again. Live authority
still applies: revoking the user's action permission blocks this endpoint. `POST /executions/status` reads the caller's saved operation.

An uncertain reserve, disconnected provider call or crash leaves
`execution_in_doubt`. The module does **not** invent exactly-once guarantees for
external side effects, automatically retry calls, or release a reservation when
the provider might already have executed. `started` may include an uncertain
reserve. A `dispatched` operation can read its original SSE result only when it
has a valid durable stream checkpoint, as described below. Without that evidence
it remains uncertain and needs the future reconciliation contract. Neither
state proves failure or permits a refund. Never retry an uncertain call with a
new key.

## Durable execution consent

Native user endpoints are separate from the four MCP metatools:

- `POST /grants/preview`: `installation`, `name`, `handle`, `quote`,
  `arguments`, `max_calls`, `max_amount`, `expires`. Returns the exact validated
  arguments, frozen grant and a one-use
  `confirmation` value. The confirmation is stored only as a hash. This request
  neither reserves funds nor invokes the tool.
- `POST /grants/confirm`: exactly `grant` and `confirmation`; no editable owner,
  price or limit fields. The matching owner must confirm within five minutes
  and before expiry, after the independent direct-human check succeeds.
  Reusing confirmation is rejected.
- `POST /grants/revoke`: `grant`; revocation is idempotent.
- `POST /grants/status`: `grant`; reads the caller's record, never the confirmation
  hash or another user's consent.

Preview/confirm/revoke require live `toolmarket.authorize`; status requires
`toolmarket.read`; execution separately requires `toolmarket.execute`. The
A01/A02 adapter must distinguish a direct user's approval from an agent's
execution permission. Ordinary `Authorize` success cannot activate a grant:
`confirmGrant` also calls `VerifyToolConfirmation`, rejecting missing,
invalid, expired, cancelled or panicking checks before it writes approval state.
The verifier binds the complete server-side intent hash, human session, owner,
audience and validity; it cannot trust model or client fields. No model metatool
can issue or confirm a grant. The
browser gateway still needs its own session and request-forgery protection;
this local interface is not proof of a completed HumanGrant integration.

Consent binds the user **and** account, installation, immutable release/tool,
connection generation, schema handle, exact argument hash, quote, amount and currency. It has a
1–1024 call limit, total amount limit, and expiry at most 24 hours and no later
than the quoted price. Amounts are **USD millionths**, not Core ledger credits.
A free tool still needs explicit zero-cost consent. Schema loading cannot be
used to approve purchases. The caller must preview and confirm the exact tool
arguments; changing a recipient, amount or other input then requires new
consent. JSON object-key order and whitespace do not change this binding. The
store retains the argument hash, not the full preview input. The UI must display
the returned canonical arguments as data rather than executing remote markup.
Per-tool semantics and the A02 distinction between human approval and an agent
remain separate gates; this does not grant authority to arbitrary commands.

The grant counters and execution intent change in one encrypted store
transaction. Simultaneous requests cannot overrun the local limits; duplicate
requests consume no additional consent. An uncertain operation keeps its
consumed amount/call. A new idempotency key cannot restore the same grant's
capacity. These counters are not a wallet, refund or platform sponsorship
budget, and do not replace Core's independent reservation limits.

Credential replacement, installation upgrade/disable, changed handle, expiry
or revocation prevents new dispatch. A revocation completed before the durable
`dispatched` transition wins the local race. Already dispatched external side
effects cannot be recalled. Later consent revocation does not erase receipts.

Existing encrypted files remain readable through an optional `execution_grants`
section. No startup migration or automatic grants are added. New calls without
consent fail. Grants without a valid `ApprovalID` and matching server-built `ApprovalHash`
cannot authorize new dispatches; they require a new preview and confirmation.
Old records are not automatically promoted. Already accepted operations retain
their original reconciliation paths. Legacy execution receipts without the new
request hash retain the older replay checks; they are not retroactively assigned
a grant or given new retry authority. Storage remains bounded single-writer
Linux storage.

MCP result JSON is checked for duplicate keys, depth, size and valid UTF-8, then
masked before persistence, model/client delivery and replay. The shared
assistant redactor masks credential fields plus the active connection's raw
access/refresh credentials and the caller credential. JSON structure, `isError`
and exact numeric literals are retained; ambiguous JSON fails with an uncertain
operation, not a free retry. Older results also pass this filter on duplicate
execution, status reads and settlement, even for disabled installations. A
successful settlement saves the filtered result. Ordinary reads do not rewrite
old files. This is best-effort filtering, not proof that arbitrary encodings or
unknown rotated credentials contain no secrets, and not a historical-data purge.

## Independent human confirmation

`Authority` must also implement the local `ConfirmationAuthority` port in
`confirmation.go` before a preview can become active. Ordinary action permission
and knowledge of the preview nonce are insufficient. Without this port,
`grants/confirm` returns 503 and leaves the grant inactive, even for free tools.

The adapter must obtain a Core-verified direct-human session decision for the
server-built `ConfirmationIntent`. The response binds its audience, exact intent
hash, user/account, `human_session` kind, audit reference and short validity.
It is not a client JSON field and is not a new implemented Core RPC. A01/A02
must provide the real adapter; test fixtures are explicitly synthetic.

Core verification runs outside the local store lock. Before activation, the
module rechecks live authority, cancellation, expiry, nonce, installation and the
entire immutable intent. Revocation or any changed argument, version or budget
rejects activation. The approval reference and intent hash persist with the
same grant; old active records without them cannot execute. No automatic
migration silently promotes those records.

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
Stateful SSE responses can resume by bounded GET requests. No local stdio
processes, legacy HTTP+SSE fallback, sampling, elicitation or long-lived server
subscriptions are implemented. A failed or unsupported exchange never retries
a tool side effect.

### Durable SSE result recovery

A server can send an empty-data primer with an event ID before the final JSON-RPC
reply. The client saves the server session, request ID, cursor, release and
installation generation in the encrypted operation record. These fields are
removed from public execution status. Credentials are not copied into this
checkpoint.

After a disconnect, the client uses GET with `Last-Event-ID` on the same checked
endpoint and session. It never sends a new `initialize` or `tools/call` POST for
an accepted operation. Each attempt permits at most two GET reconnections and
one MiB of stream input in total; the normal network deadline and rate limits
also apply. Each GET rechecks live action authority, installation generation,
release and credential expiry. A later request can recover the same checkpoint
for five minutes from its first save, including after an operating-system
process restart. A version change or missing/expired checkpoint stops recovery.

The supported server retry delay is 0–5000 milliseconds. A larger or overflowing
decimal delay clears the old cursor instead of polling too early. Empty event
IDs also clear it. Malformed responses, wrong request IDs, 404 sessions and
missing cursors remain uncertain; none authorizes a fresh tool call or refund.

Only primers and notifications advance a durable cursor. The terminal reply's
cursor is not saved before its filtered result and state are committed together.
A crash before that commit therefore cannot skip the unsaved result. Paid
results then settle the original charge; recovery never reserves again or
quotes a new price. This is not an exactly-once guarantee for arbitrary providers.

Failed initialization and completed sessions receive a bounded DELETE cleanup,
even if the caller context was cancelled. An uncertain session with a saved
cursor is kept for read recovery. The five-minute limit controls local recovery,
not the provider's session lifetime. Provider expiry and final host cleanup still
need integration; there is no new background worker or unconditional session
revocation claim.

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

A08 continuation validation used Linux / Go 1.23.2 in an isolated module with
exact copies of this whole package, `assistant/redaction`, and the existing mock
fixture. The three-run race check passed 82 top-level tests and 82 subtests per
run. Three shared-redactor tests also passed in each run. The optional external
Mocklab adapter was skipped because it was not configured. The subprocess helper
is skipped only in the parent run; the real child-process tests executed,
including killing a process after saving an SSE cursor and reopening its store.
These are synthetic MCP/authority/funds tests, not the project's pinned build,
real Core funds, PostgreSQL, complete assistant package or browser acceptance.
The root dependencies and pinned tool versions were not changed for this check.

The historical native-only validation below used Go 1.23.2 with the package's
standard-library-only sources; it does not validate the newer Web package:

```sh
cd services/extensions/internal/modules/toolmarket
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

That historical run did not validate the full module/dependency build, host
registration, real Rust authority/funds adapters, browser gateway routing,
full-process Rust isolation and live provider interoperability were **not**
validated by the isolated Go 1.23.2 run. The current branch pins its tools in
`mise.toml`. Web compatibility has additional business and HTTP fixture tests,
including encrypted-file reopen, ownership, immutable versions, live visibility,
schema drift, concurrent drafts, authorization before body reads, integer
precision and disabled paid operations. Run from `services/extensions`:

```sh
mise exec -- go test -mod=readonly -race ./internal/modules/toolmarket ./internal/webcompat -count=1
mise exec -- go vet ./internal/modules/toolmarket ./internal/webcompat
```

These tests use synthetic action authority and local mock MCP transport. They
do not prove the missing Rust action RPC, real snapshot import, browser journey,
host admission wiring, live providers or funds integration.

Keep the PR as Draft. Do not enable paid calls, merge or deploy from this task.
