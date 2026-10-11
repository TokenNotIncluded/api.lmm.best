# HumanGrant

HumanGrant lets an AI use access that a human explicitly approves. It does not
replace human login, give the AI the human's session, or grant an account level.
This implementation targets the Go service in `apps/api-go`, not the unfinished
Rust runtime. Protocol: **HumanGrant Protocol 0.1 Draft**.

Source: <https://github.com/LIghtJUNction/humangrant/tree/main/skills/humangrant-integrate>.
The protocol is source, not a published dependency or release tag. The public
signature fixture is from that project's `tests/fixtures/signature.json`.

## Current access

This first integration supports exactly one operation:
`account.profile.read` on an explicit, numeric LMM account ID. Its business
endpoint is `GET /v1/accounts/{id}/profile`. A request can only grant the signed-in
human's own profile. The response contains account ID, username and display name.

Model calls, billing, balance access, team funds, admin actions, creation of API
keys and login-level upgrades are **not** delegated. HumanGrant credentials are
not accepted by existing dashboard or relay authentication. Do not map them to a
human bearer token to add capabilities. Add an explicit action/resource policy
and tests for each later operation.

## Settings and startup

The login entry reads `GET /humangrant/config` without credentials. Its exact
label is **Continue with human beings**. The entry opens an independent agent
page; it is not an OAuth provider redirect or human dashboard login.

The switch is under **System settings → Authentication → HumanGrant** at
`/system-settings/auth/humangrant`. A fresh store defaults to enabled. An existing
saved value is retained. Only a current root user's real login cookie can read
or update its private settings endpoint:
`/api/user/auth/humangrant/settings`.

Turning the switch off denies new protocol requests and AI session access. Each
change increments a stored policy revision. Turning it back on cannot restore
old requests or grants. Humans can still list and revoke their grants when the
feature is off. This switch is stored in `human_grant_records`; it is not a new
entry in the old options map. Do not add a second conflicting options key.

Configure the canonical public HTTPS origin using `LMM_HUMANGRANT_ORIGIN`, or
keep the existing `system_setting.ServerAddress` valid. It must contain no path,
query, user information or fragment. An invalid address reports `ready:false`
and makes HumanGrant unavailable, without using a caller-supplied Host header.
The browser button is hidden until the server reports both enabled and ready.

For isolated development only, `LMM_HUMANGRANT_ALLOW_LOCAL_HTTP=true` permits an
explicit loopback HTTP origin. It does not permit public plain HTTP. Human login
still follows the host's own validated cookie policy.

Startup adds only `human_grant_gate` and `human_grant_records` through GORM. It
does not alter balances or existing login tables. Database migrations were not
run against a live or full target database while preparing this handoff. Review
and test the additive migration before deployment, including concurrent startup.

Use the repository's existing Go development/start commands. Native pages and
the browser client are embedded in the Go binary. Deploying only the frontend is
not enough. Update the packaged Nginx locations and log maps together with the
backend and frontend. All nodes must use the same database and canonical origin.

## Human consent

1. An AI creates an Ed25519 key and signs `POST /v1/requests`. Use client ID
   `lmm-agent`, target service `lmm-api`, the exact action above and the human's
   numeric account ID as the resource. `expected_human_id` can bind that account
   before review. Request TTL is ten minutes. Session TTL defaults to one hour;
   an explicit TTL must be between 60 seconds and one day.
2. The AI forwards `human_authorization_url` or the fixed `forwarding_prompt` to
   the human. The `/human/{link}` page shows the AI public key, service, client,
   requested access, expiry and requester-supplied purpose as untrusted text.
3. The human uses their existing LMM login. The verify action reads the real
   HttpOnly refresh cookie at `/api/user/auth/...`, re-checks the active login
   session and account, rotates the HumanGrant flow cookie, and displays the
   actual grantor. No user ID from the request substitutes for this check.
4. Permission choices start unchecked. The human separately approves selected
   access or rejects it. Approval expiry cannot exceed the requested session
   duration from the current consent time. Approval alone is not login success.
5. The original AI signs one claim request with its original key. Only a single
   successful claim returns the opaque credential. The token is stored as a
   hash. Claims and the transition to succeeded share one transaction. A lost
   claim response cannot recover the same credential; revoke before starting a
   new, explicit request.

The human endpoints are native host integration routes, not the reference
server's human-key authentication API. The shared `browserIdentity` verifier is
used in transaction-safe read-only mode. Expired login sessions fail closed
without invoking a second global-database write while the grant transaction is
holding locks. Existing OAuth callers keep their prior expiry behavior.

A small real signing client is included for use against a running, verified host:

```sh
node examples/humangrant-agent.mjs https://YOUR-LMM-ORIGIN ACCOUNT_ID
```

It prints the human-only authorization link, polls the actual signed Markdown
endpoint, claims once and reads the approved profile. It never reads human
cookies, prints the issued token or exports its generated private key. Closing
the process loses that ephemeral key. Keep its output private: the human review
link must not be put in public logs.

## Request proof and state

Agent routes are `POST /v1/requests`, `GET /v1/requests/{id}`,
`GET /v1/requests/{id}/status.md`, request `email`, `claim` and `cancel` POSTs,
`GET /v1/session`, and the scoped profile GET. The Markdown route returns
`text/markdown`, not JSON under a Markdown filename. Each field is a JSON literal
on its own line. Purpose text does not become forwarding instructions.

Use the strict HumanGrant Ed25519 signature profile. It binds the exact method,
configured origin plus path, raw body digest, content type, actor key and
Authorization value. Empty GET bodies are signed too. Query strings, duplicate
signature headers, noncanonical encodings, expired proofs, changed bodies and
repeated actor/nonces fail. A durable nonce reservation is shared by all nodes.

Request polling starts at five seconds and increases on early polls. A retry
must use a new signature nonce. The examples stop after bounded failures and do
not silently create another request or retry an uncertain claim. HTTP bodies
are limited to 16 KiB; duplicate/unknown JSON members and excessive nesting fail.

The store serializes HumanGrant state changes through a database gate. The Go
host adapter also locks current account/session records before a permitted read.
Each use checks grant expiry/revocation, original key binding, login session and
account state, current policy revision and the permission intersection. A
revoked human login session or changed security version invalidates its AI grant.
Do not replace these checks with a cached JWT snapshot.

## Mail

The default sender is a **private local database outbox only**. It makes no SMTP
connection and sends no real mail. A generic HTTP 202 response means accepted
for processing; it does not claim mailbox existence or delivery. External mail
transport is not implemented in this handoff.

Outbox records have kind `outbox`, state `local_spooled`, an operation ID,
destination, fixed message, creation time and request expiry. Operators may
inspect a specific operation through authorized database tooling in an isolated
test environment. These records contain private links and addresses. Do not log
or publicly export their payloads, and do not send an expired message.

Persistent default controls: recipient cooldown 60 seconds; recipient cap three
per hour and five per day; actor and direct-source cap ten per hour; service cap
100 per hour and 500 per day. Accepted-operation bookkeeping also has bounded
limits. Reusing an actor's idempotency key with identical input returns the same
operation. Different input with the same key is rejected. Suppressed and allowed
attempts have the same public acceptance form.

The template contains a separate refusal/block link. GET opens a page only.
Explicit, CSRF-checked POST records refusal or a permanent recipient block. It
never approves a grant. Refusal remains available when HumanGrant is off. Address
normalization, counters, cooldown and idempotency survive process replacement.

Source limits use the direct socket peer, not untrusted forwarded headers. A
shared reverse proxy therefore shares that source allowance. Capacity tuning and
trusted-proxy policy must be reviewed for the actual deployment; those limits
are intentionally conservative, not a claimed throughput result.

## Revoke and logs

Open `/human/grants`, sign in through the real host and load the current user's
list. Revoke uses an owner check, a page-bound CSRF value and the current login
cookie. The list is bounded to 200 records and explicitly reports truncation;
full historical browsing/pagination is not implemented. Historical request
success remains visible alongside current grant revocation or expiry.

Audit entries store action, actor, grantor, request/grant reference and time, not
raw tokens, email messages or complete authorization links. Application access
logs redact private paths. HumanGrant SQL statement logging is disabled. The
packaged edge log maps suppress sensitive requests/referrers, and private-page
proxy locations also suppress raw-URI error logs. The dedicated Go route recovery
does not dump request headers or links. Custom proxies, tracing and remote log
sinks need the same review; the local test does not prove a deployed log setup.

Records expire in bounded cleanup batches. Request/audit history is retained for
30 days; grant history for 30 days after its expiry. Nonces, flows and session
lookups have short expiries. Settings and recipient blocks do not auto-expire.

## Verification and remaining work

Standalone checks that can run without the target application's dependencies:

```sh
# From the repository or handoff overlay root:
GO111MODULE=off go test -race -count=1 ./apps/api-go/humangrant
GO111MODULE=off go vet ./apps/api-go/humangrant
node --test apps/api-go/humangrant/web/client.test.mjs
node --check apps/api-go/humangrant/web/ui.mjs
node --check examples/humangrant-agent.mjs
```

The Go tests use a serialized JSON unit-test store and a deliberately named
`test-host-login` identity seam. They test protocol state and authorization
logic, **not real host login authentication**. The Node tests reproduce the
upstream public signature bytes and test fresh client keys/nonces. The handoff's
`test_handoff.py` also runs real Nginx against a local test backend to check only
the new location/log snippets, body/header forwarding and SPA separation. It is
not a whole-production-proxy or real-authentication test.

Before merge, run at minimum in the complete target checkout:

```sh
(cd apps/api-go && go test ./humangrant ./service ./middleware ./router)
(cd apps/web && bun run typecheck && bun run build:check)
(cd apps/web && bun run i18n:sync && bun run copyright:check)
python3 -B scripts/check-docs-brand.py --docs-only
```

Also run the repository's relevant existing frontend, deployment and policy
checks. Review generated changes; do not accept unrelated file rewrites just to
make a command pass.

Still unexecuted: full target Go compilation/regressions, the real cookie/2FA
login-to-consent flow, GORM integration on the deployed database, multi-process
claim/revoke/account-disable races, concurrent migration startup, complete
frontend typecheck/build, browser accessibility/visual checks, complete packaged
Nginx validation with access-policy modules, slow-body limits through the real
Gin/server/proxy stack, and a restart/revocation run against the real host. The
native embedded pages currently use English UI text. The new settings strings
have an en/zh/zh-TW/fr/ja/ru/vi translation patch; the target i18n sync has not run.

Passing the standalone tests is not approval to merge or deploy this integration.
