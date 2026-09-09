# Go relay memory management

The Go backend retains request data while converting, retrying, and streaming
upstream requests. A request-body size limit is not a process-memory limit:
parsed JSON, provider DTOs, encoding buffers, and concurrent requests add to it.

## What the request path does

- Large bodies can spill into the configured disk cache. The default directory
  is `new-api-body-cache` under Go's temporary directory (`TMPDIR` on Unix).
  If `/tmp` is tmpfs, this is still RAM-backed storage, not ordinary disk.
- Decoded gzip, Brotli, and Zstandard bodies have unknown decoded length. The
  original compressed `Content-Length` must not select a small in-memory buffer
  or bypass large-request admission. Both compressed and decoded byte ceilings
  remain enforced. Zstandard uses lazy, single-stream decoding instead of eager
  asynchronous prefetch. Decoder resources are closed even on early rejection;
  the HTTP server retains ownership of the original wire body, so rejecting a
  request does not wait for an unread slow sender to finish uploading.
- Deep-copying `json.RawMessage` and `[]byte` fields uses a bulk clone instead
  of reflecting/allocating once per byte. This applies to converted requests
  too, without sharing mutable byte arrays with the original DTO.
- Responses and Chat Completions passthrough isolate top-level request metadata
  without deep-copying payloads that will be replayed verbatim. Converted paths
  retain deep copies so provider mutations cannot corrupt retries.
- Closing memory storage drops its owned buffer references. Already-open replay
  readers remain independent and usable; opening new readers after close fails.
  Legacy byte caches are cleared when ownership transfers to storage or cleanup
  runs. This makes data collectible; it does not force the Go GC or OS reclaim.

JSON parsing and encoding can still allocate complete values. Spilling a body
is not a claim of constant-memory JSON processing, and passthrough improvements
do not apply to routes that must convert or override the JSON payload.

## Optional large-request admission

These settings are read once when the Go router is created; restart to change
them. They are **per process**, shared across authenticated HTTP relay protocols
and playground HTTP requests, not distributed quotas across replicas.

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `RELAY_LARGE_REQUEST_MAX_CONCURRENCY` | `0` | Maximum in-flight large HTTP requests; `0` disables this control. |
| `RELAY_LARGE_REQUEST_THRESHOLD_MB` | `4` | Body size in MiB at or above which a request takes a slot. |

Unknown-length bodies, including decompressed bodies, conservatively take a
slot. Known small bodies, empty bodies, GET, HEAD, and OPTIONS bypass the pool.
WebSocket upgrades/frames and background task execution are not covered. Task
submission HTTP handlers are covered only for their HTTP handler lifetime.

Admission runs after authentication and before body/model parsing. A slot lasts
until the handler returns, including streaming. It is released on normal return,
client cancellation once the handler exits, or panic. There is no waiting queue.
Saturation returns **HTTP 429**, `Retry-After: 1`, and the stable error code
`large_request_concurrency_exceeded` (Google-native clients receive ErrorInfo).
OpenAI, Claude, and native Gemini routes retain their respective error envelopes.
Clients should honor Retry-After and apply bounded backoff/jitter, not tight
retry loops. Long streams and slow uploads can occupy slots for a long time;
this shared pool is not a per-user fairness policy. Keep appropriate client,
proxy, and upstream timeouts. Existing 413 byte limits, model rate limits,
quota checks, and the system-performance 503 guard remain separate controls.

The default is off so an upgrade does not silently change admission on existing
instances. Invalid/negative concurrency falls back to disabled with a startup
warning; invalid, nonpositive, or overflowing thresholds fall back to 4 MiB.

For a small instance, an initial opt-in configuration is:

```dotenv
RELAY_LARGE_REQUEST_MAX_CONCURRENCY=2
RELAY_LARGE_REQUEST_THRESHOLD_MB=4
```

This prevents multiple large request-processing pipelines from entering at
once; it is not a byte-precise cap on total heap/RSS, nor a promise that any two
requests will fit. One unusually large request can still exceed the available
budget. Keep per-request and cgroup limits, then tune using actual traffic.

## Small-memory systemd deployments

For a service with `PrivateTmp=yes`, verify the filesystem in the **service's
mount namespace**, not just the host's `/tmp`. If `/var/tmp` is a writable,
disk-backed private mount, a service drop-in can set:

```ini
[Service]
Environment="TMPDIR=/var/tmp"
Environment="GOMEMLIMIT=192MiB"
```

Check that no later EnvironmentFile overrides these settings. The example Go
budget is a starting point for a roughly 1 GiB host, not a universal default.
Lowering it can increase GC/CPU and latency. Keep RAM for other services and for
non-Go allocations and page cache; a 256 MiB Go budget does not imply 256 MiB RSS.
Disk-backed file cache can still count toward the cgroup's memory usage, although
it is more reclaimable than tmpfs data. Do not disable the existing memory guard
or blindly raise its threshold to hide an overload.

Verify under representative traffic after the restart:

- effective `TMPDIR` and `GOMEMLIMIT` in `/proc/<pid>/environ`;
- actual cache file paths/filesystem, without reading request contents;
- cgroup `memory.current`, `memory.peak`, `memory.events`, swap and memory PSI;
- host `MemAvailable`, CPU/GC cost, request latency, and actual relay responses;
- 429 admission rejections separately from local memory 503s and upstream 503s.

A status endpoint returning 200 alone does not validate upstream relay traffic.
Do not delete live body-cache files, flush Valkey data, or run periodic forced
restarts as a substitute for memory budgeting. Remove only your own drop-in and
restart to roll back deployment tuning; setting concurrency back to `0` disables
admission on the next restart. These source changes do not deploy themselves.

## Tests and allocation benchmarks

From `apps/api-go`:

```bash
go test ./pkg/admission ./common ./middleware ./relay ./relay/common ./relay/channel ./router
go test -race ./pkg/admission # on a supported race-detector platform
GOMAXPROCS=2 go test -run '^$' -bench '^BenchmarkCopyRequestForRelay$' \
  -benchmem -benchtime=3x ./relay
go test -run '^$' -bench '^BenchmarkLargeRequestAdmission$' -benchmem ./pkg/admission
```

The copy benchmark compares the legacy reflection-based copy, optimized deep
copy, and passthrough for identical DTOs, including a 28 MiB Responses payload.
Report allocation savings for that operation, not as a promised percentage
reduction in total server RAM.
The admission benchmark includes disabled and small-request fast paths. Use
representative concurrency/load tests and supported Linux CI in addition to
local allocation benchmarks before releasing.

### Local allocation sample

Measured with Go 1.27.1 on Android/arm64, `GOMAXPROCS=2`, `GOMEMLIMIT=768MiB`,
`-benchtime=3x -count=3`, using synthetic payloads (not production request data):

| Copy of a 28 MiB Responses input | Bytes allocated/op | Allocations/op |
| --- | ---: | ---: |
| Legacy reflection copy | about 58,722,320 | 29,360,145 |
| Optimized deep copy | 29,362,720 | 16 |
| Passthrough metadata copy | 768 | 1 |

The optimized deep copy still owns an independent 28 MiB payload. The savings
are the discarded per-byte reflection allocations, not a removal of retry
isolation. Passthrough shares only data that the handler will not mutate.
Converter lookup has a small fixed cost: in the empty-payload DTO benchmark it
added 640 bytes and one allocation/op compared with the legacy copier. These
are operation-level allocation results, **not** total process RSS or end-to-end
throughput guarantees. Native Linux CI/load testing remains a release gate.


## In-process byte-cache allocations

`pkg/cachex.ByteCache` grows its map as keys are inserted, instead of reserving
space for the configured entry limit at construction and after `Purge`.
Replacing an existing value reuses its entry and LRU node. This reduces idle
allocation for notification/verification caches and the in-memory rate limiter,
and removes cache-node churn when request counters are updated. Insertion,
replacement, and eviction share one implementation under the existing mutex.

Entry limits, weight accounting, LRU order, TTL refresh/reset, and atomic
`Compute` semantics are preserved. An oversized `SetWithTTL` still leaves an
existing value unchanged; an oversized `Compute` result still removes it.
Expiry remains lazy. Purging drops the map and entries without forcing GC.
The configured byte budget counts caller-supplied weights, not map/list overhead
or total process RSS; a filled map can retain capacity until a purge.

Synthetic Linux/amd64 allocation measurements, Go 1.25.1, three runs per case:

| Operation | Before (bytes/op) | After (bytes/op) |
| --- | ---: | ---: |
| Create empty cache, 256-entry limit | 13,784 | 176 |
| Create empty cache, 16,384-entry limit | about 873,912 | 176 |
| Create empty cache, 65,536-entry limit | about 3,495,223 | 176 |
| Replace existing integer, `SetWithTTL` | 112 (2 allocations) | 0 (0 allocations) |
| Increment existing integer, `Compute` | 112 (2 allocations) | 0 (0 allocations) |
| Insert one integer then purge, 65,536-entry limit | about 3,495,206 | 368 |
| Insert/evict distinct string keys, 256-entry limit | 119 | 119 |

These measure allocation volume for the named operations, not retained heap or
whole-server savings. New keys still allocate nodes, and growing the index on
demand shifts map growth work to insertion. User callbacks and values may also
allocate; zero-allocation updates here use integer values and a simple callback.
No production throughput or latency claim is made from these microbenchmarks.

Reproduce from `apps/api-go`:

```bash
go test -race ./pkg/cachex
go test -run '^$' -bench '^BenchmarkByteCache' -benchmem -count=3 ./pkg/cachex
```
