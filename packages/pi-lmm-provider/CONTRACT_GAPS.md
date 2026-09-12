# Integration gates (implementation, not a release approval)

## Catalog capabilities

Pi 0.85.1 model capability metadata is resolved from the installed official provider model directories by exact upstream model ID and the API advertised by the LMM catalog. Provider pricing is never reused and no cross-model fallback is permitted. Unknown IDs remain unknown and are not admitted to `/model`; the resolver does not infer capabilities from names.

## Non-static pricing

Pi 0.85.1 native cost fields cannot represent unknown, request-based, or expression pricing. Only complete finite USD/million-token configured static rates can be registered truthfully; null/dynamic prices are not replaced with zero, NaN, infinity, or invented maxima. Read-only `/lmm-prices` reports other prices with their basis and caveat. Enabling those models in `/model` needs an upstream Pi unknown-cost representation (or a separately reviewed integration), not another picker. No client budget-confirmation/expensive-group gate is planned.

## Refresh rotation

The installed Pi 0.85.1 native `Models.getAuth` correctly double-checks expiry inside `CredentialStore.modify`; `FileAuthStorageBackend.withLockAsync` holds a proper-lockfile cross-process lock across exchange and the write. However the public `Provider.auth.oauth.refresh` callback cannot inspect/attest the host's storage or require durable commit, and the file backend writes directly with writeFileSync, not atomic replacement. Abort/lock compromise/write failure after the server rotates can preserve the spent old refresh credential; a later attempt then reuses it. Memory/custom stores are also valid implementations of the same public interface.

The package now supports normal automatic refresh through its journal. The journal stores only credential summaries, serializes refreshes and prevents replay after failed rotation. If a crash loses the replacement token after server rotation, the grant may require a fresh `/login`; no plaintext token backup is kept. `/lmm-revoke` is wired to server revocation.

These are integration blockers to report to the main agent. This package must not be described as ready for publication/installation or a successful live-model test until they are resolved and separately reviewed.
