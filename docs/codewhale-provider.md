# Codewhale LMM OAuth adapter

Source: `packages/codewhale-lmm-provider`, pinned as a Git submodule to
`TokenNotIncluded/codewhale-lmm-provider`. Adapter source, tests, CI and package
metadata live in the independent repository; this parent repository owns the
server-side OAuth client registration and the pinned submodule revision.

The backend registers public native client `lmm-codewhale` / `LMM for Codewhale`.
It reuses the existing PKCE S256, state/issuer-bound loopback callback, resource,
refresh rotation and revocation contract. Its initial profile is exactly
`catalog:read balance:read usage:read models:invoke`, plus only the groups added
by explicit consent. MCP and marketplace permissions are not registered or added.
Pi/DSH/CLI grants and existing OAuth deployment switches are unchanged.

Codewhale's current plugin bundle API has no executable provider/auth adapter.
This package therefore uses a companion CLI and the documented named
`openai-compatible` provider configuration. Its native bundle contains a help
skill, not a fictitious OAuth provider entry. Only models advertising
`openai-completions` are admitted. Responses/Messages-only models, Pi-specific
model-picker integration and MCP/marketplace features are not claimed.

The CLI holds LMM credentials; the host only receives a random per-run loopback
capability through `api_key_env`. It must be launched with `codewhale-lmm run`.
The temporary provider config does not overwrite the existing Codewhale config.
The bridge rechecks catalog/grant on each request, replaces the exact synthetic
model ID with the upstream model, and sets `X-LMM-Group` without fallback.
Streaming bytes are passed through, disconnects cancel the upstream, and the
adapter does not retry inference POSTs. The host may have its own retry policy.

Local verification: Linux, Node.js 22.16.0, 36 passing tests and syntax/package
checks, including mock OAuth HTTP, separate-process refresh locking, and a mock
host process. This is not live Codewhale or production/billing acceptance.
Backend tests: `cd apps/api-go && go test ./oauthserver -run TestCodewhale -count=1`.
The registration tests passed in the initial GitHub CI run 35638700004; they
were not run in the dependency-unavailable local review environment. Production rollout remains gated by real interoperability
and billing tests; registering a client does not enable or deploy OAuth.
