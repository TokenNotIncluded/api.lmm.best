# Changelog

> This is the historical combined changelog. Production artifacts are now
> versioned independently as `go-v*` and `web-v*`; their immutable GitHub
> release notes are authoritative. Do not use this file to create a generic
> `v*` tag or infer compatibility between component versions.

All notable user-facing and operational changes are recorded here. The
administrator can publish the matching release note from system settings;
authenticated users then see it once after their next login.

## Unreleased

<!-- Add user-facing or operational changes here before the next release. -->

## [0.1.6] - 2026-08-17

- Internal production metadata alignment for this release cycle.
## [0.1.4] - 2026-08-17

- Fixed PostgreSQL assistant-profile UPSERTs by qualifying the aggregate
  counter column; assistant chat requests no longer lose profile statistics
  with SQLSTATE 42702.
- Expanded the shell-only customer simulation set to a continuous A–O matrix,
  including open-source contributors, high-frequency API builders, new L0
  applicants, team integrators, login-recovery users, and frustrated support
  users; these profiles do not write keys, payments, or upstream requests.
- Added auditable support-seeking and L0-applicant assistant profiles. Login
  errors such as 502 now receive an incident-triage welcome strategy, while
  otherwise quiet L0 users receive an explicit assistant-only/L1-review path;
  aggregate profile counters remain allow-listed and contain no user identity.
- Added a per-context assistant cache gate so concurrent identical L0
  questions do not fan out into duplicate upstream model calls before the
  first deterministic response is stored; tool-backed and cross-context
  responses remain isolated.
- Extended the shell-only A–I persona suite to support isolated per-persona
  login credentials, optional per-persona 2FA/Turnstile values, and redacted
  L0/L1 boundary evidence without creating keys, payments or upstream calls.
- Added an administrator-only assistant funding summary. The user-management
  panel now separates assistant requests from ordinary root-account traffic
  and shows the last 30 days' USD spend, token volume and remaining
  super-administrator quota.
- Strengthened the shell-only A–I persona acceptance suite: every selected
  persona now verifies its deterministic intent route, security-risk persona
  still requires the refusal policy, and focused runs can select only the
  needed profiles to reduce assistant quota spend.
- Improved the anonymous homepage entry points and added a real L0 black-box
  check: L0 users are guided to the AI assistant while key creation remains
  denied until the access boundary changes.
- Added the L0 AI assistant entry flow. L0 users can browse the permitted
  read-only areas and use the assistant to prepare an upgrade request; console,
  key creation, payment, plan and discount actions remain unavailable until an
  administrator approves L1 access.
- Added context-aware assistant guidance for setup, model IDs, base URL,
  API-key creation, package selection, discounts, invitations, open-source
  bounties and historical usage analysis. Sensitive credentials, raw OAuth
  subjects, balances and conversation contents are excluded from the context.
- Added normalized question caching, user-context isolation, configurable
  assistant personas/tools and super-administrator funding for assistant
  relay usage. Cache hits now avoid duplicate assistant-intent database writes
  while preserving exact response bytes.
- Added an administrator-only, aggregate customer-profile summary for the
  assistant. It stores only an hourly profile counter—never a user ID, email,
  raw question or conversation—so storage remains bounded; older backends can
  omit this optional panel without breaking the support queue.
- Added the Anthropic-aligned advanced security policy with an Aho-Corasick
  matcher, audit/block actions, digest-only event records, public risk
  statistics and administrator configuration. The same guardrail now covers
  text prompts in task/video, Suno and Midjourney submissions before pricing,
  billing or upstream dispatch; sensitive-word rejections also return a
  stable non-retryable 400 response.
- Added a production-operator customer profile so benign questions about
  reliability, concurrency, rate limits and observability are not mistaken for
  abuse; bypass, scanning and brute-force language still follows the security
  risk path.
- Refined assistant persona precedence so an explicit request for step-by-step
  help is routed to the guided-buyer strategy even when the message also says
  the user is technical; added deterministic A–I welcome-strategy coverage.
- Added a pre-model security refusal for high-confidence bypass, scanning,
  brute-force and prompt-extraction requests. These responses are cacheable,
  deterministic, and do not spend the super-administrator assistant quota;
  authorized non-destructive testing and security-report guidance remains
  available.
- Tightened administrator handoffs so the request is required and contains at
  least five characters at the AI tool, browser form and API validation layers;
  security-risk signals now take precedence over promotion-seeking signals.
- Known disposable-mail domains no longer receive new-account or invitation
  promotional credits; ordinary privacy-mail domains, account access, and
  administrator review remain unaffected.
- Administrators now see a disposable-email risk marker in the user list;
  the marker is excluded from ordinary user responses.
- Added release-note delivery and acknowledgement so a published changelog is
  shown after the user's next login and not repeatedly during an active session.
- Improved level, payment-visibility, administrator, mobile and chart-display
  behavior, including safer contrast for stacked usage charts.
- Standardized production operations on the native `lmm-api-go` CLI, with
  checks for package integrity, service health, resource pressure and rollback
  evidence before release promotion.
- Hardened the administrator-configured assistant search connector against
  private, loopback, link-local and reserved address targets, embedded
  credentials, DNS rebinding and unsafe redirects.

### Verification

- Go controller, service, setting and model tests pass.
- Frontend build check and targeted assistant tests pass.
- Shell-only operator persona acceptance tests pass for technical, guided
  buyer, promotion, security, normal, mobile, privacy and screen-reader
  profiles.

[Unreleased]: https://github.com/LIghtJUNction/api.lmm.best/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/LIghtJUNction/api.lmm.best/compare/v0.1.5...v0.1.6
[0.1.4]: https://github.com/LIghtJUNction/api.lmm.best/compare/v0.1.3...v0.1.4
