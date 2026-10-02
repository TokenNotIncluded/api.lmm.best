# Changelog

## 0.1.1

- Check that the LMM server actually registers the VS Code OAuth client before opening the browser. Older servers now show a clear update requirement instead of a misleading Pi authorization error.
- Keep authorization, token exchange, refresh and revocation bound to `lmm-vscode`; never fall back to another client's identity.
- Add login cancellation and client registration regressions alongside the existing callback, PKCE and refresh rotation tests.

The matching LMM backend update corrects client names throughout the consent flow. Updating the extension alone cannot register a client on an older server.
