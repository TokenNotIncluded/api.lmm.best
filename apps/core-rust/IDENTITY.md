# Native identity and teams

Task 01 for the Rust core / Go extension split (parent PR #675).
These routes talk to the core PostgreSQL database only. They do not call a Go
service. Changes here do not activate model forwarding or implement payment,
refund or budget policy.

## Start a fresh installation

Use a NEW, empty PostgreSQL database and the existing protected database URL
configuration (`LMM_CORE_DATABASE_URL` or `LMM_CORE_DATABASE_URL_FILE`). Never point
the initializer or tests at production. There is no migration path from the old
Go database or from an earlier draft schema. Startup checks the exact fresh-schema
fingerprint and refuses a different contract.

```sh
cd apps/core-rust
cargo run --locked --bin lmm-core-admin -- init-db
# Public registration is disabled unless explicitly enabled.
export LMM_CORE_REGISTRATION_ENABLED=true
cargo run --locked --bin lmm-core
```

Terminate public TLS at a trusted proxy. Do not log Authorization headers,
password bodies, callback query strings, session responses or invitation tokens.
The HTTP service uses the socket peer for its shared-database limit of 60 auth
requests per minute. It deliberately ignores caller-supplied forwarding headers.
A shared reverse proxy therefore shares that limit; configure an additional edge
limit and an appropriate deployment topology before opening registration widely.
Passwords also have a 10-attempt-per-minute login-name limit. Hashing uses at most
two concurrent blocking workers per store, with no unbounded waiting queue.

## Register, sign in, and manage sessions

`POST /core/v1/auth/register` and `POST /core/v1/auth/login` accept:

```json
{"login_name":"alice","password":"use a unique long passphrase"}
```

Names are case-insensitive ASCII identifiers of 3–64 characters, starting with a
letter or digit; later characters can also be `_`, `.`, or `-`. They are not
verified email addresses. Passwords have at least 12 characters and at most 1024
bytes. Password whitespace is not removed. Unknown JSON fields, including role
and account IDs on registration, are rejected. New users receive a personal
account at level 0; registration cannot create an administrator.

Passwords use Argon2id with a separate random salt. Session and API secrets use
256 random bits. Only their SHA-256 digests are stored. An issued secret is
returned once as `{"id":123,"secret":"..."}`. Keep it out of URLs and send it in
`Authorization: Bearer ...`. Core authorization does not use a login cookie.

| Method and path | Effect |
| --- | --- |
| `GET /core/v1/identity` | Read the current principal and allowed payer account IDs. |
| `POST /core/v1/auth/session/refresh` | Replace a session with a new secret. The old secret stops working. |
| `POST /core/v1/auth/logout` | Revoke the current session. |
| `POST /core/v1/auth/logout-all` | Revoke this user's sessions on all devices, but keep API keys. |
| `GET /core/v1/credentials?after=0` | List this user's credential metadata, without secrets. |
| `DELETE /core/v1/credentials/{id}` | Revoke a credential. Revocation is permanent. |

A login lasts seven days. Refresh extends inactivity expiry by seven days, but
never past 30 days from that login. Concurrent refresh has only one winner.
Refresh carries forward pending invitations issued by that login. Explicit
logout or revocation invalidates invitations still bound to the revoked session.
A changed user role or active state advances the user's authorization version;
disabling and re-enabling a user does not restore old credentials.

## Google sign-in

Set all three values before starting the core:

```sh
export LMM_CORE_GOOGLE_CLIENT_ID='<Google web client ID>'
export LMM_CORE_GOOGLE_CLIENT_SECRET='<protected Google client secret>'
export LMM_CORE_GOOGLE_REDIRECT_URI='https://your-host.example/core/v1/auth/oauth/google/callback'
```

Register that exact HTTPS redirect URI in the Google client configuration. Open
`GET /core/v1/auth/oauth/google` in the browser. The core redirects to Google and
sets a short-lived `__Host-lmm_google_flow` cookie with Secure, HttpOnly and
SameSite=Lax. The callback checks that browser binding, consumes state once,
exchanges the code with PKCE, and verifies Google's signature, issuer, audience,
expiry and nonce. Provider URLs and callback path are fixed by the server, not
accepted from the browser. Provider redirects are not followed and response
sizes and timeouts are bounded.

The callback returns the native session as non-cacheable JSON. A frontend can
integrate this response, but this task does not add a frontend login screen.
Google identity uses `(issuer, subject)`, never an email match. It cannot silently
attach to an existing password user or import platform roles from provider data.
Existing Google identities can log in with registration disabled. New ones cannot.
Account linking and password reset are not part of this implementation.

Tests use a local mock provider and a clearly marked PUBLIC signing-key fixture.
They exercise real signed tokens, code exchange and HTTP cookies without Go.
They do not replace a deployment check using the operator's real Google client.

## Team lifecycle and authority

Users always sign in personally. A user can create ONE team for their lifetime
and can join multiple teams. `created_by_user_id` is immutable and unique, even
after ownership transfer or closure. `owner_user_id` is the current owner. A user
who already created a team can receive ownership of another team; that is not a
new creation. This distinction prevents transfer from resetting the creation slot.

Platform role (`user`, L5 admin, L6 superadmin) and team role (`owner`, `admin`,
`member`) are separate. L5/L6 alone cannot view, edit, spend from or revoke keys
belonging to another team. Team management requires a personal SESSION, not an
API key. Owners manage admins and members. Admins manage members, but cannot
promote themselves or another member to admin. Members cannot edit their own
role or spending permission. A member/admin can leave by removing themselves;
an owner must transfer ownership or close the team instead.

| Method and path | Body or use |
| --- | --- |
| `POST /core/v1/teams` | Create a team with its own core account. |
| `GET /core/v1/teams?after=0` | List teams, roles, current versions and creation identity. |
| `PATCH /core/v1/teams/{id}` | `{"name":"Research","expected_version":1}` |
| `GET /core/v1/teams/{id}/members?after=0` | List member roles, spending permission and versions. |
| `PATCH /core/v1/teams/{id}/members/{user}` | `{"role":"member","can_spend":false,"expected_version":1}` |
| `DELETE /core/v1/teams/{id}/members/{user}?version=1` | Remove a member, or leave the team. |
| `POST /core/v1/teams/{id}/owner` | `{"new_owner_user_id":2,"expected_version":1}` |
| `DELETE /core/v1/teams/{id}?version=1` | Owner-only soft closure. |

Transfer requires an active existing member and an expected team version. It
changes ownership in one audited transaction. The old owner becomes an admin
WITHOUT spending permission. The new owner leaves the ordinary membership row.
All earlier team key grants and pending invitations become stale. A name change
also advances the team version; clients must replace affected keys rather than
assuming a name change keeps previous grants valid.

Closure disables new team use. It does not delete accounts, ledger entries,
consumption records or balances, and is not a refund or balance-transfer action.
Team closure is not reversible through this API. Membership history is retained
with increasing versions. Removing and inviting a member again never revives
that person's previous keys or invitations.

## Invitations

Create with `POST /core/v1/teams/{id}/invites`:

```json
{"recipient_user_id":2,"role":"member","can_spend":false,"ttl_seconds":3600}
```

An invite is bound to that existing native user, the issuer's current authority,
and current team/membership versions. It expires in at most seven days. This API
does not send email or allow anonymous acceptance.

The recipient lists `GET /core/v1/invites?after=0` and accepts using
`POST /core/v1/invites/{id}/accept`, or passes the one-time secret to
`POST /core/v1/invites/accept` as `{"token":"..."}`. Reject with
`POST /core/v1/invites/{id}/reject`. A permitted owner/admin withdraws with
`DELETE /core/v1/invites/{id}` and lists sent invitations with
`GET /core/v1/teams/{id}/invites?after=0`. Accept, reject and withdraw are mutually
exclusive terminal states. Metadata never returns invitation secrets. Acceptance
always rechecks live issuer authority, even if the inbox still shows pending.

## API keys and spending isolation

Create with `POST /core/v1/keys`, for example:

```json
{"owner":{"kind":"personal","id":101},"ttl_seconds":86400}
```

Use `{"kind":"team","id":7}` for a team-owned key. IDs here are public user or
team IDs, not internal storage-account IDs. Personal keys default to personal
funds only. An explicit existing funding policy can select teams for which the
user has spending permission. Team keys can select only their owning team's
funds: they cannot fall back to personal funds or another team. These restrictions
are checked in Rust and by database constraints. API key TTL is at most 365 days.

Owners/admins can list `GET /core/v1/teams/{id}/keys?after=0` and revoke team-owned
keys; a member can list/revoke their own keys only. Team roles do not grant access
to another user's personal keys or sessions. The core reads current user, team
and member versions on each authorization. Billing still must recheck authority
inside the transaction that reserves or settles money; an earlier HTTP identity
response is not a reusable spending approval.

Lists return up to 100 entries after an exclusive numeric ID cursor. Mutation
requests with stale expected versions return 409. Re-read state before retrying;
do not blindly replay ownership or permission changes. Errors do not expose SQL,
connection credentials, password hashes or bearer secrets.

## Verification

`tests/identity_native.rs`, existing identity tests and the private OAuth tests
cover registration, fixed limits, one-winner rotation, expiry, browser binding,
signed provider responses, invitation races, role isolation, team transfer,
closure, permanent grant invalidation, database invariants and audit rollback.
The shared acceptance suite also builds a disposable Go host for the unrelated
relay shutdown test; native identity tests do not connect to it.

```sh
cargo fmt --all --check
cargo clippy --locked --all-targets -- -D warnings
# DATABASE_URL must refer to an isolated local test PostgreSQL server.
cargo test --locked --all-targets --no-fail-fast
```
