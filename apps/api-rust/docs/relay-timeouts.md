# Relay Timeout Configuration

Provider requests use `RelayHttpClient`, independently of the internal dependency
timeout (`LMM_DEPENDENCY_TIMEOUT_SECONDS`, default 2 seconds).

Request construction exposes only header, body and JSON setters. Requests must
be submitted through `RelayHttpClient::send`; adapters cannot obtain the raw
reqwest client or send a builder directly to bypass the controlled response.

| Rust variable | Go fallback | Default | Zero |
| --- | --- | --- | --- |
| `LMM_RELAY_RESPONSE_HEADER_TIMEOUT_SECONDS` | `RELAY_RESPONSE_HEADER_TIMEOUT` | 1800 seconds | Disable header deadline |
| `LMM_RELAY_IDLE_TIMEOUT_SECONDS` | `STREAMING_TIMEOUT` | 300 seconds | Startup error |
| `LMM_RELAY_TIMEOUT_SECONDS` | `RELAY_TIMEOUT` | 0 seconds | Disable total deadline |

Rust settings take precedence over Go aliases, then defaults. A present invalid
Rust value is an error, not a reason to fall back. Values must be nonnegative
decimal whole seconds; empty, signed, fractional and unrepresentable values are
rejected. Errors identify the variable without displaying its contents.
Connections have a separate fixed 3-second deadline.

For example, retain Go settings during migration or explicitly override them:

```text
RELAY_RESPONSE_HEADER_TIMEOUT=1800
STREAMING_TIMEOUT=300
LMM_RELAY_RESPONSE_HEADER_TIMEOUT_SECONDS=600
LMM_RELAY_TIMEOUT_SECONDS=0
```

## Timing Boundaries

The header deadline starts when each upstream send starts, including connection
and request upload. Go starts its response-header timer after uploading; this is
an accepted migration difference, not complete Go parity.

Body idle protection begins when reading the response body, not while awaiting
headers. Each wait for nonempty bytes has one deadline. Empty frames and repeated
pending polls do not extend it; any nonempty byte fragment or heartbeat does.
This is byte progress, not a complete SSE-line deadline. The wrapper does not
parse SSE, prefetch in a background task, or buffer the whole response.

The optional reqwest total deadline covers sending and consuming the body of one
upstream attempt. Progress cannot reset it. Existing retries receive independent
budgets; retry counts and selection policy are unchanged. Disabling header or
total deadlines can retain upstream resources for longer. Body idle protection
is not a downstream socket-write deadline: a consumer that stops polling is not
actively waiting for upstream bytes.

Success and upstream-error bodies both use controlled reads, preserving existing
size limits. Once streaming starts, a timeout is a body error, never a synthetic
`[DONE]` or an additional JSON response. Error-body read failures retain the
known upstream status and the adapter's safe fallback. Dropping the body releases
the upstream response; the wrapper creates no background tasks.

## Isolated Policies

OpenAI, Anthropic/Gemini, media, Midjourney submissions, misc and assistant model
requests share this policy. Internal dependency and misc Valkey rate-limit
deadlines remain separate. Protected Midjourney image fetching retains its
existing DNS/address validation, redirect and deadline policy. Assistant retains
its existing business-level outer deadline.

Slow downstream write protection, deployment and full Go timing equivalence are
outside this change.

## Differential Evidence

The OpenAI listener oracle accepts `LMM_RELAY_TIMEOUT_PROFILE` values from
`tests/behavior-oracle/fixtures/scenarios/relay_timeouts.json`. Both real listeners
use the same provider delay and timeout values. Use a clean, external Go source
tree that includes `RELAY_RESPONSE_HEADER_TIMEOUT`; the oracle's older default
Go revision predates that setting. Set `LMM_GO_ORACLE_REVISION` to identify the
exported commit and `LMM_GO_ORACLE_ROOT` to its source root.

The initial profiles cover three-second response headers, a one-second header
deadline, and a disabled header deadline. `timeout-result.json` in the retained
runtime records status, content type, exact body, elapsed time and differences.
The active-stream and total-stream profiles also exercise progressing SSE with
total timeout disabled/enabled. The cancel-stream profile closes the downstream
connection after first content and verifies a correlated upstream disconnect
event, rather than counting natural completion as cancellation.

Existing pre-header error envelopes intentionally remain different: Go returns
504 with `upstream_timeout`, while Rust OpenAI returns 500 with
`do_request_failed`. The oracle verifies each contract and explicitly reports
the status/body mismatch; it does not normalize it into an equality claim.

The tested Go OpenAI handler synthesizes an additional usage event on successful
SSE and emits `[DONE]` after a total-timeout truncation. Rust preserves provider
bytes on success and reports a body error without synthetic `[DONE]` on timeout.
Both stream profiles record the raw mismatch. These existing handler differences
are not changed by this timeout policy or represented as byte-level parity.

The fragment-stream profile sends nonempty fragments without completing an SSE
line within the idle interval: Rust continues on byte progress, while the tested
Go handler times out waiting for a line. The heartbeat-stream profile verifies
that comment heartbeats keep the stream active; Rust preserves their bytes,
whereas the Go handler consumes them. These differences are explicit scenario
expectations, not normalized away by the comparator.
