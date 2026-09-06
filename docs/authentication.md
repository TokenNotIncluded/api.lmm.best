# User Authentication and Login Sessions

Panel authentication now uses a combination of short-lived access tokens, `HttpOnly` refresh cookies, and server-side session control. Panel requests no longer rely on Gin session middleware, and the `New-Api-User` request header is no longer required.

## Authentication Model

- Access token is a 15-minute JWT kept only in browser memory and sent via `Authorization: Bearer <token>`.
- Refresh token is a random opaque value with a maximum lifetime of 30 days. The browser stores it only in an `HttpOnly`, `SameSite=Strict` cookie; the server stores only an HMAC digest and rotates it at each refresh.
- `user_sessions` is the session control plane. It stores device, IP, login method, last active time, expiry, and revoked state. Session state in the database is the source of truth. Revocation propagation depends on Redis topology described below.
- When security-related fields like password, status, role, or security factors change, `auth_version` increments and invalidates prior sessions. Plan changes that only alter grouping may refresh authorization cache and do not force logout.
- Redis caches both authentication and session snapshots. Version fences and revoke tombstones prevent stale cache reauthorization. Session snapshots use TTL tied to `SYNC_FREQUENCY`. On cache miss or without Redis, authentication falls back to database checks.

`SESSION_SECRET` derives separate signing keys for access tokens, security proof, refresh hash, and AuthFlow digest.

In production and multi-node deployments, all nodes must share the same high-entropy secret value; rotating it invalidates existing sessions, temporary auth flows, and security proofs.

## Multi-Node Redis Topology

Multi-node setups must share the same primary database.

- Session count, account-level active-session limit, and issuance window counts are enforced in the database, so limits apply globally across nodes.
- Redis session hashes (`revoking`/`revoked` tombstones) are cache only; TTL is the minimum of remaining session lifetime and `SYNC_FREQUENCY`.
- `SYNC_FREQUENCY` defaults to 60 seconds, including invalid values.
- Cache reads never extend TTL. On expiration, fallback to database by SID.
- Completed active cache writes can only continue when DB observation window remains; they cannot revive a full cache cycle after revoke tombstone expiration.

| Redis Topology | Session State Propagation | Rate-Limit Semantics |
| --- | --- | --- |
| Shared Redis across all nodes | Revoke and version updates propagate through one shared cache | Rate-limit quota is shared across all nodes |
| Independent Redis per node | Convergence after cache TTL expiry, at most within `SYNC_FREQUENCY`; old cache may return 401 briefly during version rotation on a node | Limits are independent per node, so worst-case combined quota can scale to node count × single-node cap |
| No Redis | Every check queries database directly | Node-local in-memory limiters, limits are independent |

A larger `SYNC_FREQUENCY` widens stale cache windows for independent Redis. A smaller value increases per-node database checks for active sessions.

With default settings, active sessions can add one rate check approximately every 60 seconds per node.

When shared Redis is used, revocation tombstones and version changes still propagate immediately.

All nodes must also share the same `CRYPTO_SECRET` when using the same Redis, otherwise cache key digests differ and caches cannot be shared.

This scoped guarantee applies to login authentication only. Rate limiting and other caches can still be affected by topology.

## Browser Interfaces

After login, password, 2FA, passkey, OAuth, WeChat, and Telegram flows return a unified payload:

```json
{
  "success": true,
  "data": {
    "access_token": "...",
    "token_type": "Bearer",
    "access_expires_at": 1730000000,
    "user": {},
    "session": {
      "sid": "...",
      "current": true,
      "login_method": "password",
      "ip": "...",
      "user_agent": "...",
      "created_at": 1730000000,
      "last_active_at": 1730000000,
      "expires_at": 1732592000
    }
  }
}
```

Session endpoints:

| Endpoint | Auth | Purpose |
| --- | --- | --- |
| `POST /api/user/auth/refresh` | Refresh cookie; Origin check added in secure mode | Rotate refresh token and issue a new access token |
| `POST /api/user/auth/logout` | Refresh cookie; Origin check added in secure mode; Bearer optional | Revoke current login session and clear cookie |
| `GET /api/user/sessions` | Bearer | List valid sessions for current auth version, current session first, max 100 |
| `PUT /api/user/sessions/settings` | Browser-session Bearer | Save the current user's automatic session sign-out preference |
| `DELETE /api/user/sessions/:sid` | Bearer | Revoke selected session, including current one |
| `POST /api/user/sessions/revoke-others` | Bearer | Keep current session and revoke all others |

When an in-memory session exists, include `X-Auth-Session: <sid>` on refresh/logout calls.

If refresh cookie SID and header SID mismatch, both endpoints return `409 AUTH_SESSION_MISMATCH` and do not rotate/revoke/clear any session.

The client should first call refresh to clear stale session SID in this tab, align cookie to current session, then retry logout.

When no active memory session exists at cold start, this header may be omitted.

Concurrent use of the same refresh token is supported. The server deterministically rotates to one successor token so other tabs are not forced to log out due to missing “winner” response.

If the latest refresh token reappears after a short tolerance window, it will revoke the corresponding session. Unknown earlier or random tokens are rejected and cannot be used to kick sessions by guessing.

The frontend serializes refresh calls per profile using Web Locks and shares session identifiers and login/logout events via BroadcastChannel (fallback to `storage` events).

Access token and refresh token are never shared across tabs via Web Storage.

## Session Issuance Limits and Retention

The **Automatically sign out sessions older than one week** switch is enabled by default for new and existing accounts. The user preference is stored as `session_auto_logout` in `users.setting`; a missing value means enabled. `GET /api/user/sessions` keeps its `data` array and includes the effective `session_auto_logout` boolean at the top level. Save it with `PUT /api/user/sessions/settings` and a JSON body such as `{"session_auto_logout": false}`. A boolean is required; PATs cannot change this browser-session setting.

When enabled, a session is signed out when its login time (`created_at`) is strictly earlier than `now - 7 × 24 hours`. Exactly seven days is still within the boundary. Refreshing a token or using a device updates activity without resetting this login age. The rule also applies to the current session. Turning the switch off preserves sessions still valid under the original 30-day lifetime; it cannot restore a session already revoked.

The Go backend checks the age during access-token validation and refresh, and cleans up old sessions before login issuance, before listing sessions, and when enabling the switch. The master node also performs cleanup at startup and every hour, including accounts that no longer visit the site. Automatic sign-out uses the existing revocation and cache tombstone path; revoked records remain subject to audit retention below. This preference currently applies to the default Go backend; the Rust preview has not implemented it.

Login issuance enforces two-tier account-level checks for all login methods:

- `USER_SESSION_ACTIVE_LIMIT` (default `50`): max number of unexpired active sessions. Exceeded returns `409 AUTH_SESSION_LIMIT`.
- `USER_SESSION_ISSUANCE_LIMIT` (default `100`) and `USER_SESSION_ISSUANCE_WINDOW_SECONDS` (default `86400`): total sessions created in window, including revoked sessions and old auth versions. Exceeded returns `429 AUTH_SESSION_ISSUANCE_LIMIT`.

These counters and inserts are not wrapped with cross-node DB locks, so rare over-allocation can occur under extreme concurrent logins, but issuance is denied when counters fail.

Accounts already above active limits are not forcibly logged out during upgrade; limits apply only to subsequent token issuance.

`USER_SESSION_REVOKED_RETENTION_DAYS` (default `7`) controls audit retention for revoked rows.

Issuance window must not exceed revoked retention; otherwise startup logs warnings and clamps the effective window to retention to avoid undercounting throttling from early revoked-row cleanup.

Cleanup does not delete expired sessions if `created_at` remains inside issuance window. Unexpired revoked rows are retained through retention window.

Expired entries are preserved to avoid silently reducing issuance counters or audit records.

Active count includes active rows with stale `user_auth_version`; because device list shows only current version, users may need to use “revoke other sessions” on an active device to clear hidden stale rows. Password reset revokes all sessions but does not reset issuance counters.

Only master nodes run periodic cleanup every hour for expired sessions and revoked sessions beyond retention.

`USER_SESSION_HOURLY_ALERT_THRESHOLD` (default `5000`) only creates alerts during unusual hourly issuance spikes and is not a global circuit breaker.

## Refresh/Logout Origin Checks

Refresh and logout origin controls are tied to cookie security mode:

- If `SESSION_COOKIE_SECURE` is unset or `false`, refresh cookie works on local HTTP. Refresh/logout OriginGuard is disabled and `SESSION_COOKIE_TRUSTED_URL` should not be configured. This enables `http://localhost` with different ports for local dev proxies.
- If `SESSION_COOKIE_SECURE=true`, refresh cookie is HTTPS-only and strict OriginGuard is enabled. `POST /api/user/auth/refresh` and `POST /api/user/auth/logout` validate browser `Origin`. If `Origin` is absent, a valid single `Referer` is accepted as fallback.

Allowed origins include request origin and exact entries in `SESSION_COOKIE_TRUSTED_URL`.

Secure Origin checks ignore client-provided `X-Forwarded-Proto`.

When TLS terminates at reverse proxy, include public HTTPS panel origin in `SESSION_COOKIE_TRUSTED_URL`.

`SESSION_COOKIE_TRUSTED_URL` now explicitly lists trusted origins for refresh/logout cookies only; it is not a CORS whitelist.

Rules:

- Configure only when `SESSION_COOKIE_SECURE=true`.
- Use comma-separated exact HTTPS origins, for example `https://panel.example.com` or `https://panel.example.com:8443`.
- No wildcards, paths, query strings, user info, or domain suffix matching.
- Does not modify relay behavior or CORS for legacy billing dashboard, `/api/usage/token`, or `/api/log/token`.

Local development example (OriginGuard disabled):

```env
SESSION_SECRET=<local-random-value>
SESSION_COOKIE_SECURE=false
# Do not set SESSION_COOKIE_TRUSTED_URL
```

Production example (OriginGuard enabled):

```env
SESSION_SECRET=<high-entropy-random-value>
SESSION_COOKIE_SECURE=true
SESSION_COOKIE_TRUSTED_URL=https://panel.example.com,https://admin.example.com
```

This switch only controls refresh/logout origin behavior.

## Trusted Proxies and IP Rate Limits

Gin now defaults to trusting all proxy-supplied client-IP headers. This project moves to a three-state control:

- If unset/empty/whitespace, trust loopback and private CIDRs by default: `127.0.0.0/8`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, and `fc00::/7`, with startup warning.
- This protects localhost/Nginx/Docker setups but may not be sufficient for public internet direct access.
- `TRUSTED_PROXIES=none` (case-insensitive, exact only) enables strict direct mode and ignores all proxy headers.
- Any non-empty value is parsed as comma-separated IP/CIDR and fully replaces defaults.
- Invalid CIDR, empty list, or mixing `none` with values prevents startup.

When request source is trusted proxy, Gin reads header chain from right to left and takes the first non-trusted hop as client IP.

Default trust of private network implies residual risk: other hosts in the same private network that can directly reach the app may forge those headers. Use `none` or precise proxy addresses for stronger enforcement.

Redis rate limiting uses atomic Lua fixed window instead of prior approximate list-based sliding window.

This intentionally changes behavior: both sides of a boundary window can pass, allowing up to about twice configured rate in very short bursts (for example, `20/20min` may briefly pass ~40).

User-level model success limiting still uses existing Redis List sliding window, with timestamps written in UTC.

During rolling migration, mixed local-time and UTC entries may cause temporary over/under-limit within a single window. Consistency is restored after one full window across all nodes.

This migration keeps keyspace unchanged.

Public registration remains protected by critical IP limiting; distributed multi-account attacks cannot be fully prevented by IP limits alone.
For public deployments, enable Turnstile and email verification. Additional device/feature risk controls should be separate security projects.

## PAT Access Contract

Panel PAT (`User.AccessToken`) remains `Authorization: Bearer <pat>`, and still accepts `Authorization: <pat>`.

`New-Api-User` is no longer required for auth; external scripts no longer need duplicate Bearer+user-id headers.

PAT is not a panel login session. It cannot call session management APIs and cannot produce session-bound security proofs.

## Temporary Auth Flow and Re-Verification

OAuth state, 2FA pending, passkey ceremony, and Telegram bind state live in `auth_flows`. Client stores random `flow_token`; database stores only HMAC digest.

Flow records bind purpose, provider, intent, user, and login session and are atomic single-use.

OAuth affiliate code is persisted through the login auth flow as well.

Standard OAuth bind callback sends result via same-origin `postMessage` to popup opener. Only opener calls backend bind APIs with its own in-memory Bearer token.

Telegram binding creates binding AuthFlow while user is logged in; Telegram widget callback returns with `flow_token` in URL path. Callback verifies source login session is still valid.

Telegram signed widget assertion is a one-time credential and replay is rejected.

Sensitive operations use 5-minute `X-Security-Proof`:

- `channel.key.read`: read channel key
- `passkey.register`: register passkey
- `passkey.delete`: delete passkey

Proof binds user, login session, user auth version, session version, and scope. It cannot be reused across user/session/purpose.

For users with 2FA enabled, both passkey register begin and finish must include valid `passkey.register` proof. Finish re-validates proof before consuming AuthFlow token.

Passkey registration for users without 2FA does not require this header.

## Upgrade Notes

- Legacy `session` cookies are no longer used; existing panel logins become invalid after upgrade.
- Database migration adds `user_sessions`, `auth_flows`, `external_identity_claims`, and `users.auth_version`, and initializes auth versions and Telegram ownership.
- Migration rejects startup if historical data has one Telegram ID linked to multiple users.
- Migration adds indexes for session issuance counts and batched cleanup.
- For large existing `user_sessions`, allocate maintenance window on first startup.
- `user_sessions.previous_refresh_hash` migrates from fixed `char(64)` to `varchar(64)`. Application reads both.
- Migration is idempotent and should not repeatedly alter column type.
- Only master nodes run periodic cleanup of expired sessions, revoked sessions beyond retention, and old AuthFlows.
- Without explicit `TRUSTED_PROXIES`, default private proxy trust applies; deployments with public LBs, `100.64.0.0/10`, link-local, or custom CNI ranges should configure explicitly.
- Set `none` to ignore all forwarded headers.
- Redis limiter now uses fixed window with explicit boundary burst semantics (up to ~2x burst).
- User-model success limiting has transient mixed-timestamp window during rolling migration.
- Self-built clients should adopt new AuthBundle, `flow_token`, and Security Proof contract.
- PAT clients can remove `New-Api-User` usage.
