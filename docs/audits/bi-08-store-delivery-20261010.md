# BI-08: store delivery verification, 2026-10-10

Status: **database acceptance blocked; not ready to merge**.

Related audit: #711. Audit baseline: `main@565b64e4`.
This work is based on current main
`2f4164978cf27f6e0e605b0589fbe631aef2c441`, not the independent #675 modules.

## Changes and limits

The change adds tests only. No store, refund, payment, authentication, or frontend
implementation is changed. No business defect has been reproduced in this run;
there is therefore no speculative repair commit. #704's claim login-return
implementation is retained.

`apps/api-go/model/merchant_store_bi08_postgres_test.go` uses main's existing
`merchantStorePGDB` and `merchantStorePGContend` helpers. The former uses an
explicit literal-loopback PostgreSQL URL, creates a unique test schema, uses a
16-connection pool, and removes that schema. The latter observes real database
lock contention. These are not substitutes for main's order transactions.

The added cases cover:

1. Two sellers, three buyers, and one remaining inventory item. Competing order
   requests must have one winner. Replay and denied cross-account operations
   must not change selected order, wallet, stock, transfer, or delivery facts.
2. A failure at the final settlement-event write must roll back the entire
   settlement. The same receipt can then be retried. A lost acknowledgement must
   not create another receipt or transfer. A test-owned ciphertext fault tests
   paid delivery failure and subsequent rereading of the same repaired item.
3. Unpaid, cancelled, and expired orders cannot claim content. Physical inventory
   and sales quota are checked separately, including cancellation and restocking.
4. Static content supports concurrent and repeated authorized reading. Existing
   refund interfaces are used to check remaining access after a quantity refund
   and denial after a full refund. No refund implementation is changed.

The standalone Node tests import the actual `signInHref` and
`sanitizeAuthRedirect` implementation. They cover local claim addresses,
queries, fragments, reauthentication, retry, and avoiding nested sign-in URLs.
They do not execute the browser, router validation, cookies, or an OAuth flow.
They are not a full security test of the redirect helper; task BI-02 owns that.

No payment adapter, application server, email worker, or notification sender is
started by the new cases. Receipts and ciphertext faults are synthetic. The
model settlement primitive is called directly, so signature verification,
provider transport failures, and real provider reconciliation are not covered.

## Actual execution evidence

The local workspace contains selected files retrieved through the repository
connector. It is **not a complete checkout**.

| Command or check | Observed result |
| --- | --- |
| `git clone --depth 1 --single-branch --branch main https://github.com/TokenNotIncluded/api.lmm.best.git /mnt/data/api.lmm.best` | Failed: `Could not resolve host: github.com`. |
| `go version` | `go version go1.23.2 linux/amd64`; main's `go.mod` requires Go 1.25.1. |
| `command -v postgres`, `command -v initdb`, `command -v psql`, `command -v docker` | No executable found for any of these commands. |
| `node --version` | `v22.16.0`. |
| `gofmt -d apps/api-go/model/merchant_store_bi08_postgres_test.go` | Empty output, zero-byte diff. Parsing/formatting only; not compilation. |
| `node --experimental-strip-types --test scripts/business-audit/store-claim-return.test.mjs` | 2 tests passed, 0 failed, 0 skipped. |
| `git hash-object apps/web/src/features/auth/lib/auth-redirect.ts` | `24b8d5ba9e1fba8fd6b5a2d6004cb4fb351c7364`, equal to the source blob retrieved from the base commit. |
| Go package compilation, `go test`, and PostgreSQL cases | **Not run.** Complete source, dependencies, the required Go version, and PostgreSQL are unavailable. |
| Repository documentation checks and full frontend/application checks | **Not run.** Complete checkout and dependencies are unavailable. |

No database instance, schema, seller, buyer, order, or payment was created during
this run. **Actual before/after database states are unavailable.** Assertions
written in a test file are not observed database results.

## Expected states in the unexecuted database assertions

These values are integer wallet quota, not measurements from a running database.

| Case | Before | Expected after |
| --- | --- | --- |
| Last inventory item | Each buyer and seller has 10,000,000; root has 0; one unit in each seller's product | Exactly one buyer has 9,500,000; seller one has 10,495,000; root has 5,000; seller two is unchanged. One paid order, one delivered unit, and two order transfers. Other seller's inventory remains available. |
| Final settlement write failure | Pending order with a held fee; seller has 9,995,000; root has 0; stock is reserved | After failure, the selected database state is identical. After a successful retry: paid order, seller 10,495,000, root 5,000, one receipt, three transfers including the original fee hold. Further receipt replay changes nothing. |
| Cancellation and expiry | Two physical units, sales limit one; an unpaid reservation consumes one quota | Cancelled/expired orders cannot claim; stock returns to two units, seller returns to 10,000,000. After a later paid sale and restock: two physical units remain, but available sales quota is zero. |
| Static content and refunds | Buyer and seller each have 10,000,000; no inventory-backed delivery | After purchase, one order-bound static-content row remains readable repeatedly. Partial refund leaves one purchased unit. Full refund denies future claims; buyer has 10,000,000, seller 9,990,000, root 10,000. No stock or stock-refund item is manufactured. |

The tests emit `BI08_DB_STATE` records containing selected synthetic accounting
and fulfillment fields, not whole production tables. Tokens, codes, email
addresses, gateway snapshots, DSNs, and delivery plaintext are excluded.
Content is represented only by a ciphertext digest. No such records were emitted
in this run because the database tests did not execute.

## Commands for local database validation

Use a complete checkout of the task branch, Go 1.25.1 or newer with the module's
pinned dependencies, and an explicitly disposable local PostgreSQL database.
Set `MERCHANT_STORE_POSTGRES_TEST_DSN` privately to that database. Do not use the
production connection string or start the application or its workers. No remote
CI is needed. The existing harness rejects non-loopback endpoints.

From the repository root:

```bash
node --experimental-strip-types --test scripts/business-audit/store-claim-return.test.mjs

gofmt -d apps/api-go/model/merchant_store_bi08_postgres_test.go

: "${MERCHANT_STORE_POSTGRES_TEST_DSN:?An explicitly disposable local PostgreSQL URL is required}"
result_dir=$(mktemp -d)
result_file="$result_dir/store-delivery.jsonl"
status=0
(
  cd apps/api-go
  GOTOOLCHAIN=local go test -race -count=1 -timeout=10m -json ./model \
    -run '^(TestMerchantStoreBI08Postgres|TestMerchantStorePostgres(DSNGuard|Concurrency|Variants|SalesLimit))$'
) >"$result_file" 2>&1 || status=$?
cat "$result_file"
test "$status" -eq 0 || exit "$status"
python3 - "$result_file" <<'PY'
import json
import sys

required = {
    'TestMerchantStoreBI08Postgres',
    'TestMerchantStorePostgresDSNGuard',
    'TestMerchantStorePostgresConcurrency',
    'TestMerchantStorePostgresVariants',
    'TestMerchantStorePostgresSalesLimit',
}
passed, bad = set(), []
with open(sys.argv[1], encoding='utf-8') as stream:
    for line in stream:
        event = json.loads(line)
        if event.get('Action') in {'fail', 'skip'}:
            bad.append((event.get('Test', '<package>'), event['Action']))
        if event.get('Action') == 'pass' and event.get('Test'):
            passed.add(event['Test'])
if bad or not required <= passed:
    raise SystemExit(f'NOT ACCEPTED: bad={bad}; missing={sorted(required - passed)}')
print('Selected database test parents passed without skipped cases.')
PY
```

A skipped PostgreSQL case is not acceptance. The command includes existing main
cases for same-key concurrency and variant/sales-limit behavior, as well as the
new multi-buyer and delivery cases. It does not qualify HTTP/browser or provider
behavior beyond the tested model entry points.

## Still unverified

All new Go tests remain uncompiled and unexecuted. All requested database and
multi-connection results remain unverified, including the existing main tests
listed above. There is no evidence of a successful database acceptance run.

Real HTTP/controller seller isolation; authenticated browser account switching;
OAuth/2FA/login return; session and claim-credential expiry; actual provider
signature checks and transport-level lost receipts; concurrent refund versus
claim; and inventory-backed partial-refund delivery still need end-to-end
execution. Default-variant facts are asserted by the new cases; the broader
existing variant suite has not been run here.

`MerchantStoreOrder.ExpiresAt` is the checkout deadline. The new test deliberately
does not turn it into a new expiry for already paid content. Claim-specific
credential or session expiry is a separate, unverified boundary.

No production, real funds, notifications, CI dispatch, release, deployment, or
merge is part of this work. Keep the PR in draft until actual database evidence
and the remaining required boundary tests are available.
