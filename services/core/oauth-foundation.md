# Unissued OAuth foundation v1 (phase 1)

Approved boundary at base `f27526800a316831f9df957ab785002d9e407a55`.
This is not a Pi-compatible login or a deployment/upgrade procedure.

## Frozen wire profile

Production issuer: canonical HTTPS origin, no credentials/path/query/fragment.
Only a typed explicit development option accepts http://127.0.0.1[:port], matching
actual Pi `LmmHttp` test support. Resource is exactly issuer + `/api/oauth2`.
Discovery paths: `/.well-known/oauth-authorization-server` and
`/.well-known/oauth-protected-resource/api/oauth2`. Future endpoints are
`/api/oauth2/{authorize,token,revoke}`. None are mounted in phase 1.
Public client: `lmm-pi`; redirect: `http://127.0.0.1:<1..65535>/oauth/lmm/callback`
with exact binding including port, no DNS aliases, credentials, query or fragment.
Code + mandatory S256 PKCE; bounded nonempty state echoed with issuer `iss`.
Code/access/refresh prefixes: `lmm_oc_`, `lmm_at_`, `lmm_rt_`; 32 random bytes,
hashed at rest, domain-separated by prefix. Code TTL <=300 seconds, access TTL
1..86400 seconds, fixed family TTL <=31536000 seconds. Refresh never extends
family lifetime and replaces the token, never widens scope. Access revoke revokes
the whole family. Consumed refresh lineage is never deleted while any family
credential could be valid; consumed replay commits family revocation even when
that individual refresh is expired. Concurrent losing refresh is replay, not retry.
A lost successful refresh response requires login again, not secret recovery.

Scope vocabulary: `catalog:read balance:read usage:read models:invoke`, optional
`mcp:bounties mcp:drawing remote:control`. No group scopes in this foundation.
Future lmm-pi issuance requires all four mandatory scopes in request, selected
consent AND actual Core-supported purpose bindings. Unsupported optional scopes
are omitted, not granted. Fixture-seeded scopes do not prove these capabilities.

## Authority and persistence

`identity::grants::GrantStore`: explicit read-only contract-checked open,
authorization-code exchange, refresh, revoke, and remote-only purpose validation.
No production issuance, consent proof constructor, HTTP exposure, startup config,
feature advertisement or automatic DDL. Fixture seeding exists only in cfg(test),
uses real locked native Session authority and is NOT CSRF/consent acceptance.

Dedicated `core_oauth` v1 schema contains contract, families, codes, access_tokens,
refresh_tokens. Native identity-v3 schema/credentials remain unchanged. Family
binding includes user, stable PERSONAL account, auth version, consent version,
issuer/resource/client, scopes and fixed expiry. Current consent version is the
family's live version; access/refresh/code store their birth version and fail if
changed. No birth Session foreign key: normal browser refresh is independent.
Code and refresh consumption is transactional. Lock order is family, code/token,
then native user/account (shared); bounded statement/lock timeout. All errors
roll back, except consumed-refresh replay explicitly commits revocation first.
Opening verifies exact DDL receipt and live component definitions; no re-receipting
or catalog bypass. Fixture owner installs SQL explicitly into a newly created
private database. Application runtime receives only narrow DML, never schema
ownership, contract writes or token deletion authority.

## Additive RPC

`CoreControl.AuthorizeRemote(AuthorizeRemoteRequest {})` uses existing server-owned
`authorization`, `x-lmm-protocol=1`, `x-lmm-user-credential` metadata, not request
owner/funders/scopes. Existing RPC fields/numbers and CredentialKind stay unchanged.
Response fields: source oneof `native_personal=1` / `oauth_grant=2`, current user
`user_id=3`, stable PERSONAL storage account `owner_account_id=4`, live
`auth_version=5`, credential expiry unix seconds `expires_at=6`, verified
`remote_permission=7`. NativePersonal: credential_id=1, native_kind=2 (only SESSION
currently permitted). OAuthGrant: opaque grant_id=1, opaque access_id=2. No platform
role, funding accounts, financial/spending authority. Source is never inferred by
Go. OAuth branch requires an explicitly bound checked GrantStore; phase 1 host
keeps it unconfigured and returns unavailable. Native APIKey/host/group tokens
are denied. Native general Authorize/profile/admin/model spending still reject
OAuth tokens. Remote leases must recheck at <=15 seconds; response expiry is not
permission to cache through user, owner, consent or revocation changes.

## Owner procedure freeze

Native base and schema contract remain identity-v3. No core_schema COMPONENTS,
startup catalog, installer/runtime privilege changes are authorized in this phase.
Unconfigured old startup remains compatible. Private fixtures install OAuth SQL
only after native fresh initialization; such databases are NOT approved assembled
Core installations (their old assembled catalog receipt would no longer match).

A future reviewed owner-only offline upgrade MUST bind exact prior source SHA,
all compiled component fingerprints AND stored/live catalog fingerprint, ownership
and ACL snapshot, row/sequence hashes and recovery archive. It must reject unknown,
already-upgraded, conflicting or drifted states before any DDL. In one transaction,
lock installer authority, add this component and minimal reviewed runtime grants,
verify native data/ownership/ACL invariants and only then record the exact new
compiled contract/live catalog. No arbitrary re-receipting. Crash/replay/conflict
rehearsal and isolated pg_dump restore verification precede any deployment. This
procedure is a requirement, NOT implemented/authorized SQL for occupied databases.
Production fresh installer integration also requires review and isolated restore.

## Upstream provenance

Protocol PKCE reuse: 197g/oxide-auth pinned git revision
`0e3ef86d924aa5546dc3275d1d7269ea63edfaf1`, oxide-auth 0.6.1, MIT OR Apache-2.0,
Andreas Molzer. Real GitHub source inspected with gh: Cargo.toml and
src/code_grant/extensions/pkce.rs; `Pkce::required()` rejects plain/missing method.
No MSRV declared upstream; Core MSRV is 1.91; compatibility must be checked locally.
Core uses its own PostgreSQL adapter, not upstream HashMap token persistence or
optional refresh rotation. No custom PKCE cryptography. Rauthy reference pin
`96be553802a3828a2c756a7a50ff42eb72f3c53f` (Apache-2.0, Rust 1.95) is reference
only; no copied code or identity replacement. Async upstream adapters not needed
for isolated PKCE primitive reuse; full protocol HTTP integration is phase 2.

## Incomplete gates

- Authenticated browser consent UI, server-owned CSRF, request/consent/capability
  policy, issuer-bound success/error redirects and production code issuance.
- Actual scoped catalog/balance/usage/model HTTP; reviewed personal-only delegated
  budget/spending guards. Representation of models:invoke grants is NOT exposure.
- Discovery, token/revoke HTTP, Go adapter/host/proxy/consent wiring, Pi 1.1 binding.
- Production fresh/offline owner procedure, formal isolated DB restore/rehearsal.
- Independent real Core/PG/RPC/Go/client full-flow tests and security acceptance.
