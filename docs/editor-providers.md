# LMM editor providers

The editor adapters use browser OAuth to access the models allowed by the signed-in LMM account. They do not require a manually copied LMM API key. The server still checks the account, consented groups, model permissions and balance for each request.

| Editor | Package | Integration |
| --- | --- | --- |
| Visual Studio Code | [`vscode-lmm-provider`](../packages/vscode-lmm-provider) | Language model provider for the Copilot Chat model picker |
| Zed | [`zed-lmm-provider`](../packages/zed-lmm-provider) | Local OAuth bridge used by Zed's OpenAI-compatible provider |

These are different integrations. The VS Code extension uses the official [Language Model Chat Provider API](https://code.visualstudio.com/api/extension-guides/ai/language-model-chat-provider). Zed's documented [extension capabilities](https://zed.dev/docs/extensions/developing-extensions) do not expose a native language model provider registration API, so the Zed package runs a local bridge through [OpenAI-compatible provider settings](https://zed.dev/docs/ai/use-api-access#openai-compatible-endpoints). It is not a native Zed AI extension and must not be presented as a Zed marketplace extension.

## Install and use

For VS Code, build the VSIX in `packages/vscode-lmm-provider` with `npm ci --ignore-scripts` and `npm run package`, then use **Extensions: Install from VSIX**. The extension requires VS Code 1.104 or later. Run **LMM: Sign In**, complete browser consent, and choose an LMM model in the Copilot Chat model picker. **LMM: Refresh Models** reloads the authorized catalog; **LMM: Sign Out** removes the login. Copilot organization policy can disable custom language model providers.

The VS Code adapter admits catalog models with a supported OpenAI chat-completions endpoint. Context limits are not supplied by the LMM catalog, so the extension advertises configurable conservative budgets through `lmm.maxInputTokens` and `lmm.maxOutputTokens`. Set these within the selected model's verified limits. Tool calling is disabled by default; put only model IDs verified to support OpenAI function calling in `lmm.toolModels` before using those models with Copilot Agent tools.

For Zed, install the npm package/archive with Node 22.19 or later and the `zed` command in PATH. Run `lmm-zed login`, quit any existing Zed process, then run `lmm-zed start`. This loads the authorized catalog, updates Zed's LMM provider settings with a backup, starts the local bridge and launches Zed with its generated local credential. Keep the bridge terminal running. The local credential is separate from the LMM OAuth credentials.

`lmm-zed settings` prints the generated fragment without writing it; `lmm-zed configure` updates and backs up settings without launching Zed. These commands preserve unrelated settings and existing LMM model metadata. Inspect the catalog with `lmm-zed catalog` and supply verified model overrides using `--models models.json`, following the [bridge README](../packages/zed-lmm-provider/README.md).

Version 0.1.0 can be installed from locally built artifacts:

```sh
code --install-extension packages/vscode-lmm-provider/lmm-copilot-provider-0.1.0.vsix
npm install --global ./packages/zed-lmm-provider/tokennotincluded-zed-lmm-provider-0.1.0.tgz
```

The Zed bridge can forward OpenAI chat-completions and Responses streams for supported catalog routes; Anthropic-only routes are omitted. The catalog does not establish context windows or tool capabilities. Automatic configuration uses compatibility defaults of 32,768 context tokens and 4,096 output tokens, with tools and images disabled. Review those limits and enable capabilities only after verification. Choosing a model still requires matching the route and protocol supported by that model; OAuth authorization alone does not make every model compatible with every editor feature.

## OAuth and production prerequisites

The API server must register the public clients `lmm-vscode` and `lmm-zed`. Each requests exactly `catalog:read balance:read usage:read models:invoke`, together with the group snapshot explicitly displayed during consent. The editor clients do not grant built-in MCP or marketplace scopes. Their credentials and revocation are isolated from the other editor and other LMM clients.

Both adapters use the LMM authorization-server discovery document and a loopback callback with PKCE. The access token is used with the OAuth catalog and relay endpoints, rather than the ordinary API-key endpoints. See the [server OAuth contract](../apps/api-go/service/oauth_contract.md) for the authoritative endpoint and authorization rules.

The client registrations in this repository are not proof that the production server has been deployed. Until a server containing both registrations is deployed and OAuth is enabled, production login may reject these clients. A successful local build, mocked OAuth exchange or packaged artifact does not establish live login, model invocation or billing acceptance.

## Validation and release evidence

The [editor providers workflow](../.github/workflows/editor-providers.yml) checks the adapters and the focused server registration tests, and preserves installation artifacts for review. It does not publish to a marketplace.

Run the corresponding commands from each package directory:

```sh
# packages/vscode-lmm-provider
npm ci --ignore-scripts
npm test
npm run test:host
npm run package

# packages/zed-lmm-provider
npm ci --ignore-scripts
npm run typecheck
npm test
npm run build
npm run pack:check
npm pack --ignore-scripts
```

The VS Code test command compiles and typechecks the extension before running tests. `test:host` needs the `code` and `rg` executables and a running display; use `LMM_VSCODE_BIN` to select another installed Code executable. On Linux CI, the workflow installs the official VS Code 1.104.0 package with a pinned checksum and runs the harness under Xvfb. The harness verifies activation, provider registration, commands and silent unauthenticated discovery in an isolated editor profile. It does not exercise live OAuth or paid model requests.

The Zed tests execute source TypeScript, while the published package contains built JavaScript so it can run from an installed npm package. CI also opens the VSIX and npm archive to verify their runtime entry points and exclude tests and dependency trees.

The focused server check, from `apps/api-go`, is:

```sh
GIN_MODE=release go test ./router ./service ./oauthserver -run 'TestOAuth(Editor|CLI|OpenCode|HTTPDiscovery)|TestCodewhale' -count=1
```

Before calling a release production-ready, verify browser authorization, restart and credential restoration, model discovery, a streamed model request, cancellation, token refresh and revocation against the deployed server. Check the resulting account usage and wallet settlement. Editor UI integration must also be verified in the supported editor version; unit tests alone do not exercise the running editor.

Publication status must be recorded separately from packaging and deployment. A VSIX artifact is an installable package, not evidence of a Visual Studio Marketplace or Open VSX listing. An npm archive for the Zed bridge is not a Zed extension-store listing.

## Publishing the reviewed artifacts

VS Code version 0.1.0 was published as a preview on 2026-10-02: [LMM for Copilot on Visual Studio Marketplace](https://marketplace.visualstudio.com/items?itemName=LIghtJUNction.lmm-copilot-provider). Its public listing and installation command were verified. Production OAuth client registration is still pending deployment; this publication does not establish live model access. Open VSX and the Zed npm package have not been published.

The VS Code manifest uses publisher ID `LIghtJUNction`, matching the existing publisher owned by the authenticated Visual Studio Marketplace account. The extension ID is `LIghtJUNction.lmm-copilot-provider`. Publishing requires registry authentication. Open VSX namespace access and npm publish access to the `@tokennotincluded` scope are separate prerequisites. Source publication through a pull request is a separate step.

For [Visual Studio Marketplace](https://code.visualstudio.com/api/working-with-extensions/publishing-extension), authenticate with the publisher account using `npx vsce login LIghtJUNction` or configure the documented Microsoft Entra identity, then publish the reviewed VSIX from its package directory:

```sh
npx vsce publish --packagePath lmm-copilot-provider-0.1.0.vsix
# With an already configured Microsoft Entra identity:
# npx vsce publish --azure-credential --packagePath lmm-copilot-provider-0.1.0.vsix
```

After the Marketplace listing is published, install it with `code --install-extension LIghtJUNction.lmm-copilot-provider`.

For [Open VSX](https://github.com/eclipse-openvsx/openvsx/wiki/Publishing-Extensions), the account must have accepted the publisher agreement and have access to namespace `LIghtJUNction` to publish this same VSIX. Supply its registry token through `OVSX_PAT`, then publish the artifact:

```sh
npx --package ovsx ovsx publish lmm-copilot-provider-0.1.0.vsix
```

For the Zed bridge, complete `npm login` with an account permitted to publish the scope, then publish the reviewed archive from its package directory using [npm's public-package command](https://docs.npmjs.com/cli/v11/commands/npm-publish/):

```sh
npm publish ./tokennotincluded-zed-lmm-provider-0.1.0.tgz --access public
```

After publishing, verify the public registry entry and download/install that exact version. Record those URLs separately from the production OAuth and editor runtime acceptance results.
