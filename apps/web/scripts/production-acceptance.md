# Production post-deploy acceptance

`production-acceptance.mjs` captures a deployment-bound baseline and performs
live post-deploy acceptance against exactly `https://api.lmm.best`. It does
not publish releases, deploy services, or change Git state.

`baseline` logs in as root, records enabled channel identities, and logs out.
`verify` checks the deployed backend/frontend and channel set, creates one
isolated common user and API key, grants that user 10,000 quota units, and makes
real provider calls. It then deletes the exact test key and user and logs out.
These operations create authentication, usage, and billing records; acceptance
is not read-only. Failed cleanup is reported with any retained test identity.

## Credential interface

Set exactly one credential source:

- `LMM_ACCEPTANCE_CREDENTIAL_FILE`: absolute path to a root-owned regular file
  with mode `0600`; symlinks are rejected.
- `LMM_ACCEPTANCE_CREDENTIAL_FD`: inherited, already-open descriptor above 2,
  referring to a root-owned mode-0600 regular file.

The credential content is JSON:

```json
{
  "username": "root-user",
  "password": "current-password",
  "totp_code": "current-code",
  "completion_model": "known-safe-chat-completion-model"
}
```

`totp_code` is required only when the root account requires 2FA.
An optional `turnstile_token` supplies the challenge proof when login requires it.
`completion_model` is required and must name a deliberately selected model
known to support OpenAI-compatible chat completions. The runner verifies that
the exact model is present in the created API key's `/v1/models` response and
fails closed when it is unavailable. It never guesses from the model list.

## Invocation

Both `baseline` and `verify` require the deployment ID, expected backend
revision, frontend release and asset-manifest digest, and integer Unix-second
deadlines. The lowercase SHA-256 digest is computed by `frontendManifestDigest`
over the index at `/` and its referenced asset paths and bytes. The main deadline
must be in the future, with time reserved before
the later cleanup deadline. Use the same bindings for both runs. `verify` also
requires a successful baseline from an absolute root-owned mode-0600 regular
file through `--baseline-file`; this option is rejected in baseline mode.

For example, capture the baseline using the credential file:

```sh
sudo env LMM_ACCEPTANCE_CREDENTIAL_FILE=/etc/lmm-api/acceptance.json \
  node apps/web/scripts/production-acceptance.mjs baseline \
  --deployment-id "$DEPLOYMENT_ID" \
  --backend-revision "$BACKEND_REVISION" \
  --frontend-release "$FRONTEND_RELEASE" \
  --frontend-digest "$FRONTEND_DIGEST" \
  --deadline-epoch "$ACCEPTANCE_DEADLINE_EPOCH" \
  --cleanup-deadline-epoch "$CLEANUP_DEADLINE_EPOCH"
```

Capture the successful JSON output in the protected baseline file. After
deployment, use `verify` with the same options and
`--baseline-file /absolute/path/to/baseline.json`. An inherited descriptor uses
the same invocation options with `LMM_ACCEPTANCE_CREDENTIAL_FD` instead of the
file variable.

Passwords, access tokens, cookies, 2FA codes, channel keys, and the created API
key remain in memory only. They are never accepted as arguments or written to
logs, summaries, or other artifacts.

## Guard contract

Stdout contains exactly one JSON object. `success` is true only when all
required checks and cleanup succeed; the process exits nonzero otherwise.
The `funded_test_user` check is boolean and never exposes quota or balance
values in the summary.
Channel results contain only ID, type, enabled state, and redacted
pass/fail status. Disabled channels are enumerated with `passed: null` and are
not called. Each enabled channel is tested exactly once, serially, through the
backend's bounded `/api/channel/test/:id` real validation route. The bulk test
route is not used, so acceptance cannot trigger automatic channel bans.

All HTTP calls have one hard deadline covering headers and the complete
response body. Bodies are read as a bounded stream and rejected as soon as
they exceed 1 MiB, including chunked responses without `Content-Length`. The
created API key must list OpenAI-compatible models and complete one
non-streaming request using the explicit model with `max_tokens: 1`.
Unsupported channel tests, timeouts, missing cleanup evidence, and an
unavailable explicit completion model are required failures.

Run the offline contract tests with:

```sh
node apps/web/scripts/production-acceptance.test.mjs
```

## Local operator persona suite

`operator-persona-suite.mjs` is a shell-only, read-mostly regression suite for
the A–O user profiles used during iteration. It is intentionally local-only:
the runner rejects every non-loopback URL, requires a marker-owned deployment
workspace, bounds requests and response bodies, and writes a `0600` report
without response text, cookies, tokens, balances, or API keys.

Run it against a local preview or isolated deployment workspace, never against
production:

```sh
PERSONA_REVIEW_URL=http://127.0.0.1:4174 \
PERSONA_DEPLOY_WORKSPACE=/absolute/path/to/marker-owned-workspace \
PERSONA_OUTPUT_DIR=/absolute/path/to/marker-owned-workspace/artifacts/personas \
node apps/web/scripts/operator-persona-suite.mjs
```

For an authenticated L0/L1 check, provide a separate `0600` JSON credential
file through `PERSONA_CREDENTIAL_FILE`. To model genuinely separate users,
set `PERSONA_CREDENTIAL_FILE_A`, `PERSONA_CREDENTIAL_FILE_B`, and so on for
selected persona IDs; each override must be an independent regular file with
mode `0600` or stricter. If no base file is supplied, the first selected
persona override is used for the shared authenticated checks. Optional
`PERSONA_2FA_CODE_A` and `PERSONA_TURNSTILE_TOKEN_A` variables override the
corresponding shared values for that persona. Credentials and tokens never
enter the report.

To exercise the deterministic assistant-cache, intent and profile checks,
additionally set `PERSONA_RUN_ASSISTANT=1`. When a turn is cache-eligible, the
suite requires the repeated answer to return `HIT` with identical response
bytes; turns that invoke live tools are reported as non-cacheable rather than
being cached. The full A–O set runs by default. For a lower-cost focused pass,
set `PERSONA_RUN_IDS=A,D` (comma-separated IDs); every selected persona still
requires the expected deterministic intent and, where applicable, the
security-refusal policy. The report records only whether a persona used an
isolated account and its L0/L1 boundary result.
The suite does not create keys, make payments, publish bounties, or call a
provider; L0 key creation is tested only as a required authorization denial.

Run its offline safety-contract tests with:

```sh
node apps/web/scripts/operator-persona-suite.test.mjs
```
