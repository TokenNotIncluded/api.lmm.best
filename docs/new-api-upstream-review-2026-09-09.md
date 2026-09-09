# New API selective upstream review — 2026-09-09

## Scope and comparison

Compared LMM base `74384a66939cacb7d74a78ca3c7aa035900d1b24` with
QuantumNous/new-api `064ed943e1ac40e3eaca1b58ffb7fa5dacb3fde3`.
Screened the latest 75 upstream commit subjects (August 30–September 9), then
read candidate patches and the corresponding local implementations. This is
a selective integration, not a claim that every upstream change was audited
or that the repositories are fully synchronized.

For the source inventory, mapped upstream Go paths into `apps/api-go/` and
`web/src/` into `apps/web/src/`, normalizing the Go module import prefix.
The counts below include tests. They compare the two starting revisions,
not the resulting patch, and measure file content rather than semantic equivalence.
Frontend extensions counted: `.ts`, `.tsx`, `.js`, `.jsx`, `.css`.

| Area | Local files | Upstream files | Shared paths | Identical | Different | Local-only paths | Upstream-only paths |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Go | 1358 | 972 | 760 | 269 | 491 | 598 | 212 |
| Frontend source | 1372 | 1244 | 886 | 289 | 597 | 486 | 358 |

Paths that exist only on one side can represent renamed or replaced features;
these counts must not be interpreted as missing functionality.

## Adopted changes and adaptations

| Upstream commit | Change | LMM adaptation |
| --- | --- | --- |
| [876903a8](https://github.com/QuantumNous/new-api/commit/876903a8eb22c44e395c03da38f6701c650651ae) | Correct VolcEngine model discovery | Use `/api/v3/models` for the regular Ark base; retain the existing special-base override, enabled-key selection, request headers and URL validation. Test actual HTTP paths and authorization. |
| [6e10f9bc](https://github.com/QuantumNous/new-api/commit/6e10f9bc927a4eae889864a6ef601359d53526b9) | Preserve Kimi dynamic tools | Preserve message-level `tools`, omit nil `content` only on tool-loading messages, count tool declarations, and share system-prompt injection. Encode the history once instead of allocating a serialized RawMessage per message. Regenerate the assistant request contracts after the DTO change. |
| [49ec4696](https://github.com/QuantumNous/new-api/commit/49ec4696682530781a036eab1ac195f0b04706c0) | Model-specific OpenAI Chat capabilities | Match mapped model names and supported snapshots; preserve unknown models. Keep this fork's legacy reasoning-effort suffix handling. Retain the effective effort across OpenRouter conversion. Explicit completion limits, including zero, take precedence and always remove unsupported `max_tokens`. |
| [b518d003](https://github.com/QuantumNous/new-api/commit/b518d0033b670f5518b8a2f1cf8ea0142a9d1b8d) | Bound response-header waits | Add the environment setting to the current transport factory, preserving proxy caching and SSRF checks. Clamp using int64 so the constant remains compilable on 32-bit targets; explicitly clear inherited timeouts when disabled. Test a stalled server and streaming after headers. |
| [8f72ecbb](https://github.com/QuantumNous/new-api/commit/8f72ecbbfd86ded5ee15373921533eed3d334a9a) | Searchable log groups | Reuse the existing combobox and this fork's Bun/Happy DOM tests. Personal/admin suggestions follow the active log scope; exclude `auto`. Preserve historical custom groups, confirm selection before Enter submits, and retain the password input while sensitive fields are hidden so no suggestion list is exposed. |
| [c79b74b6](https://github.com/QuantumNous/new-api/commit/c79b74b68358180c68440057596bdc34a99cc649) | Reduce duplicate status requests | Adopt only the applicable request-sharing principle: system configuration loading now calls `getStatus()` instead of raw `fetch`. It joins existing session-scoped in-flight HTTP requests. Keep current user-scoped query keys, capability readiness checks, and fresh route-guard checks. This does not introduce a new global status cache or eliminate intentional refreshes. |

`/api/status` is not entirely public in LMM: `controller/misc.go` projects
`docs_access`, `docs_link`, and console activation state for the authenticated
user. The upstream single global query entry cannot be copied safely under
that assumption. Existing branding, custom currency codes and capability
normalization remain intact.

Kimi message-level tools are passed through OpenAI-compatible Chat requests.
This change does not implement dynamic tool loading for every cross-protocol
conversion. The host-injectable JSON codec part of the upstream patch was not
imported; the local relaykit retains its current JSON helpers.

## Screened changes not imported in this patch

| Candidate | Decision and source evidence |
| --- | --- |
| [2cf177ac](https://github.com/QuantumNous/new-api/commit/2cf177ac) — bulk RawMessage copying | Already implemented locally in `common/copy.go`, including both RawMessage and byte-slice converters and memory/ownership tests. Keep the local implementation. |
| [4fc9d1f1](https://github.com/QuantumNous/new-api/commit/4fc9d1f1) — pricing writes / option primary-key rebuild | The affected upstream `model/model_pricing_config.go` does not exist locally. LMM uses `model/option.go` with price-lock-aware transactions. Do not import the upstream pricing mutation path or automatic table rebuild as a dependency of unrelated fixes. This does not establish that every historical database has a valid options primary key. |
| [064ed943](https://github.com/QuantumNous/new-api/commit/064ed943), [d52bdc0b](https://github.com/QuantumNous/new-api/commit/d52bdc0b) — expression/time-based pricing | Defer the coordinated billing and editor migration: it needs explicit integration with dynamic pricing, persistent price locks, currency conversion and settlement. Copying only its frontend would expose unsupported configuration. |
| [9bf328d9](https://github.com/QuantumNous/new-api/commit/9bf328d9), [a2057413](https://github.com/QuantumNous/new-api/commit/a2057413) — Sora/Wan task plugins | The corresponding JavaScript task-plugin runtime and plugin files are absent locally. Provider-field exposure and usage accounting need a separate migration to the local task adaptors. |
| [6f233399](https://github.com/QuantumNous/new-api/commit/6f233399) and related auth commits | Defer coordinated auth migration. LMM already has its own session rotation, account permissions and backend capability gates; replacing just the UI or verification endpoints would break those contracts. |
| [12be9975](https://github.com/QuantumNous/new-api/commit/12be9975) — global error notification changes | Defer the cross-application migration. Preserve LMM's price-lock warning responses and assistant-specific success/ignored semantics. |

## Operational behavior

`RELAY_RESPONSE_HEADER_TIMEOUT` defaults to **1800 seconds**. It limits waiting
for upstream response headers after the request is written, including the
wait for non-streaming generation. It does not impose a total duration on
streaming after headers arrive. Set `0` to disable; negative values also
disable it. Restart the Go service after changing the environment variable.
This bounds one source of retained request buffers; it is not a process-wide
memory limit or a guarantee against every stalled network operation.

No schema migration or dependency upgrade is included. Waffo lifecycle handling,
dynamic pricing and price locks retain their existing implementations.

## Verification

- Go main-module full tests; relaykit full tests; Go vet and production build.
- Controller request-compatibility tests cover mapped models, Azure/streaming,
  sampling, explicit zero limits, legacy effort suffixes, OpenRouter effort,
  and unchanged direct Responses requests.
- HTTP timeout tests cover enabled/disabled/overflow values and streaming
  behavior; targeted service tests run with the race detector.
- Frontend full suite: 240 test files; typecheck, lint, production build,
  protected-header formatting, and initial bundle budget check.
- Log-group DOM regressions exercise filtering, scope switching, masking,
  historical groups, failed suggestions, blur, and Enter selection/submission.

Validation uses local test servers and fixtures, not production provider
credentials or live billing transactions. No production deployment was made.
