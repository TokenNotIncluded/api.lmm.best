# Upstream response-model diagnostics

Usage logs can contain `other.response_model` with `requested_model`,
`upstream_model` (the selected provider request model), and `returned_model`
(a model declared by the provider before response conversion). These names are
diagnostics only. They do not select routes, prices, funding, settlement, retry,
OAuth permissions or a downstream model name. No mismatch boolean is stored.

An exact ordinary response adds no metadata. Different casing, provider paths,
snapshot aliases, model mapping and genuine differences retain the three names.
Empty declarations are ignored. A genuine mismatch replaces an earlier
compatible alias and is retained even if a later event matches or is empty.
Observations reset for each upstream attempt rather than leaking across retries.

The Go, Rust and Web comparison uses the same contract fixture in
`apps/lmm-extensions/relay/common/testdata/response_model_compatibility.json`. Comparison
trims whitespace, ignores case and compares the final provider-path component.
An empty expected name is not a wildcard. The returned name must equal the
requested or selected name, or extend one by exactly a supported suffix:

- `-YYYY-MM-DD` or `-YYYYMMDD`;
- `-latest`;
- `-preview`, optionally followed by `-MM-DD`, `-MM-YYYY`, `-YYYY-MM-DD` or
  `-YYYYMMDD`.

These suffixes identify declared aliases/snapshots, not provider authenticity.
Compatibility is directional: returning an unspecified base for a requested
snapshot does not establish the requested snapshot. Other suffixes and prefix
collisions warn, including `gpt-4` -> `gpt-4o`, `gpt-4o` -> `gpt-4o-mini`, and
Sonnet -> Haiku. This bounds the upstream prefix/suffix heuristic so it cannot
silently classify a smaller or unrelated model as an alias.

Go observes OpenAI Chat/Completions, Responses HTTP/WebSocket, Claude Messages
and Gemini generation declarations, including streamed and converted response
paths. It observes only provider fields (`model`, `response.model`,
`message.model`, Gemini `modelVersion`), never synthesized converter models.
Legacy native Claude behavior can still update its existing `UpstreamModelName`;
the diagnostic selected name is snapshotted before that update. Changing that
legacy behavior or pricing is outside this feature.

Rust OpenAI/Responses emit the same diagnostic schema in their existing settled
consume logs. Native Claude/Gemini do not yet participate in that usage-log
settlement, so returned-model diagnostics are not available in their consume
logs. Native same-protocol route support remains separate from logging parity;
cross-protocol ownership gating and unsupported WebSocket boundaries are
unchanged.
