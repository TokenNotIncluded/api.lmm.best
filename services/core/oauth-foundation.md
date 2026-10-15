# Core OAuth issuer and delegated authority boundary

The unissued Pi foundation remains a separate internal adapter. Optional
LmmIssuer implements real native-browser consent for the read-only public client
`lmm`. Source presence does not prove production, HTTPS clients, Pi compatibility,
existing-install upgrade, recovery, or independent acceptance.
Operator guide: [Rust client authorization](../../docs/core-oauth2.md).

## Preserved protocol

Configured canonical HTTPS domain origin equals identity.web; resource is issuer
+ /api/oauth2. Advertised client lmm requests exactly catalog:read balance:read,
then receives explicitly displayed group snapshot scopes. Mandatory S256 reuses
the pinned oxide-auth primitive. Exact callback is
http://127.0.0.1:<nonzero port>/oauth/lmm/callback. Callback includes only
code/state/iss or error/state/iss. Independent canonical unpadded base64url
32-byte OAuth secrets are hashed at rest; never URL, Debug or ordinary logs.

GET creates an immutable pending flow only. Continue/Consent use the canonical
BrowserAuth family lock and persisted MFA assurance, stable SID and user epoch.
API keys/service levels cannot replace Session. Independent random cookie and
one-use CSRF bind the persisted request. CSRF-derived per-stage cookie names
prevent one delayed response from overwriting/clearing another tab's stage.
Cookie name is only selection, never identity/consent. Refresh cookie Path remains
/api/user/auth. Origin, bounded parsing, no-store and fixed CSP remain enforced.

## Persistence and resources

Formal fresh Core has 13 components including abuse and oauth-grants. Startup
checks exact DDL/catalog and narrow runtime rights, never initializes/upgrades.
Runtime may SELECT/INSERT issuer records and UPDATE only family scopes/revoked_at
or one-use consumed_at. No policy/immutable snapshot edits or token deletion.
Code is one-use with exact issuer/client/resource/redirect/PKCE. Refresh rotates,
never widens or extends family lifetime; consumed lineage replay commits family
revocation before refusal. Unknown result is not retry permission. Current
user/account/developer/group/version/expiry is checked under authority locks.
Canonical policy revocation updates the same user epoch; arbitrary owner PK
moves/TRUNCATE are outside the runtime contract, not claimed covered.

Read authority is purpose-limited, never NativePrincipal or funds authority.
Catalog uses the actual gateway's opaque immutable serving/credential/price
surface. Missing/stale is unavailable; known empty metadata is a true empty
response, not model-use proof. Balance uses exact integer ledger/opening
provenance and decimal conversion. Neither dispatches models or posts ledger.

## Pi and market completion remain explicit

Pinned CLI b8eb0f1512855aaf8098c15cbd76201f9acead5c requests two read scopes.
Pinned Pi 8a533140b0fb4470fc1ae195d52941891836f7d4 requires four application scopes
catalog:read balance:read usage:read models:invoke, with optional built-in MCP and
remote scopes. This issuer refuses lmm-pi; the CLI slice does not fulfill Pi.

core_charging.requests.key_id is NOT NULL FK to native credentials. Do not issue
synthetic keys, map OAuth families to Session/APIKey, or bypass that proof merely
to reuse charging. Real delegated spending needs an immutable source variant
with grant/access/client identity, current user epoch, stable server-selected
PERSONAL payer, group/model snapshot and grant budget/expiry. Reserve must hold
and recheck that proof in the same transaction as revocation/funds, recording the
original source/payer and preserving committed plus pending budgets through
refresh. Honeypot/new-cost guards precede new dispatch; old holds/settle/refund
must remain recoverable after revoke. Usage needs exact scoped source filtering.

Market OAuth requires separate explicit scopes and immutable tool version/price/
payer/caps consent. Model/catalog scopes never imply market:invoke. CoreTools
may implement native Session/APIKey consent first, but caller client_id is not
OAuth identity. A future purpose verifier returns verified
{grant_id,access_id,client_id,user_id,personal_account_id,auth_version,scope_snapshot};
reserve retains price/payer/cap checks and Go never receives a general Principal
or secret. Existing lmm families cannot acquire those scopes on refresh.

## Upstream and remaining acceptance

Fresh gh inspection: 197g/oxide-auth revision
0e3ef86d924aa5546dc3275d1d7269ea63edfaf1, oxide-auth 0.6.1, Andreas Molzer,
MIT OR Apache-2.0. Cargo.toml and src/code_grant/extensions/pkce.rs were read;
Pkce::required rejects plain/missing method. Repo not archived; this was its
2026-01-31 default-branch head at inspection. Core already pins it in Cargo.lock;
no new dependency/copied security protocol. PostgreSQL persistence remains Core
owned, not upstream in-memory stores. Rust 1.91 floor/actual 1.99 compatibility
still needs execution, not inference from manifest.

Required: compile and owned PG/TCP, independent frozen combined verification,
actual gateway/normal-provider/ledger negatives, real HTTPS CLI/keyring/browser/
MFA, installation/upgrade/backup/restore, real Pi delegated model/usage and market/
MCP, Google credentials/Passkey conditions. Failures/UNRUN remain incomplete.
