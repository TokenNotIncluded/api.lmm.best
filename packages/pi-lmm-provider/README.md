# LMM provider for Pi

Development preview for Pi 0.85.1. This package is not yet release-ready; live OAuth and model invocation remain integration acceptance requirements.

The extension registers LMM in Pi's native provider interface. It uses browser OAuth with a loopback callback and PKCE; it does not ask users to copy an API key. `/lmm-prices` refreshes the account-scoped catalog and shows its pricing. Wallet status is labelled as platform credit, not spendable US dollars.

## Local development

```sh
cd packages/pi-lmm-provider
npm install --ignore-scripts
npm test
npm run typecheck
npm run pack:check
```

After the interoperability gates are resolved, test the local extension with Pi's `-e ./src/index.ts`, use `/login` to select LMM, and select an admitted model through `/model`. A successful package load or browser login alone is not a successful model-call test.

## Current acceptance gaps

- The server must be deployed with a trusted issuer and explicit group allowlist.
- Catalog capabilities come from the installed Pi official provider model directories by exact upstream model ID and advertised API. Provider prices are not reused and no cross-model fallback is allowed; unknown IDs remain unavailable for invocation.
- Static token prices are converted from platform credits to nominal USD. Dynamic, expression and incomplete prices remain inspection-only.
- Normal automatic refresh is implemented with a journal that stores only credential summaries and prevents replay after failed rotation. A crash that loses the replacement token still requires login again.
- `/lmm-revoke` is connected, but live authorization, stream, cancellation, account switching, revocation, refresh and billing reconciliation remain to be tested in production.

See [CONTRACT_GAPS.md](CONTRACT_GAPS.md) and the repository's [OAuth profile](../../apps/api-go/service/oauth_contract.md). These gaps are work to complete, not a reduced definition of the intended product.
