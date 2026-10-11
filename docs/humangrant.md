# HumanGrant — Rust core integration

Protocol: https://github.com/LIghtJUNction/humangrant/blob/main/skills/humangrant-integrate/SKILL.md

## Flow

An AI creates an Ed25519 key pair and signs its own HumanGrant requests. The Rust core returns a request ID, an agent-readable Markdown status page at /v1/requests/{id}/status.md, and a different human-only review URL at /human/{link}. The AI forwards the human URL.

The human opens the review URL in a browser, signs in using the **existing LMM identity**, reviews the requester and permissions, and explicitly approves or refuses. Approved permissions are stored separately from browser sessions and ordinary LMM API keys. The AI can claim a short-lived, opaque, one-time credential bound to its Ed25519 key. The original user password, session cookie, and bearer token never go to the AI.

The human can list and revoke grants at /human/grants. Each delegated call checks the actor key, message signature, nonce, token digest, permission, active account, expiry, and revocation against the database.

This initial integration permits only **lmm.profile.read on self**, through GET /v1/humangrant/profile. It does not provide model invocation, account administration, payment, balance changes, or a general login session. Each new permission must have an independently audited server handler.

## Default enabled; administrator may disable

For fresh installations core_identity.hg_config.enabled defaults to TRUE. The System Settings authentication section provides a HumanGrant switch backed by GET and PUT /api/humangrant/settings. The PUT requires a current native browser bearer, exact allowed Origin, and platform administrator role. Turning the switch off blocks new requests, consent, claims, and existing AI session use; revocation stays available.

The login page shows the entry point when the Rust browser identity service is configured and the switch is on. It links to /human, which explains how to receive a request from an AI.

The canonical external origin must be configured accurately, preferably HTTPS. Configure the reverse proxy to send /human, /human-grant, /v1/requests, /v1/session, /v1/humangrant and /api/humangrant routes to the Rust core, not the static SPA fallback. All protocol and consent responses must use no-store caching.

## Deployment limits

**Fresh database only.** The checked-in schema and runtime role grants support an isolated first installation with lmm-core init-db. The core's existing strict schema contract does not provide online upgrades to this schema: write and validate a separate migration before deploying to an occupied database. Apply deployment/postgres/grant-core-runtime.sql offline to the runtime account.

The optional email endpoint returns 503 mail_unavailable: it does **not** send or claim acceptance of an invitation. An actual mail implementation needs fixed templates, delivery handling, and persistent per-requester/per-address/per-source limits.

Do not deploy to production before Cargo checks, PostgreSQL integration tests, and real human browser consent and reverse proxy tests pass. Do not log grant credentials, signing secrets, review link tokens, or CSRF tokens.
