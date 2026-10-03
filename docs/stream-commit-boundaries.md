# SSE commitment and pre-output failover

This is the LMM adaptation of [issue #434](https://github.com/TokenNotIncluded/api.lmm.best/issues/434)
and [upstream #7509](https://github.com/QuantumNous/new-api/pull/7509).
Header commitment is an HTTP attempt boundary, rather than a global check of
`gin.ResponseWriter.Written()`. WebSocket handshakes and resettable
administrator-assistant writers retain their existing retry behavior.

## HTTP policy

- For OpenAI chat streams with an enabled `OPENAI_FIRST_OUTPUT_TIMEOUT`, headers,
  role, usage and heartbeat activity stay uncommitted while no visible
  content/reasoning/tool/function output has arrived. The first visible frame
  commits headers immediately before forwarding the retained prefix and frame
  in order. Timeout before that point remains a retryable real HTTP 504.
- The retained Go prefix is limited to 1 MiB (or the smaller configured response
  limit) and 1024 events, including framing overhead. Exceeding it returns a
  bounded 502 before commitment.
- With the timeout disabled, the shared scanner flushes headers before starting
  its producers only for a successful response with an explicit, exact
  `text/event-stream` media type. Type matching ignores case and accepts valid
  media-type parameters. It rejects lookalike subtypes.
- OpenAI chat, native Responses and their stream converters reject an explicit
  non-SSE response before reading or committing, with a retryable 502 under the
  existing retry policy. Headerless legacy input waits for a business write.
  Image JSON conversion has a separate, fully read/decoded payload path.
- The pre-response global pinger cannot commit a response. It writes only after
  this HTTP attempt has committed. Delayed upstream headers and rejection
  responses therefore retain their actual HTTP status.
- Both a committed attempt and a downstream write/flush failure prohibit
  provider retry. Downstream failures are not upstream channel failures and
  bypass provider error logging/automatic penalties. Failure messages used for
  classification are bounded and do not reflect upstream payloads.
- The controller resets the explicit commitment and downstream-failure flags
  before each HTTP attempt. Internal resettable writers are excluded from those
  flags; writer identity and reset behavior remain intact.

Header commitment does not prove successful business-event delivery. Responses
readers track `eventWritten` independently, so an empty EOF keeps the existing
API-error/refund outcome instead of entering successful settlement merely
because headers were flushed. Once committed, the controller suppresses a JSON
error append and retry remains prohibited. With the timeout set to zero, a
validated upstream's later empty failure therefore closes an already-observed
HTTP 200 SSE response; it cannot become a visible HTTP 502/504. This loss of
pre-output HTTP error visibility is the requested early-header semantics. The
enabled OpenAI first-output guard retains its uncommitted 504 boundary.

`CommitEventStreamHeaders` propagates the underlying `FlushError` through Gin's
`Unwrap`, rather than letting `gin.Flush` discard it. A failed initial commit
closes the upstream body/socket before starting the producer. Shared-scanner
failures invoke its existing cancel/stop/body-close/worker-join cleanup; no later
event is written after the downstream failure is latched.

## Reader audit

There is no new pricing, usage estimator or timeout policy in this change.
Dedicated readers use their existing `RateLimitStreamStatus`, keeping admission
classification separate from their legacy usage collection. In particular,
Cohere and Zhipu continue reading terminal usage after an output write fails.
They stop writing to the client immediately, prohibit retry and retain the
existing settlement facts. This is **usage collection continuation**, not a
claim that the upstream was immediately canceled.

The streaming Responses-to-chat converter also preserves usage when a newly
observable downstream flush/write failure occurs. Its failure branch uses the
usage reports already accepted by that converter's response-metadata events,
including an explicit zero report. Without a report it uses only the existing
locally countable, nonempty output text, with zero inferred input tokens.
Headers/role alone settle zero; no historical/prepayment fallback is introduced.
The branch stops before any later finalizer, usage frame or DONE write, marks
delivery failed and releases the success reservation. This specifically avoids
refunding generated output merely because a client flush failed. It does not
expand the converted route's existing `response.error`/`response.failed`
API-error/refund policy for explicit upstream failures, which differs from the
native Responses partial-output settlement policy.

| Reader | Acceptance and commitment | Cleanup/output failure | Existing size/time boundary |
| --- | --- | --- | --- |
| Shared scanner: OpenAI chat/native Responses/converters, Claude, Baidu, Gemini/native Gemini, xAI, Dify, SSE audio, native image SSE | Exact successful SSE headers permit early commit when no first-output guard is active; headerless input waits for business writes. OpenAI chat preserves its enabled first-visible window. | Initial failure precedes producer start; later write/flush failure stops workers and closes body. | One queued event; per-line and decoded-event limits; 30 s downstream write deadline; `STREAMING_TIMEOUT` idle watchdog, default 900 s, reset per upstream line; enabled keepalive has a default 120 min lifetime. Comments can reset idle but cannot satisfy first-visible output. |
| Zhipu | Explicit non-SSE rejected; headerless input waits for a parsed business write. | Initial failure precedes producer; later failure latches writes off while retaining meta usage; request cancellation exits/closes body. | Per-line scanner limit; no reader-owned aggregate byte, idle or total deadline. |
| Tencent | Same explicit SSE gate. | Initial failure precedes Scan; later failure stops all writes, including DONE, while retaining existing text collection; deferred body close. | Per-line limit; no reader-owned aggregate byte, idle or total deadline. |
| Cloudflare | Same explicit SSE gate. | Initial failure precedes Scan; later failure stops client writes and retains existing read-to-DONE/EOF behavior; deferred body close. | Per-line limit; no reader-owned aggregate byte, idle or total deadline. |
| Coze | Same explicit SSE gate. | Initial failure precedes Scan; event write failure returns and closes body under existing usage rules. | Per-line limit and retained-text limit; no reader-owned read deadline. |
| Cohere v1 | Existing HTTP status/provider JSON-lines codec acceptance, before producer start. This is not payload-success verification or an SSE MIME assertion. | Initial failure closes body without starting producer. Later failure stops all writes/flushes and continues terminal usage collection; cancellation closes body. | Per-line limit and 30 s downstream write deadline; no reader-owned aggregate byte, idle or total deadline. |
| PaLM | Existing status/provider whole-JSON conversion acceptance; synthetic SSE, before the body-read goroutine. | Initial failure closes body before producer; synthetic write failure returns, with no subsequent write. | Whole response read limit, default 32 MiB; no reader-owned read deadline. |
| Ollama | Existing status/provider NDJSON or JSON codec acceptance; before role frame/Scan. | Initial failure closes body; later write failure retains existing read-to-done usage collection; request cancellation closes body. | Per-line limit; no reader-owned aggregate byte, idle or total deadline. |
| Xunfei | Validated upstream WebSocket handshake; commit before submitting the generation request or starting producer. | Failed initial commit closes socket, with no generation submission. Later writes latch failure; existing usage collection and cancellation cleanup remain. | 5 s handshake timeout; cumulative response byte budget; no WebSocket read/idle/total deadline. |
| Buffered Responses-to-chat conversion | Buffers and validates the upstream completion before emitting the downstream representation; no new early commit. | Existing body-close/conversion failure paths remain. | Per-line scanner limit and existing accumulation limits; no new reader-owned deadline. |
| OpenAI image JSON fallback | Bounded full JSON read and successful decode before synthetic SSE events. Non-SSE MIME does not qualify for native SSE early commit. | Existing body close, usage and image-count rules remain; failed writes use the shared downstream latch. | Configured whole-response limit; existing HTTP transport deadlines. |

The outer Go HTTP client has a total deadline only when `RELAY_TIMEOUT` is
nonzero; its default is zero. `DoApiRequest` adds a cumulative body byte wrapper
only when `ContextKeyResponseByteLimit > 0` (for example an assistant budget).
The per-line scanner limit does **not** imply a cumulative body or elapsed-time
limit. Legacy dedicated usage continuation can therefore wait indefinitely
when the upstream remains open, the client context remains live and no outer
total timeout is configured. Changing that settlement/read policy requires a
separate decision; this transport patch does not silently introduce one.

## Rust boundary and regression evidence

Rust already implements this transport boundary in `RelayHttpClient::send`:
it validates successful SSE headers and buffers the exact raw prefix under the
enabled first-output guard before returning a response. The prefix budget is
1 MiB. With no guard (including an explicit zero override), the response is
returned after upstream headers, so Axum can commit without a model frame.
Provider retry occurs before returning that response; downstream disconnect
cancels the guarded read or drops the active response body.

Rust's independent transport defaults are 1800 s response-header and 900 s read
idle deadlines, with no default total deadline. These remain unchanged. This
change adds real TCP upstream/gateway transport regressions rather than a new
Rust relay or billing implementation.

Go's real HTTP tests cover delayed headers with the global pinger enabled,
disabled-timeout early flush, headers/role/usage-only stalls, visible
content/reasoning/tool/function output, media-type rejection and disconnect
cleanup. Writer-injection tests cover initial flush failure with zero reads,
write failure with no subsequent events, internal-writer retry isolation and
dedicated terminal usage preservation. Native image disconnect tests retain
the existing requested-count and higher-actual-count billing assertions.

The Rust `relay_openai_sse_commit` test target covers the same header/first-output
boundary and real disconnect cleanup. Both runtimes still require their normal
CI/release qualification; transport fixtures do not prove production provider
credentials, models or usage reports.
