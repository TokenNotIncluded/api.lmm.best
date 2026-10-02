# LMM for Copilot

<img src="https://raw.githubusercontent.com/TokenNotIncluded/api.lmm.best/main/packages/vscode-lmm-provider/assets/icon.png" alt="LMM logo" width="128" height="128" />

> **Preview:** Production OAuth client registration is pending deployment; current build/runtime tests do not establish live model access.

Use the models available to your LMM account in the native VS Code Chat model picker. Sign in with your browser; no API key is required.

## Setup

1. Install **LMM for Copilot** in desktop VS Code 1.104 or newer. Install or enable the VS Code Chat / GitHub Copilot Chat interface if it is not already available.
2. Run **LMM: Sign In** from the Command Palette. Approve access and select model groups in the LMM browser consent screen.
3. Open Chat, select **LMM** in the model picker (or **Manage Models**), and choose a group/model. Run **LMM: Refresh Models** after changing access.
4. Run **LMM: Sign Out** to remove the local credential and revoke the server grant.

LMM account balance and current server pricing apply to every request. This extension does not provide free Copilot credits or an inline tab-completion provider. Chat UI availability and account requirements are controlled by your VS Code / Copilot installation.

OAuth login uses a temporary listener bound only to `127.0.0.1`, random state, S256 PKCE, and an issuer-bound callback. Credentials are stored through VS Code SecretStorage. Expiring tokens rotate automatically; a durable hashed journal prevents refresh-token replay across windows or after a crash. If a rotation is interrupted, sign in again. Credentials, authorization codes, prompts, and responses are never logged by this extension.

The extension runs on the desktop UI host, including Remote SSH workspaces. Browser-only vscode.dev is not supported. A browser must be able to reach the VS Code computer's loopback address. No issuer override or API-key fallback is supported.

## Models and tools

The OAuth catalog determines available groups and models. This version supports catalog entries advertising the OpenAI chat completions API, streams text and function-call deltas, and passes text tool results back to the model. Responses-only and Anthropic-only catalog entries are excluded. Images and non-text tool results are unsupported and produce a clear error.

The catalog does not provide verified context limits or function-calling capability. The default settings advertise a conservative **32,768 input / 4,096 output token budget**, rather than a claim about a model's full context. Set `lmm.maxInputTokens` and `lmm.maxOutputTokens` at or below the supported limits of all models you use. Token counting is a conservative UTF-8 byte estimate, not the model's exact tokenizer.

Tools are disabled by default. After verifying that a model supports OpenAI function calling, add its exact upstream model ID to `lmm.toolModels`. Refresh the picker; the model can then be selected for tool-based Chat/Agent workflows. The provider emits tool calls to VS Code; VS Code owns approvals and tool execution. No arbitrary model option can override the OAuth group, relay destination, or credentials. Billable requests are never retried automatically.

## Build and test

```sh
npm ci --ignore-scripts
npm test
npm run package
# Optional real desktop extension-host smoke (installed VS Code and a display required):
npm run test:host
```

`npm test` compiles against the stable VS Code 1.104 API and runs OAuth rotation, cross-window replay, catalog authorization, callback security, and SSE tests. `npm run package` creates `lmm-copilot-provider-0.1.0.vsix`; the extension has no runtime npm dependencies.

Install the local package with `code --install-extension ./lmm-copilot-provider-0.1.0.vsix`. A real OAuth/model request requires the production server to register public native client `lmm-vscode` with loopback `/oauth/lmm/callback`, the required scopes, and the OAuth relay endpoints.

## Marketplace publishing

The manifest publisher is `LIghtJUNction`. Publishing requires control of that exact Visual Studio Marketplace publisher and an Azure DevOps Personal Access Token authorized to manage extensions. Run `npx vsce publish --packagePath ./lmm-copilot-provider-0.1.0.vsix` from an authenticated publisher environment. Open VSX additionally requires an Open VSX account, a matching owned namespace, publisher agreement, and its own access token. Never put publishing tokens in repository files.

Packaging and automated tests do not establish a successful browser OAuth session, paid model request, or Marketplace listing. Those must be verified separately.

## Development sources

The provider follows Microsoft's [Language Model Chat Provider API](https://code.visualstudio.com/api/extension-guides/ai/language-model-chat-provider), [stable 1.104 API types](https://github.com/microsoft/vscode/blob/1.104.0/src/vscode-dts/vscode.d.ts), and [official provider sample](https://github.com/microsoft/vscode-extension-samples/tree/main/chat-model-provider-sample). OAuth relay contracts are implemented by the LMM server in this repository.
