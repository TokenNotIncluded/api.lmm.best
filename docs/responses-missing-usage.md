# Responses missing-usage settlement

Go and Rust native Responses streams include refusal deltas in the same local output lower bound as text, function-call arguments, reasoning text and reasoning summaries. When a successful `response.completed` or `response.done` is the only source of generated output, the lower bound reads its message text/refusal, string function arguments and visible reasoning content/summary. Encrypted reasoning, images and tool metadata are excluded.

Terminal snapshots are used only if no output delta text was collected, so the usual delta plus final snapshot sequence is not counted twice. This deliberately remains a lower bound: a partially observed item does not cause other final-snapshot items to be added speculatively. The original provider response is preserved.

An accepted provider usage report always takes priority. A zero lifecycle placeholder does not erase earlier measured usage or suppress later observed output. A terminal usage report, including explicit zero, does suppress local output and historical/prepayment fallback. The distinction is retained through Go's final settlement and Rust's price calculation; checking only the nonzero token counts loses it. Separately priced actual tool calls retain their existing rules.

| Evidence | Current settlement policy |
| --- | --- |
| No upstream event | No estimated consumption |
| Created-only then EOF, without usage | No estimated consumption; acceptance alone does not prove input consumption |
| Incomplete/cancelled without usage or output | No estimated consumption |
| Explicit failure/flat error without usage or output | No estimated consumption |
| Successful terminal-only generated output, without usage | Count recognized generated output; bounded request-side input estimate |
| Text/refusal/function/reasoning deltas, without usage | Count the observed generated lower bound; bounded request-side input estimate |
| EOF/read failure/client cancellation after billable output | Keep existing partial settlement and replay prevention |
| Provider terminal reports zero | Zero token charge; no historical/prepayment fallback |

The input estimate remains clamped to 1,050,000 tokens. A successful response with **absent** usage and no countable output retains the existing successful-empty fallback. Reported zero and absent usage are different cases.

Flat `error` events terminate both runtimes immediately, preventing a later completion from replacing the failure and avoiding a second synthetic interruption event. A nominally completed event carrying a non-null error cannot seed terminal-output estimates.

## Confirmed partial-output policy in issue #373

Issue [#373](https://github.com/TokenNotIncluded/api.lmm.best/issues/373) requests zero fallback after explicit failure without provider usage, while also requiring the original [#341](https://github.com/TokenNotIncluded/api.lmm.best/pull/341) regressions to remain green. The original `failed_text` regression explicitly charges a stream that emitted text and then `response.failed` without provider usage. Both requirements cannot be met unchanged.

On 2026-10-03 the maintainer explicitly chose to preserve the existing partial-output billing rule. An explicit failed/error event after actual output therefore still uses the existing local lower bound when provider usage is absent. Empty failures, created-only and empty-incomplete requests continue to settle at zero. The issue is resolved against this confirmed policy rather than changing the original `failed_text` regression.

## Verification scope

Go covers the existing real HTTP/gzip settlement matrix plus refusal interruption, terminal text/refusal/function/reasoning, snapshot deduplication, reported-zero final settlement, completed-with-error and flat-error termination. The assertions compare wallet, Token, user usage, consume-log quota and one upstream request. Native nonstream reported-zero observation is covered separately.

Rust unit tests cover the same output types, provider usage priority, successful reported-zero pricing, failed terminal-only exclusion and flat-error framing. The explicitly ignored PostgreSQL settlement suite includes real HTTP cases for all terminal output types, snapshot deduplication, streaming/nonstreaming reported zero, nested failure and flat-error termination; it must be run with an isolated database. Normal tests alone do not prove that integration suite passed.
