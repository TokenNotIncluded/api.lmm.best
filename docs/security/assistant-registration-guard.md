# Built-in assistant registration guard

L0 admission is performed through the built-in assistant, not a second reviewer
model or a user-submitted recommendation letter. The public legacy submission
route returns 410; historical requests and administrator audit/override helpers
remain readable. Legacy submission and administrative approval/rejection writes
return 410. Pending and rejected letters no longer appear in onboarding or the
live todo queue, including unread counts. Historical records are not deleted.

## L0 to L1

An enabled L0 account can describe an ordinary use such as coding, learning or
chatting. The assistant can call `grant_developer_access` in that same first turn;
there is no completed-turn minimum, recommendation letter, client-name, repository
or work-proof requirement. `minimum_completed_turns` remains zero in the response
for older clients. Conversation ownership, browser-session identity, current
account status and fresh registration-risk evidence are still required. A
current explicit trust override remains authoritative. A retired application
rejection is not a current restriction and does not veto a new grant.

The action is limited to the signed-in user's L1 access. It cannot select another
user, credit a wallet, grant higher levels or restore a suspended account. The
transaction rechecks the user and risk state, records a new audit receipt and
returns the same grant for repeat calls. It does not rewrite historical letters.
Welcome rewards retain their own identity and gift checks.

A held account is directed to human support. A failed check is a service error,
not a request to keep chatting. The assistant must not demand more turns, invented
waiting periods, projects or proof, or blame urgent/frustrated users. Paid
activation continues to use the existing real-payment policy; a legacy letter
must not become a second payment approval gate.

L0 can open `/support` and `/todos`. Human handoff and messages use the existing
owner-scoped support API, independently of model availability and L1. The pricing
read/runtime/aggregate performance endpoints, own notifications and acquisition
self-report routes match their L0-visible pages; unrelated API, admin and payment
write boundaries remain unchanged. Deploy the Go and Web changes together (Go
first during a rolling release). Refresh the real account after a grant; cached
letter status is never an access decision.

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
