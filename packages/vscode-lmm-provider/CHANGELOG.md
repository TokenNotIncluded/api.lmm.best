# Changelog

## 0.1.3

- Preserve independent upstream input limits, exact provider-qualified IDs and model classification in a reproducible, hashed models.dev snapshot. Conflicting bare IDs remain unknown.
- Exclude known embedding, image, speech and other non-text chat models even when configuration overrides exist.
- Support group-specific wire-ID tool verification and recheck tool settings and token budgets immediately before each request.
- Validate all catalog rows before filtering unsupported protocols. Add stale-setting, input-limit and model-classification regressions.

## 0.1.2

- Distinguish model groups and show authenticated group multipliers in model names.
- Replace universal context estimates with exact-ID models.dev reference limits, output reservation, and group/model-specific limit overrides. Unknown aliases remain explicitly labeled estimates.
- Display validated wallet USD/credits in the status bar, with refresh after sign-in and generation, periodic/manual refresh, and sign-out race protection. Missing or stale balances are not presented as current.
- Add metadata, wallet-unit validation and account status regression tests. No paid requests or production credentials are needed for these tests.

## 0.1.1

- Check that the LMM server actually registers the VS Code OAuth client before opening the browser. Older servers now show a clear update requirement instead of a misleading Pi authorization error.
- Keep authorization, token exchange, refresh and revocation bound to `lmm-vscode`; never fall back to another client's identity.
- Add login cancellation and client registration regressions alongside the existing callback, PKCE and refresh rotation tests.

The matching LMM backend update corrects client names throughout the consent flow. Updating the extension alone cannot register a client on an older server.
