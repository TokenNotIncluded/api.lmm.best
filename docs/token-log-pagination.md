# Optional token-log pagination

`GET /api/log/token` keeps the existing `success` / `message` / `data` response
with a latest-1000 array when none of `p`, `page_size`, `ps`, or `size` is
present. Other filters do not enable pagination. An explicitly present paging
key, including an empty value, enables a `data` object with `page`, `page_size`,
`total`, and `items` in both Go and Rust.

The page is at least one. Page size uses the first positive, successfully parsed
value among `page_size`, `ps`, and `size`, defaults to 10, and is capped at 1000.
Values outside the signed integer range are invalid values and use those
fallbacks. A successfully parsed page whose `page * page_size` would overflow
returns `success: false` before a database query. Other callers of the shared
pagination parser keep their existing default 100-row cap.

Both the exact count and row query use the authenticated token ID. A `token_id`
query parameter cannot change that scope. The count is performed only for an
opted-in page; a failed count prevents the row query, and neither count nor row
failure returns a successful payload. Empty pages have `items: []`. Display IDs
start at the page offset plus one. Existing user-log formatting strips
`admin_info` and `audit_info` and hides channel names; this change does not alter
diagnostic metadata. Go preserves ClickHouse's `created_at DESC, request_id DESC`
order and other databases' `id DESC`; Rust PostgreSQL uses `id DESC`.

The 1000-row page bound matches the existing legacy read bound. It limits the
response, not the cost of an exact count or a deep offset. The small regression
fixture is not a production history-size benchmark.

## Upstream eligibility evidence

On 2026-10-03, [upstream PR #7523](https://github.com/QuantumNous/new-api/pull/7523)
remained open at `7f954ef0a29f2ea9b3c4fd24ca0525da9e0e906b`. Its
[backend test job](https://github.com/QuantumNous/new-api/actions/runs/35684382382/job/106607984437)
failed; vet and build passed. This is not evidence that the upstream PR is
stable. [LMM issue #455](https://github.com/TokenNotIncluded/api.lmm.best/issues/455)
also permits proceeding when that failure is shown unrelated.

The failing test was `TestSecurityAccountDeletionConcurrentRequestsHaveOneWinner`
with SQLite `SQLITE_BUSY`, expecting one successful deletion and receiving zero.
The identical failure already occurred in the
[2026-09-21 job](https://github.com/QuantumNous/new-api/actions/runs/35622122306/job/106407463016),
whose actual checkout was `7d2f1ff94c32bfc0f0ece51d30fe34f4a294ba87`, before
the pagination change. The pagination PR's exact parent
`9310231b3c27fea933e939b46cf26e0ce67192e3` also has a
[successful backend run](https://github.com/QuantumNous/new-api/actions/runs/35623528641/job/106412144205).
GitHub source-tree blobs for the deletion test, enrollment/audit database
fixtures, account controller, auth flow/session/user models, verification
service, database-type helper, CI workflow, makefile, and Go dependencies are
identical across those historical, parent, and pagination-head checkouts.

The upstream pagination test is serial, starts no goroutines, and changes only
the log database pointer and log dialect. Its cleanup restores both before
dropping its log table and closing its pool. The deletion test independently
creates main and audit SQLite files under its own temporary directories and
sets both database pointers and dialects. `DeleteSelf` / scoped proof consumption
/ `DeleteUserForSession` do not call either paging parser or token-log query.
These facts identify the failed account-deletion concurrency test as an existing
unrelated failure. The implementation here is native to the two LMM runtimes;
the upstream patch was not copied.

## Regression checks

Go contract tests cover legacy arrays, all opt-in keys, empty values, alias
precedence, page cap, first/second/deep/empty pages, token count/read isolation,
redaction, count/read failures, and parse/offset overflow. The same fixture runs
against isolated PostgreSQL in the mandatory server qualification job. A SQLite
execution of Go's ClickHouse ordering path reverses IDs and ties timestamps to
verify request-ID ordering; it does not claim a real ClickHouse-server run.

Focused Go checks:

```sh
GOMAXPROCS=2 go test -race -p 1 ./common ./controller ./model \
  -run '^(TestGetPageQuery|TestTokenLog|TestFormatUserLogs|TestClickHouseLogOrder)' \
  -count=1 -timeout=180s
```

Set `TEST_POSTGRES_DSN` to a disposable test database and
`TEST_POSTGRES_ISOLATED_SCHEMA=1` to run the PostgreSQL case. Each run creates a
unique schema, restores the global log database/dialect, closes its pool, and
drops its schema. Do not point the fixture at a production database.

Rust unit checks are selected by `token_log`. Its four PostgreSQL HTTP contract
tests are ignored in a normal unit run and explicitly selected in the required
Rust real-integration CI job. They cover the same paging boundaries against the
production store and real token authorizer, retain self/admin default caps, and
exercise row-projection and count failures. Run them with a disposable database:

```sh
cargo test --locked --manifest-path apps/api-rust/Cargo.toml -p lmm-api-rs \
  --lib token_log
LMM_TEST_DATABASE_URL="$TEST_POSTGRES_DSN" cargo test --locked \
  --manifest-path apps/api-rust/Cargo.toml -p lmm-api-rs \
  --test observability_token_log_pagination -- --ignored --test-threads=1
```

Local Rust compilation is delegated to the central Rust test owner together with
the final combined patch. Writing a fixture and checking its formatting does not
constitute a passing Rust compilation or PostgreSQL test result.
