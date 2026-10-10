# MK-05 verification record

Date: 2026-10-10 (UTC). Draft PR: [#696](https://github.com/TokenNotIncluded/api.lmm.best/pull/696).
Parent: [#675](https://github.com/TokenNotIncluded/api.lmm.best/pull/675).
Work branch: `wip/mk-05-relay`. Target: `wip/rust-core-go-extensions`.
Initial base: `b83723cdac3f148140806b8025cb5360a2f6a26d`.

## Evidence inspected before this record

Implementation and tests: `6c2a39afd0c17cc0ee8bcc32185c4a1ef5f0483f`.
Successful run: [38022308784](https://github.com/TokenNotIncluded/api.lmm.best/actions/runs/38022308784).
Evidence: [artifact 11658308770](https://github.com/TokenNotIncluded/api.lmm.best/actions/runs/38022308784/artifacts/11658308770),
`mk05-relay-38022308784.zip`, SHA-256:
`d1974ee2ffc4411be98f7a3f93d8920ecb9a6a224b2b7dbbb32174a8bbf71a60`.

The run was triggered by `33901931e50599b612d76815ffe3cdf95c46a6ed`.
During development, its branch-only normalization step applied reviewed source
corrections and committed the tested tree. The artifact's
`mk05-tested-commit.txt` records the actual tested commit above; do not confuse
it with the trigger commit. The temporary source-editing script was removed.

The workflow accompanying this record is read-only. It has no source-editing,
format-fix, commit, push or repository-write permission. It checks both branch
pushes and pull requests. Its evidence records the exact checkout commit and
tool versions. Use the PR checks and their artifacts for results after this
record; this document does not assert a future run has already passed.

## Observed results

`cargo fmt --all --check`: passed.
`cargo clippy --locked --all-targets -- -D warnings`: passed.
`cargo test --locked --all-targets -- --nocapture`: 69 passed, 0 failed,
0 ignored. Breakdown: 36 library unit tests, 4 schema tests, 2 identity HTTP
PostgreSQL tests, 7 identity PostgreSQL tests, 20 relay HTTP tests. The 69
includes existing core tests; this task adds 17 relay unit tests and 20 relay
HTTP tests. Binary targets without tests report zero tests.

`python3 -B scripts/test-core-boundaries.py`: 8 passed.
The Go extension executable was built from this checkout with `-mod=readonly`.
The Rust toolchain was 1.99.0; Go was 1.27.2. Tests ran in GitHub Actions on
Linux with an isolated PostgreSQL service. They were not run in the assistant's
local environment, which lacks those build tools.

The protocol matrix contains 16 cases inside one of the 20 HTTP tests:
4 upstream protocols x 2 client formats x JSON/SSE. It is not 16 additional
Rust test functions. The upstream protocols are OpenAI Chat, OpenAI Responses,
Anthropic Messages and Gemini GenerateContent. Clients are OpenAI Chat and
Responses. All matrix cases printed a passing record.

## What the real HTTP tests verify

The fixture uses loopback TCP sockets and chunked HTTP, not just JSON decoder
unit tests. Three-byte upstream chunks split UTF-8 and SSE boundaries. The
matrix checks text, reasoning text, tool IDs, split function arguments,
normalized usage, finish reasons, model alias preservation and upstream auth.
Separate tests read an actual Axum HTTP response before upstream completion.

Disconnect/cancel tests verify upstream closure, release of admission slots,
and one billing finalization after an accepted reservation. Other tests cover
reserve/finalize timeouts, header/read/total deadlines, refused connections,
HTTP 302/401/429/500/503, malformed or truncated streams, invalid UTF-8,
no retries after ambiguous work, slow-client backpressure, queue/chunk limits,
SSE event bounds, output/tool/body caps and incomplete function JSON.

Billing denial sends no upstream request. A failed or timed-out finalizer
withholds success markers and returns `settlement_pending`. Missing usage
stays unknown. Invalid cumulative counters do not replace the last valid
snapshot. No upstream error body or credential is exposed to the client.

Regression tests also check Gemini signatures on ordinary text, separate
content/thinking metadata, unsupported encrypted/annotated Responses output,
rejected ambiguous input, sparse oversized tool indexes, and error packets
obeying the same chunk cap as model output.

## Go process independence result

The lifecycle test builds and starts the actual `lmm-extensions` binary. It
receives a first Rust HTTP output chunk, kills the Go process, then permits the
mock upstream to send another chunk. The Rust client receives later output,
`[DONE]`, and a single finalization with input/output usage 2/3.

The inspected run logged:

```text
actual Go extension pid=3699 killed after first HTTP output; Rust emitted later output and settled once; no Rust restart was performed
```

This proves independence from that Go process in this test configuration.
It does not test restarting Rust, restarting a container, changing a proxy or
a load balancer, or terminating the host. It does not guarantee uninterrupted
tokens during a single-instance restart.

## Limits of this evidence and remaining integration

No live provider account or paid API call was used. Upstreams are simulated
HTTP servers. Routing and billing use test adapters. The tests do not certify
complete compatibility with all provider features or SDK versions.

The byte, queue, block and concurrency limits are exercised. This is not an
RSS or allocator-peak benchmark. TLS, HTTP, JSON trees, escaped output copies,
Responses terminal snapshots and kernel buffers use additional bounded or
implementation-managed memory. Tune limits only after a deployment load test.

No public model route is connected. Existing model endpoints and business
readiness remain 503. No production billing, payer selection, budget,
subscription, durable reconciliation or active route store is supplied here.
Tasks 03/04 must implement the documented `Billing` and `RouteProvider`
contracts and pass paid-request integration tests before exposing an endpoint.
A reservation timeout may have committed; recovery must use durable state,
not assume zero cost. The relay itself holds no SQL transaction over a stream.

Unsupported features and provider-specific metadata rules are in [README.md](README.md).
The PR remains Draft. No merge, production deployment or old-data migration
is part of this task.

## Reproduce

From the repository root, using the exact tested checkout:

```sh
export GOWORK=off
(cd apps/lmm-extensions && go build -mod=readonly -o /tmp/lmm-relay-extension ./cmd/extensions)
export LMM_RELAY_EXTENSION_BIN=/tmp/lmm-relay-extension
cargo +1.99.0 fmt --manifest-path apps/lmm-core/Cargo.toml --all --check
cargo +1.99.0 clippy --manifest-path apps/lmm-core/Cargo.toml --locked --all-targets -- -D warnings
cargo +1.99.0 test --manifest-path apps/lmm-core/Cargo.toml --locked --test relay_http -- --nocapture
# Set DATABASE_URL to an isolated PostgreSQL instance, never production.
cargo +1.99.0 test --manifest-path apps/lmm-core/Cargo.toml --locked --all-targets -- --nocapture
python3 -B scripts/test-core-boundaries.py
git diff --exit-code
```

The real Go lifecycle test fails instead of silently skipping when
`LMM_RELAY_EXTENSION_BIN` is missing. CI artifacts include the source snapshot,
exact tested commit, strict-check log, full test log and boundary-check log.
