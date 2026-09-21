# Built-in assistant registration guard

L0 admission is performed through the built-in assistant, not a second reviewer
model or a user-submitted recommendation letter. The public legacy submission
route returns 410; historical requests and administrator audit/override helpers
remain readable. The existing three completed server-recorded turns minimum for
an L1 grant remains. It is an anti-replay prerequisite, not proof of personhood.

## Boundaries

- `get_registration_risk` reads owner-scoped redacted history and aggregate
  cross-account observations. `notify_registration_risk`,
  `end_registration_conversation`, and `ban_l0_user` accept **no arguments**.
  The server resolves the browser actor and owned conversation, not the relay
  billing account. Tool calls never choose another target, run SQL, or set an IP
  allowlist.
- Casual replies, topic changes, spelling, speed, nickname choices, QQ email and
  AI-assisted writing do not authorize sanctions. No puzzle or nickname story
  proves that a person has never registered before.
- HMAC-indexed long-message shingles are combined across multiple messages and
  accounts. A template campaign alone creates an alert; a campaign plus linked
  network peers pauses access/rewards. Suspension additionally requires an
  already-consumed canonical reward identity. Shared networks alone never ban.
- L1, paid/developer access, administrator roles and explicit trust overrides are
  excluded from the new L0 sanction tool. Every write rechecks durable evidence
  under the target user row lock. No model confidence score can override it.
- Sanctions persist a terminal notice, restrict the current conversation, advance
  the existing authentication-version fence, and create an audit event in one
  database transaction. Remaining tools in that batch do not execute. Automatic
  sanctions share a database-locked UTC daily cap across instances (default 5,
  maximum 5). A disabled/exhausted cap returns an actual `notify` receipt.
- Administrators can restore guard-suspended accounts after confirmation. A
  seven-day automatic-resuspension grace period follows. Restoring does not reset
  the welcome-credit identity ledger or silently reopen a restricted conversation.

## Operation and privacy

Four new tables are included in both migration entry points. Risk fingerprints
are limited to 256 per observed account and seven days; raw foreign conversation
text, email, IP and peer IDs are not returned by the tools. The stable secret is
reused from the existing gift-risk key table. Preserve that table and secret on
migration; changing it invalidates correlation.

The index starts from observed assistant traffic, not a retroactive scan of every
historical conversation. A tool may read the current user's last six redacted
messages. There is no claim of full historical coverage, AI detection accuracy,
or unique-human identity verification. An email or server-bound OAuth subject is
required for a fresh registration observation. Admission/reward checks fail
closed on missing, stale or changed identity evidence while normal support can
continue. Gift claiming refreshes server request metadata before checking.

Alerts are durable **in-site inbox events**, not email/push deliveries. The new
settings inbox supports pagination and restoration. It does not mass-ban a whole
cohort and does not run a new autonomous reviewer. Existing deterministic
aggregate reporting and historical audit structures are retained.

Options `AssistantRegistrationAutoSuspendEnabled` and
`AssistantRegistrationDailySuspendCap` are the only writable registration-guard
settings. Internal budget state is not writable through the options API. Ordinary
L0 conversations cannot create global blocking rules; administrator configuration
uses the existing restricted configuration capability.

## Validation

Regression suites cover weak signals, cross-user ownership, protected roles,
missing/stale/changed identity, OAuth without email, repeated sanctions, auth
version changes, restoration and the global cap. Frontend tests cover unknown and
failed states, recommendation-form removal and the human-support explanation.
Run Go model/controller tests and web typechecking in a dependency-equipped
runner. Production traffic, PostgreSQL/MySQL concurrency, Redis outage behavior,
and browser visual review must be verified before deployment; SQLite unit tests
alone do not certify these behaviors.
