# Responses WebSocket channels

Go serves persistent Responses sessions at `GET /v1/responses`. Channel selection
and locked-channel revalidation use the same eligibility check. LMM's channel
identifiers and defaults are:

| Channel | LMM type | Unset `responses_websocket_enabled` | Explicit `false` |
| --- | --- | --- | --- |
| OpenAI | 1 | Enabled, preserving existing behavior | Disabled |
| Codex | 57 | Enabled, preserving existing behavior | Disabled |
| Advanced Custom | 58 | Disabled; explicit opt-in required | Disabled |
| Sub2API | 59 | Disabled; explicit opt-in required | Disabled |
| New API | 60 | Disabled; explicit opt-in required | Disabled |
| OpenHuman (removed) | 61 (reserved) | Unsupported | Unsupported |

The field belongs to the channel's `setting` JSON. Go uses an optional boolean so
omission remains distinguishable from `false`; database create/update/reload
preserves both. The Web editor uses one type predicate for the toggle and
serializer, saves explicit boolean values, and loads the compatibility default
when the field is absent. Changing channel type resets the control to that type's
default and excludes the field for unsupported types. The editor requires a live
backend capability of `responses_websocket: true`; an absent or false capability
disables the control without clearing a saved value.

## Native protocol and configuration limits

Sub2API and New API use their existing native Responses adaptor, upstream URL,
and Bearer credential semantics. Supporting their HTTP adaptor alone does not
establish that an external provider implements WebSocket Responses; the configured
upstream must accept a native `response.create` session and Responses events.
Codex retains its existing JSON credential and account/Beta header handling.

Advanced Custom requires a matching `/v1/responses` route for the requested
original model and an empty or `none` converter. Other routes on the same channel
may use converters. The selected upstream path must end in `/responses` (a
trailing slash is allowed), with no URL credentials or fragment. A full HTTP(S)
URL may supply the upstream host; a relative path requires a valid HTTP(S) base
URL. The existing route adaptor handles model placeholders and route selection.
Routes to Messages, Chat Completions, or converted Responses are rejected before
billing preparation or dial.

Route authentication supports the existing default Bearer key and the configured
`header`, `query`, or `none` modes, including the `{api_key}` template. Header
credentials must have valid HTTP syntax and cannot replace connection, upgrade,
host, framing, or `Sec-WebSocket-*` handshake headers. New types (58/59/60) with a
per-channel `proxy` setting are rejected before billing or dial: this transport
does not implement that setting. Existing 1/57/61 proxy behavior is retained.
This is a capability limit, not a claim of interoperability with every provider.

Unsupported types/configurations produce the stable non-retryable
`responses_websocket_unsupported` error (HTTP 400); an explicitly disabled or
default-disabled supported channel produces `responses_websocket_disabled`
(HTTP 403). Initial pool selection excludes ineligible channels locally, without
spending upstream retries, marking them used, preparing billing, or dialing them.
Pinned and already locked channels never fall back to another channel.

## Persistent session changes

Every create still revalidates authentication and channel/group/model access.
The session retains its channel/model lock, one-active-turn admission, used-channel
tracking, and existing shutdown registration. A route, route-auth, key, base URL,
organization, header override, model mapping, or effective parameter override
change retires the idle upstream connection and redials the same locked channel
before sending the next admitted turn. Parameter overrides can rewrite handshake
headers, so any change to that configuration conservatively triggers reconnection,
including a body-only change. A multi-key session keeps its physical connection's
enabled key; deleting or disabling that key causes selection of a replacement and
reconnection. Secrets are only part of a hash and are not logged.

An eligibility error for a rejected second create does not orphan an already
admitted turn. Its existing upstream terminal event still completes settlement
and releases the reservation. An idle disabled connection is closed before any
later controls can be forwarded. Administrative channel shutdown remains handled
by the existing session manager. A retired reader cannot forward events, settle,
or close a replacement connection. Shutdown closes the client socket before
waiting for a stalled forwarding write.

Success admission and charging remain independent: a failed/incomplete/cancelled
turn can settle billable output while releasing its successful-request slot;
a delivered completed/done turn commits that slot once. Pricing, group ratios,
OAuth handling, and administrator permissions are unchanged.

## Validation boundary

Go regressions exercise all six types with real local WebSocket handshakes and
native create/terminal frames, two turns on one connection, Advanced Custom auth
placement, actual configuration redials, disabled/ineligible no-dial paths,
multi-key replacement, clearing organization, old-reader isolation, active-turn
rejection, stalled-client shutdown, auto-group retry restoration, and failed
partial billing followed by a completed turn and a rate-limit rejection. A
database regression checks typed setting create/update/reload. Web regressions
exercise the rendered toggle, capability gate, and actual serializers for true,
false, and unset values across the supported types.

Rust deliberately keeps its production service unconfigured for every channel
type. Saving the setting does not enable a provider. It does not advertise the
capability and rejects a complete legal Upgrade with stable HTTP 503 before any
provider/billing hooks. See [the Rust transport boundary](rust-responses-websocket-correlation.md#rust-channel-capability-boundary-375).

These local regressions establish the gateway contract. They do not establish
deployment or authenticated interoperability with live external providers.
