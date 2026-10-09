# Async task performance metrics

Go's video/task and Suno polling paths contribute to the existing model performance metrics when a task first reaches `SUCCESS` or `FAILURE`. This includes missing upstream IDs, unavailable channels, and timeout cleanup. Legacy timeout tasks contribute failure samples while retaining their existing no-refund policy.

Only the worker that wins the durable status compare-and-swap (CAS) records a sample. Nonterminal updates, competing CAS losers, repeated terminal metadata updates, and refund reconciliation emit no new sample. Sampling is observational: it does not change settlement, refund intent, pricing, group routing, model price locks, or `/fast` behavior.

The metric model is `PrivateData.BillingContext.OriginModelName`, falling back to `Properties.OriginModelName` for older tasks. The group is `Task.Group` (an empty group uses the existing `default` bucket). Keys contain only model, group, and time bucket; task/user IDs and provider response payloads are not metric dimensions. Tasks without a model name are skipped.

Latency covers submission to completion, including queue time, using second-resolution task timestamps converted to milliseconds. If a provider omits the finish timestamp, the time at which polling observes the terminal result is used without changing the stored task. Missing, reversed, or unrepresentable durations contribute zero latency. Async samples do not report time to first token.

Successful tasks with positive provider-reported `TotalTokens` contribute throughput; otherwise positive `CompletionTokens` is used. The denominator covers start to finish, falling back to submission when the start timestamp is missing. Throughput requires a valid positive duration. Failed tasks and successful tasks without reported usage still contribute counts and latency, without invented token usage. Suno and compatible task responses currently carry no normalized token usage.

Samples run after the existing financial actions and use the enabled setting, bounded process-local buckets, optional Redis/Valkey current buckets, and periodic `perf_metrics` database flush. The existing Redis recorder uses a one-second context; Redis errors and sampling panics do not propagate into task processing. This is best-effort telemetry, not a transactional outbox: terminal CAS, sampling, and flushing are separate, so a process crash can lose a sample. The terminal transition is not replayed merely to recover telemetry.

## Rust boundary

This change adds samples to the Go polling lifecycle. Rust already has native Midjourney submission wiring, stored Kling task reads, and task-list reads. Its Suno/Kling/Jimeng submission provider remains unconfigured by default, and generic video routes use the fail-closed service; this change does not extend those providers.

The former Rust metrics reader has been retired. The new core has not yet implemented task metrics; current Go collection and storage remain unchanged.
