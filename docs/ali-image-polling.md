# Ali asynchronous image polling

Ali asynchronous image requests wait 5 seconds before the first task query,
then wait 10 seconds between queries, with at most 20 attempts. Transport and
response-reading errors consume attempts too. There is no wait after the final
attempt. Twenty immediate nonterminal or failed queries therefore finish after
195 seconds.

Polling has a **205-second overall deadline**, starting when polling begins and
covering delays, HTTP response headers and response body reads. This is a new
wall-clock bound, not a guarantee previously provided by the attempt counter.
Slow queries may use the budget before all 20 attempts run. Once Ali accepts a
task, polling is detached from the incoming request deadline and cancellation:
a client disconnect does not cancel the provider's image-generation job and
must not cause the accepted job's pre-consumed quota to be refunded. The
internal polling deadline remains in effect.

Task queries reuse the shared HTTP client with the channel's proxy and transport
settings. Existing `RELAY_TIMEOUT` and response-header timeout settings still
apply; the shared client's timeout is not mutated. Even with `RELAY_TIMEOUT=0`,
the polling context provides a finite remaining budget.

Terminal provider statuses and raw responses retain their existing handling.
Synchronous image generation, response conversion and image-count pricing are
unchanged.
