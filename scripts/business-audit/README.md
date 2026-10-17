# Local business-integrity audit — 2026-10-10

Source baseline: `main@565b64e475419e073f55225e64d0718f7bf070b4`.
Test-only change. No business repair, CI dispatch, release, production access,
real payment or real notification. The separate microkernel PR #675 is not main.

## Reproduced defects

- `AUDIT-PRICE-01`: `Quote.rates` ignores an accepted fast long-context tariff
  when `standard.long` is absent. The synthetic fixture charges 6.52802400 USD
  instead of 13.05604800 USD. These are configured test prices, not a current
  provider tariff or evidence of actual production losses.
- `AUDIT-SSE-01`: the assistant frontend dispatches an unfinished terminal event
  at EOF. A terminal event without the final empty line resolves as success.
- `AUDIT-SUB-01`: changing only an existing subscription's `amount_used` stops
  confirmation of a different pending checkout. This is premature polling
  termination, not proof of unauthorized credit.

An authentication-related security reproducer is retained in the private
handoff, not in this public change. No advisory or email was sent for the user.

## Actual commands and results

From the repository root, using Node 22.16.0:

```sh
node --experimental-strip-types --test scripts/business-audit/regressions.test.ts
```

Two positive controls pass; the SSE and subscription assertions fail.
This is runtime execution, not TypeScript checking or a React browser test.

For the standard-library-only pricing package, using Go 1.23.2:

```sh
cd apps/api-go/pkg/servicetier
GO111MODULE=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  go test -race -count=1 -run TestBusinessAuditTierLocalLongContextPrice -v .
```

The price assertion fails. No dependency manifest was changed. These tests assert
correct behavior; keep their failures visible until separate fixes pass them.

## Verification limits

A complete current-main checkout and dependencies were unavailable locally.
Selected source came from an earlier `7f09e17d` archive, with the current auth
file read separately. All four finding source files match current-main Git blob
hashes; the account-funding, admission and pricing package trees also match.
This is verified-component execution, not a complete main build.

Existing component suites ran for funding rules, pricing, admission,
registration guard, provider configuration, Extore, OIDC, Decisions, SSE,
auth metadata, referral evidence and assistant preference inputs. Existing
frontend function tests covered assistant lifecycle, checkout intent replay,
amount formatting, session expiry, OAuth input and model availability.
OIDC used an in-memory store; one optional interoperability fixture export was
skipped because no output target was configured.

The full Go app requires >=1.25.1, which was unavailable. PostgreSQL, cache,
Rust/Bun and app dependencies were also unavailable. Chromium loopback navigation
was blocked by administrator policy; no bypass was attempted. Database-backed
billing, payments, refunds, referrals/coupons, orders/delivery, MCP execution,
privileged assistant tools and admin authorization remain **unverified**.
Dependency-loading errors are not business failures. Passing helper tests does
not prove persistent state, funds or end-to-end behavior.

Business fixes must be separate commits/PRs. Do not weaken assertions, alter CI
triggers or deploy this draft. Preserve the main/WIP branch boundary.
