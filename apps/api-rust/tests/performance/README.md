# Local Go / Rust comparison

This benchmark exercises the **ordinary listeners**, using one freshly created
PostgreSQL 18 database and separate Valkey cache databases. It never connects to
an existing database or production service. Go creates the schema once, then
restarts in schema-verification mode before measurements. Both servers use four
runtime workers and identical session secrets, data, and rate-limit settings.
Both database pools permit 10 open connections with a 30-minute maximum
lifetime: these are the pinned SQLx defaults, explicitly supplied to Go.
This avoids comparing Go's default 1000-connection capacity with Rust's 10.
Both processes write access logs to one redirected output file. Go uses
`--log-dir=` to disable its extra duplicate log file while retaining request
logging. Go text and Rust JSON formatting remain their normal implementations.

Build the optimized binaries and load driver before starting measurements:

```sh
mkdir -p "$HOME/.cache/lmm-backend-comparison"
(cd apps/api-go && go build -trimpath -o "$HOME/.cache/lmm-backend-comparison/go" .)
(cd apps/api-rust && CARGO_INCREMENTAL=0 CARGO_BUILD_JOBS=2 \
  cargo build --locked --release -p lmm-api-rs)
go build -o "$HOME/.cache/lmm-backend-comparison/http-load" \
  apps/api-rust/tests/performance/http_load.go
go build -o "$HOME/.cache/lmm-backend-comparison/relay-provider" \
  apps/api-rust/tests/performance/relay_provider.go

python3 apps/api-rust/tests/performance/run_local_comparison.py \
  --go-binary "$HOME/.cache/lmm-backend-comparison/go" \
  --rust-binary apps/api-rust/target/release/lmm-api-rs \
  --driver "$HOME/.cache/lmm-backend-comparison/http-load" \
  --relay-provider "$HOME/.cache/lmm-backend-comparison/relay-provider" \
  --output-dir "$HOME/.cache/lmm-backend-comparison/results" \
  --requests 10000 --rounds 3 --concurrency 1 8 32
```

If `CARGO_TARGET_DIR` is configured, use its release-binary path instead. The
runner requires local `initdb`, `postgres`, `psql`, `valkey-server`, and
`valkey-cli`. It stops only its own processes and retains logs and redacted
service metadata. Do not compile, update packages, or run another load test
during measurement.

`comparison.json` contains binary SHA-256 hashes, host/CPU affinity and load,
idle RSS/PSS/private memory, successful throughput, p50/p95/p99 latency, backend
CPU time, sampled peak RSS, process-lifetime peak RSS, and swap use. Separate
listener metadata records startup against the initialized schema; initial Go
schema creation is recorded separately. Round order alternates Go/Rust and
Rust/Go. Each concurrency level gets a warmup.

The driver checks the full JSON body for **every response**. A non-200 status,
business failure, redirect, malformed JSON, or changed body makes the run fail.
A path whose Go and Rust response bodies differ is reported as a differential
and is not compared as a performance win. The driver accepts only loopback HTTP
URLs and reuses connections.

With `--relay-provider`, the runner also generates `relay-comparison.json`.
It uses the ordinary authenticated `/v1/chat/completions` handler, equivalent
independent users and API keys, real PostgreSQL wallets/tokens/logs, and a local
deterministic provider. Each response must match Go's full successful response.
After **every** cold request, warmup and measured batch, the runner checks exact
wallet and token debits, user/channel counters, log counts/quotas/token totals, and that
no Rust settlement remains pending. Provider request totals must also match.
The first relay request is recorded separately, including memory before/after
tokenizer initialization. Subsequent rounds record HTTP latency/throughput and
the additional wait until all accounting becomes visible. Use
`--relay-requests 500` to choose requests per measured relay batch.

Current Go initializes the common schema; the runner applies the real Rust
`0014_relay_settlement.sql` and `0018_subscription_amount_snapshots.sql`
extensions before starting both serving processes.
The fixture's model ratio and completion ratio are both 1; each provider reply
has 512 prompt plus 128 completion tokens, requiring exactly 640 quota units.
Each backend uses one owner/key, so concurrent requests contend on one wallet.
This workload does not measure distributed owners or a slow external model.
Both backends must emit at least one completed access event per verified
request, including preflight, warmups, and relay batches; listener metadata
records actual event counts and log sizes in both standalone result files.

Scope and interpretation:

- Default workloads are `/api/livez`, `/api/about`, and `/api/notice`. They do
  **not** establish AI relay, streaming, payment, authenticated-write, or full
  backend parity/performance.
- Go initializes its default tokenizer at startup; Rust loads its two shared
  tokenizer vocabularies on demand. Public-read and idle memory therefore do
  **not** establish warmed AI relay memory savings. Measure first real tokenized
  requests, warmed streams, and concurrent relay load separately.
- RSS is the backend process, excluding PostgreSQL, Valkey, and the load
  generator. PSS and private memory are included when Linux exposes them.
  These values are not total deployment memory.
- RSS is sampled every 10 ms and can miss brief peaks; lifetime `VmHWM` is also
  recorded. Closed-loop latency samples are successful completed requests,
  not an open-loop overload or tail-latency guarantee.
- Startup is one warm-host observation for each binary, not a statistically
  robust cold-boot comparison. The script records the exact scope.
- Inspect request logging before interpreting CPU/throughput differences. In
  the first measured batch Go emitted an access log per request while Rust's
  ordinary boundary did not. That is an implementation gap and additional
  work in the Go measurement, not an isolated language-performance result.

Regression checks:

```sh
python3 -B -m unittest discover -s apps/api-rust/tests/performance -p 'test_*.py' -v
```

The tests deliberately serve failure envelopes, wrong data, redirects, and
malformed JSON to ensure faster incorrect responses cannot inflate the result.
