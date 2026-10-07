# Shaders in LMM Forge

The Web app uses the official `shaders@4.0.0` package, pinned in its existing Bun workspace lock. Home, store, tool market and assistant entry points share `ForgeShaderSurface`; they do not create an effect for each product, tool, amount or table row. Existing homepage 3D models and scroll transitions remain separate.

The shared composition comes from our original **Forge Ambient** effect in the connected Shaders project. The official CLI-installed source is parsed offline into a small JSON configuration. Home, store, tools, ecosystem, future and assistant variations read its shape, speed, seed and grid settings, while colors use the app's semantic theme colors, including light, dark and custom presets. The browser needs no Shaders account, remote preset fetch, runtime API key or telemetry.

## Runtime

`src/components/shaders/forge-shader-surface.tsx` is the React host. It lazily imports the official public `shaders/core` renderer after visibility, motion, capacity and native GPU-adapter checks. A browser that exposes WebGPU but denies an adapter keeps the static composition without downloading the SDK. The SDK's `<Shader>` React root currently does not expose resolution or frame rate controls; passing invented `dpr` or `frameloop` props would only put attributes on its HTML element. The owned React host uses the supported renderer API instead.

- At most one shader render context across all mounted routes; this budget does not include the existing homepage Three.js renderer. Other visible surfaces keep their static composition until capacity is available. This conservative limit also avoids concurrent WebGPU context loss observed on the actual review browser.
- Canvas dimensions are bounded before initialization to a maximum first allocation of 1024 × 640 pixels. The runtime then uses 80% resolution, at most 24 frames per second, or 18 for assistant intent.
- Editor settings pass a literal-only parser and bounds check. Route variations retain at most eight gradient points and 48 grid cells; editor root attributes, executable expressions and new layer types require an explicit adapter change.
- Hidden tabs, offscreen surfaces, reduced motion, data saving, homepage pause and inactive entry points stop the animation and release their context. Assistant effects run only on pointer or keyboard intent.
- A CSS composition remains visible when WebGPU is unavailable, denied, lost or unable to compile. Shader readiness never gates text, navigation, search, checkout or keyboard access.
- Cleanup runs immediately and again after asynchronous GPU acquisition settles; the capacity lease is retained until then. Canvas contexts are unconfigured. The SDK's shared default device is not forcibly destroyed by one consumer.

Do not add these surfaces to financial states, prices, order statuses or each list item. Keep new effects behind the same host, preserve its context limit and verify actual reduced motion and WebGPU fallback in a browser.

## Account and editor sync

Run commands from `apps/web`, using the locally pinned package:

```sh
bun run shaders:connect
bun run shaders:install
bun run shaders:update
bun run shaders:sync
bun run shaders:check
bun run shaders:open
```

`shaders:connect` opens the official browser sign-in and project picker. The codebase is connected to the **LMM Forge** project `948b250c-c294-46dd-8d12-06700b1583b7`, created and selected through that actual CLI flow. `shaders.config.ts` records the project, React and the output directory. **Forge Ambient**, shader `4740184`, was saved in that account and installed by the actual CLI. Its unmodified React source is `src/components/shaders/generated/ForgeAmbient.tsx`; the real `shaders.lock.json` records its ID and source hash.

The install/update scripts run `scripts/sync-shader-compositions.mjs` afterward. It uses a pinned Babel parser, verifies the CLI hash, accepts the supported literal MeshGradient/Grid layers without executing TSX, and writes `forge-ambient.generated.json`. The owned renderer consumes that JSON; it never mounts the generated `<Shader>` root. Editor colors are intentionally replaced by the active site palette, and each route applies relative shape/motion variations. Changes to source or lock require a fresh sync. Production builds run only a local hash/configuration check and fail if the derived configuration is stale; they never contact the editor.

The two generated files are excluded from automatic formatting, and the exact official TSX file is excluded from copyright rewriting, so CLI source tracking stays valid. `update` preserves locally changed files unless `--force` is explicitly used. Do not run `--force` over the owned host. CLI sync is a developer operation, not a browser runtime dependency.

Browser credentials belong in the CLI's private `~/.shaders/credentials.json`, never in the repository. For unattended editor sync, use a private environment-provided `SHADERS_API_KEY`; never put it in source, arguments, browser bundles or committed MCP configuration. Library presets may require Shaders Pro; our local runtime compositions do not use a paid preset or watermarked preview.

## Codex MCP

The official CLI has installed a project-scoped `.codex/config.toml` declaring the HTTP server `https://shaders.com/mcp`. It contains no credential. Open a new trusted Codex session for this repository to load the configuration, then complete the MCP client's browser OAuth when prompted. Installing configuration, authenticating MCP, connecting the CLI project, and installing a remote shader are separate actions; none implies that the others have happened.

To reproduce only the project configuration:

```sh
# Repository root, not apps/web; never add --global.
bun node_modules/shaders/dist/cli.js install-mcp -a codex -y
```

Official references: [React quickstart](https://shaders.com/docs/guide/react/quickstart), [CLI guide](https://shaders.com/docs/guide/cli), [MCP guide](https://shaders.com/docs/guide/mcp), [complete component reference](https://shaders.com/llms-full.txt).
