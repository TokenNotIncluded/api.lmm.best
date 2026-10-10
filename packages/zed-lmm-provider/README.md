# LMM for Zed

<img src="https://raw.githubusercontent.com/TokenNotIncluded/api.lmm.best/main/packages/zed-lmm-provider/assets/icon.png" alt="LMM logo" width="128" height="128" />

**0.1.1 is a preview release.** Browser login requires the LMM server to register `lmm-zed` and advertise it in authorization-server metadata as `lmm_client_ids_supported`. Before opening a browser, the bridge verifies that registration along with the issuer, endpoints, S256 PKCE, and resource metadata. An older server receives a clear Zed-specific update message; the bridge never substitutes another client's identity. Packaging and local tests do not establish live production login or model access.

Use your LMM OAuth account in Zed's native AI model selector, with account-scoped models and automatic token refresh. No LMM API key is needed.

Zed's extension API currently cannot register an AI model provider. This package runs a local authenticated OpenAI-compatible bridge and configures Zed's built-in provider support. It is distributed through npm, **not the Zed extension marketplace**.

The package includes the LMM logo in `assets/icon.png`. Zed currently uses a fixed generic icon for [OpenAI-compatible providers](https://github.com/zed-industries/zed/blob/main/crates/language_models/src/provider/open_ai_compatible.rs); provider settings do not support a custom logo in the model picker.

## Quick start

Requires Node.js 22.19+ and the `zed` command in PATH.

```sh
npm install -g @tokennotincluded/zed-lmm-provider
lmm-zed login
# Quit all existing Zed windows first.
lmm-zed start
```

Login opens LMM in your browser. Approve the groups you want to use. `start` loads that account's catalog, adds the LMM provider to Zed's user settings, and launches Zed with a randomly generated local bridge key. Select an LMM model in Zed's AI model selector. Keep the bridge terminal running while using it; Ctrl+C stops the bridge.

`start` preserves unrelated settings and JSONC comments, saves a timestamped backup before changes, and keeps your existing LMM model limits/capabilities. It updates the LMM endpoint and catalog model list. A running Zed instance may ignore new environment variables, so quit it before starting.

The generated provider uses only OpenAI Chat Completions or Responses models advertised by your account. Anthropic-only models are omitted. The bridge supports text, tools and images when enabled in model settings, but this release has not been verified inside a real Zed session or against live billed LMM inference.

## Model limits and capabilities

OAuth catalog v1 does not expose context limits, tool support or image support. Automatic setup uses **compatibility defaults of 32,768 context tokens and 4,096 output tokens**, with tools, images and parallel tool calls disabled. These values are not a claim about model capabilities; adjust them from the model provider's documented limits if needed. Zed Agent tool use needs a verified tool-capable model with `tools: true`.

Optional verified overrides:

```sh
lmm-zed catalog
lmm-zed start --models models.json
```

`models.json` is an array. Use the exact catalog ID (it identifies both group and model):

```json
[
  {
    "id": "COPY_EXACT_ID_FROM_CATALOG",
    "max_tokens": 128000,
    "max_output_tokens": 8192,
    "capabilities": {
      "tools": true,
      "images": false,
      "parallel_tool_calls": false
    }
  }
]
```

Explicit `--models` replaces LMM model metadata with your overrides. Models without the required OpenAI protocol are rejected. Responses-only models are configured with `chat_completions: false`.

## Commands and storage

- `login`: OAuth authorization code flow, PKCE S256, verified issuer and loopback callback.
- `catalog`: inspect the current account's models, group IDs and advertised protocols.
- `settings`: print the generated settings fragment without changing Zed.
- `configure`: update and back up Zed settings without starting the bridge.
- `start`: configure Zed, start the bridge and launch `zed`.
- `logout`: revoke the OAuth token family and remove the local credential file. Revocation failure leaves local credentials available for another attempt.

Options: `--port 7391`, `--issuer https://api.lmm.best`, `--data-dir PATH`, `--settings PATH`, `--models FILE`. The issuer must be a trusted HTTPS origin. Default settings path is `$XDG_CONFIG_HOME/zed/settings.json` (or `~/.config/zed/settings.json`); Windows uses `%APPDATA%/zed/settings.json`.

OAuth credentials are stored in `$XDG_STATE_HOME/lmm-zed/oauth.json` (or `~/.local/state/lmm-zed/oauth.json`) with 0600 permissions inside a 0700 directory. This is private local file storage, not an OS keychain. Rotating refresh tokens are fenced by a durable journal before exchange; ambiguous refresh failures require another login rather than replaying a consumed token.

The bridge binds only `127.0.0.1`, requires a random local Bearer key, verifies Host, and refuses browser Origin requests. The local key is passed through `LMM_API_KEY` only to the launched Zed process; the LMM OAuth token is never written into Zed settings. Requests use the catalog's upstream model and `X-LMM-Group`, and inference is never automatically retried. Local processes with your user permissions can inspect local files/process environments.

## Development

```sh
npm ci
npm run typecheck
npm test
npm run build
npm run pack:check
npm pack --ignore-scripts
```

## Platform references

- [Zed extension features](https://zed.dev/docs/extensions/developing-extensions)
- [Zed extension API source](https://github.com/zed-industries/zed/blob/main/crates/extension_api/src/extension_api.rs)
- [Zed OpenAI-compatible providers and generated API-key environment variables](https://zed.dev/docs/ai/use-api-access#openai-compatible-endpoints)
