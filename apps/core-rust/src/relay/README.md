# Rust relay (MK-05)

Parent: #675. Work branch: `wip/mk-05-relay`. Base at start:
`b83723cdac3f148140806b8025cb5360a2f6a26d`.

This is an internal relay library, not an enabled public model API. Existing
HTTP model endpoints and business readiness remain unavailable. No production
billing adapter or task-04 routing adapter is supplied in this task. Do not
open paid endpoints until those core-owned adapters pass integration tests.

## Supported surface

Clients: OpenAI Chat Completions and Responses. Upstreams: OpenAI Chat,
OpenAI Responses, Anthropic Messages, and Gemini GenerateContent. Each pair
supports JSON and SSE, one candidate, text, function tools and tool results.
The HTTP tests exercise all 16 provider/client/output-mode combinations.

The route selects the complete upstream operation URL and real model name.
Gemini streaming needs `:streamGenerateContent?alt=sse`; JSON needs
`:generateContent`. The client still sees its requested model alias.

The bounded internal block format keeps function call IDs separate from
Responses output-item IDs. It keeps tool argument fragments, thinking text,
provider signatures, usage and finish reasons separate. Responses event
sequence numbers are increasing. Length-limited tool JSON remains incomplete;
a normal tool completion with invalid JSON is an error. An HTTP EOF without a
required semantic end is an error, never an invented success.

Anthropic cache-read and cache-creation tokens are added to its uncached input
count. Gemini thoughts are included in normalized output usage. Missing usage
stays `None`, not zero. Counters cannot decrease or overflow; invalid counters leave the last accepted snapshot unchanged. Thinking text
uses `reasoning_content` for Chat and reasoning-summary items for Responses.
Plain-text signatures use the Chat `content_details` extension or Responses item metadata. These details are preserved on output; replay of signed plain-text history is not yet supported.
Opaque signatures remain provider-tagged metadata, not fabricated OpenAI
reasoning tokens. They cannot be replayed to a different provider.

This is deliberately not a universal lossless bridge. Images, audio, video,
multiple candidates, hosted/server tools, encrypted or stateful Responses
history, unknown request fields, annotated Responses output, unsupported provider content blocks and
unsupported finish reasons fail explicitly. Anthropic redacted-thinking blocks
are not supported. Chat streams that fragment the tool name/ID before the
initial complete identity are not supported. Multiple signed thinking blocks
are returned as provider-tagged details; replay of those details is not yet a
supported input. Provider-specific features must not be silently discarded.

## Integration contract for tasks 03 and 04

`RouteProvider::select` receives the verified actor/key context and normalized
request. Return an immutable Rust-owned `Route`, including its price version,
complete URL, credential and model. Do not query or wait for Go in this method.
Route origins must come from trusted active configuration, not request input.
HTTPS is mandatory except for explicit numeric loopback upstreams. Redirects,
URL credentials, credential query parameters and ambient proxy settings are
not used. Upstream error bodies and credentials are not returned to clients.

`Billing::reserve` must authorize the actor/key, payer, team restrictions,
budget and subscription policy, then commit a durable reservation. It must be
idempotent by `RequestContext.request_id`. Return only a reservation handle,
not a SQL connection or transaction. The relay sends no model request until
this hook succeeds. The relay does not import SQLx or a Go extension client.

`Billing::finalize` runs exactly once in each live worker after a reservation
handle was returned, including cancellation, timeout and upstream failure.
The adapter must make the durable operation idempotent across retries/restarts.
It must persist a final charge or a durable reconciliation obligation. Missing
usage, an ambiguous send, failed finalization and a reservation timeout are
not evidence of zero cost. A cancelled reserve future may already have
committed: recover by request ID. The billing implementation must own a
persistent sweeper/recovery queue; an in-process callback is not sufficient.

The client may receive streaming deltas before settlement. Successful terminal
markers (`[DONE]`, `response.completed`, `response.incomplete`) and a JSON 200
response are withheld until finalization confirms persistence. Finalization
failure is a `settlement_pending` error. A report's outcome describes upstream
work; a later client delivery failure is reported separately in Completion.

Integration sketch (not a public route):

```rust,ignore
let request = Request::parse(ClientProtocol::Chat, body, &limits)?;
let session = relay.start(request, verified_context)?;
let (response, completion) = session.into_http().await?;
// Observe Completion and durable reconciliation state outside the HTTP body.
// Route registration and authentication belong to the final integration task.
```

## Resource and shutdown limits

Defaults: 16 admitted requests; 8 queued chunks per request; 16 KiB per normal
output chunk; 2 MiB input body; 256 KiB SSE event; 1 MiB accumulated output;
256 KiB per tool's arguments; 128 output blocks; 64 MiB total upstream bytes.
There is no unbounded event queue. HTTP chunks are consumed byte by byte by the
SSE framer, so a large network chunk does not create an unbounded event list.
Both source and JSON-escaped output have explicit limits. JSON escaping and
Responses final snapshots require extra bounded copies; these figures are
not an RSS guarantee. Allocator, TLS, HTTP, JSON tree and kernel socket buffers
are additional. Increase concurrency/output caps only after a memory load test.

Defaults: 5 s connect; 30 s headers; 30 s idle read; 10 s slow-client write;
300 s forwarding deadline; 10 s per billing/routing hook. Bounded money cleanup can continue after the forwarding deadline. Client-body drop cancels reading,
writing and the upstream request. The reservation and finalizer have their own
short deadline and are not aborted just because the client disconnected.

Retry defaults to zero. At most two explicit retries may be configured, only
for connect failures. HTTP statuses, redirects, read failures, partial output,
header timeouts and ambiguous POST failures are not replayed. Reqwest's own
retry policy is disabled. A retry does not reset the total request deadline.

`Relay::drain(grace)` closes admission atomically, waits for current requests,
then cancels remaining upstreams and waits for bounded hooks. It reports both
cancelled and remaining workers. The host must inspect `remaining`; do not
claim success while work remains. Drop upstream bodies after terminal events
instead of consuming arbitrary trailing bytes to reuse a connection. Idle
connection pooling is disabled to avoid retaining connections for an unbounded
set of configured origins; active connections and HTTP/2 windows are bounded.

Stopping Go does not notify or cancel relay workers. The acceptance test builds
and starts the actual Go extension binary, reads a Rust HTTP stream, kills Go,
then checks later output and one finalization. This is not a Rust/container
restart test. A single Rust instance restart can break a stream. Zero lost
tokens across restarts requires deployment-level draining and multiple healthy
instances, and is not guaranteed here.

## Reproduce verification

Use the repository's pinned Rust 1.99.0 and Go 1.27.2 tools. PostgreSQL is needed
only for existing core database tests, not the relay HTTP tests. No provider
account, production database, API key or paid model call is used by these tests.

```sh
export GOWORK=off
(cd apps/api-go && go build -mod=readonly -o /tmp/lmm-relay-extension ./cmd/extensions)
export LMM_RELAY_EXTENSION_BIN=/tmp/lmm-relay-extension
cargo +1.99.0 fmt --manifest-path apps/core-rust/Cargo.toml --all --check
cargo +1.99.0 clippy --manifest-path apps/core-rust/Cargo.toml --locked --all-targets -- -D warnings
cargo +1.99.0 test --manifest-path apps/core-rust/Cargo.toml --locked --test relay_http -- --nocapture
# Set DATABASE_URL to an isolated test PostgreSQL instance before all-targets.
cargo +1.99.0 test --manifest-path apps/core-rust/Cargo.toml --locked --all-targets -- --nocapture
python3 -B scripts/test-core-boundaries.py
```

The real Go process test fails, rather than silently skipping, when its binary
is not supplied. The CI evidence artifact includes the tested commit, strict
checks, test output, boundary checks and task-source snapshot. The PR records
which exact run has passed. Source submission alone is not a passing result.
