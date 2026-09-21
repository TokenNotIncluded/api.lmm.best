# Rust relay timeout boundaries

The Rust candidate uses a connection deadline (at most three seconds), an
adapter-owned deadline around sending the request and receiving response headers,
and a reqwest read timeout. The read timeout resets after each successful read;
it is not a total stream lifetime limit. A provider that sends headers and then
stops producing bytes must fail rather than retain a pending read indefinitely.
Transport bytes, including heartbeats, count as progress, not just model tokens.

The listener currently passes `LMM_DEPENDENCY_TIMEOUT_SECONDS` (default: 2) to
both the relay builder and the adapters. No default is changed here. This short
dependency setting may be unsuitable for real model first-token waits. Separate
operator-facing relay configuration remains migration work; increasing this
shared setting also affects non-relay dependencies.

The shared client is used by OpenAI, Anthropic/Gemini, media/Midjourney, misc
relay and assistant adapters. Read-timeout protection applies to their consumed
response bodies as well. Existing adapter-specific deadlines and body-size
limits are unchanged. Control-plane `outbound_http::client` retains its total
request timeout. TLS validation and redirect rejection remain unchanged.

This is not full Go timeout parity: Go supports an optional total `RELAY_TIMEOUT`
as well as `RELAY_RESPONSE_HEADER_TIMEOUT`. Rust's adapter deadline also includes
request sending; it is not solely the post-upload wait. A read timeout does not
bound an indefinitely progressing stream, nor provide an absolute request or
slow-consumer deadline. Cancellation is tested by dropping the downstream body
and observing the loopback producer being released.

Run the loopback regression suite without database or provider credentials:

```sh
cd apps/api-rust
cargo test --locked --test relay_openai_first_token
```

It exercises the real relay client and HTTP router for missing headers,
progressing streams beyond both configured deadlines, stalled bodies, and
downstream cancellation. No production rollout or accounting changes are made.
