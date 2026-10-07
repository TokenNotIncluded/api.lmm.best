# Market AI listing review

Tools and merchant products have independent `off`, `assist` and `auto` modes.
Both default to `off`. They share the existing official OpenAI moderation
`ModerationGroup` and `ModerationModel`, but do not inherit chat enablement,
assistant enablement, chat policies, fines, wallet charges or account sanctions.

`assist` records a private recommendation and leaves the submission pending for
an administrator. `auto` may approve or reject the current submission after a
successful classification. Provider errors, unavailable routes, oversized input
and failed tool technical validation retain pending/manual review. An earlier
assist job never starts applying decisions just because the setting becomes
auto. A captured auto job may apply only while the current setting remains auto.

## Input and meaning

The classifier receives public listing title and description, tool names and
descriptions, and product link titles and descriptions. It does not receive link
destinations, images, tool input/output schemas, credentials, gateway settings,
inventory/card keys, prices, orders, buyer accounts, contacts or pickup secrets.
Public text is redacted using the existing moderation redactor before dispatch.
Oversized input fails with `market_review_input_too_large`; it is never silently
truncated and approved. Private tool listings require manual review.

The result describes submitted text categories and scores. It does not certify
product quality, inventory legitimacy, payment safety or the content behind a
link. Image classification is not implemented. A clean tool classification
also requires a fresh run of the existing remote executor validation, an exact
`ValidationDigest == Digest`, and the existing metering checks before approval.

## Durability and manual decisions

Submission and queue insertion commit together; provider calls run later in the
existing leased moderation worker. Every submission receives a new review token,
including resubmission of identical text. The job retains that token, exact tool
version (where applicable), public text hash, captured route and captured mode.
Completion rechecks the enabled owner, token, hash, pending state and current
configuration under database row locks. Duplicate completion is idempotent.

An administrator may review their own listing and may override the current
automatic decision. Manual review cancels in-flight work and invalidates the
token, so a late response cannot replace the human decision. Editing or
resubmitting invalidates the corresponding old result. Results retain their
classification facts and expose `stale` or `overridden` when applicable.
Private administrator review queues include current AI-approved/rejected
decisions so they remain reachable after reopening the page. Pending submissions
sort first; a human override or a superseding draft removes an old AI decision
from that queue. Public catalogs do not gain review metadata.
Transient provider failures retry up to the existing three-attempt limit, then
remain durable `failed/manual_required` records. Terminal jobs clear the text
payload. Only stable error codes are exposed; provider bodies are not stored.

Market jobs use a dedicated completion transaction and explicitly carry no fee,
penalty notice, assistant review or wallet-transfer side effects. The old chat
completion entry point rejects market sources. Chat review lists and statistics
exclude market jobs.

## APIs

`GET /api/security/market-ai-review/settings` requires an administrator.
`PUT` at the same path requires a superadministrator and accepts exactly
`{"tool_mode":"off|assist|auto","store_mode":"off|assist|auto"}`. Both fields
are required; unknown fields, trailing JSON and invalid modes are rejected.
Enabling a mode requires an enabled official moderation route. The response also
includes the shared group/model, `engine: "openai_moderation"`, text coverage and
the supported categories. Changing these modes does not edit chat fine policies.

Private history endpoints require the current enabled owner or an administrator:

- `GET /api/tool-market/services/:id/ai-reviews?version_id=<optional-version>`
- `GET /api/store/products/:id/ai-reviews`

They return `{rows: [...]}` in descending job ID order, limited to 20 records.
A tool query defaults to its current draft or live version; an explicit version
must belong to that service. Rows expose version/hash, captured mode, lifecycle,
classification facts, recommendation, outcome, application flag, stable error
code and timestamps. Unprovided classification facts are `null`, not fabricated
zeros. Public product/tool DTOs do not include these private results or scores.

## Additive migration and rollout

The existing migration registry already includes all affected models. This
feature adds five columns with empty defaults and no new table:

| Table | New columns |
| --- | --- |
| `moderation_jobs` | `target_id` (varchar 36, indexed), `target_version` (varchar 36), `market_outcome` (varchar 24) |
| `tool_market_versions` | `ai_review_token` (varchar 36) |
| `merchant_store_products` | `ai_review_token` (varchar 36) |

All new columns are non-null. Existing jobs and listings retain their data;
there is no financial migration or replay. The new index is
`idx_moderation_jobs_target_id`. This migration belongs to the release containing
the AI feature, not the previously frozen shop release.

Keep both modes off until both production nodes and all active moderation
workers run the version that supports market sources. Confirm the two modes
remain off during a rolling upgrade; enable them only after both nodes are
verified. A Go85 worker can claim an unknown market job, cancel it permanently
and clear its text payload. Therefore mixed Go85/new workers must not process
newly enabled market reviews. They do not charge a wallet for the unknown source,
but they can discard pending AI work.

Before rollback, set both modes off while the new workers still run, then wait
until `moderation_jobs` has **zero** pending or running rows whose source is
`market_tool` or `market_product`. The new worker cancels disabled jobs without
calling the provider; manual review can also cancel a submission's remaining
job. Keep each listing pending for manual review. If active jobs remain, do not
start Go85 workers: finish draining them or explicitly handle those listings
manually first. Only then stop the new workers and restore Go85. N-1 acceptance
means additive schema compatibility and runtime with both modes off and no active
market jobs; it does not mean AI functionality is compatible while enabled.
Enabling modes is an explicit administrator action; this implementation never
enables them in production by itself.

Focused local tests cover reference/automatic behavior, owner/admin privacy,
settings validation, provider failure, stale content, resubmission, duplicate
completion, concurrent manual review, self-review, old completion isolation,
fixed wallet balances, and fresh tool validation failure. They use offline
provider/validator fixtures and SQLite; they are not live provider acceptance or
PostgreSQL migration-preservation proof.
