# DSH plugin compatibility update — 2026-10-10

The `packages/dsh-lmm-provider` submodule now tracks [dsh-lmm-provider PR #5](https://github.com/TokenNotIncluded/dsh-lmm-provider/pull/5), merged as commit `f4589c40e638e9816a2bcc61f7acd03fb3106ce7`.

- Plugin source version: `0.1.0-alpha.7`
- Verified DSH hosts: `0.2.0-rc.2`, `0.2.1-alpha.1`, `0.2.1-alpha.2` and the then-current npm `latest` release.
- Upstream `0.2.1-alpha.2` switched `@earendil-works/pi-ai` from 0.87.x to 1.x. The LMM plugin now declares pi-ai as a host-provided peer rather than installing a second, incompatible transcript type. Its test setup selects the SDK from the active DSH host.
- Checks: TypeScript, plugin unit tests, npm tarball contents, authentic DSH Web process startup, account-scoped browser OAuth fixture, protected RPC, catalog, streaming model call, logout, and installing the built plugin tarball against each host. See [successful DSH CI run](https://github.com/TokenNotIncluded/dsh-lmm-provider/actions/runs/37968296755).
- Plugin submodule `vendor/pi-lmm-provider` remains pinned independently; this change does not update Pi, OpenCode, Codewhale, or `packages/lmm-scripts`.

After checking out this parent revision, synchronize Git links and nested submodules:

```sh
git submodule sync --recursive
git submodule update --init --recursive
```

**Distribution note:** This sync changes the committed plugin source only. It does **not** publish `0.1.0-alpha.7` to npm, create a GitHub release asset, update running DSH installations, perform production OAuth, or deploy `api.lmm.best`. Install from source until a verified packaged release is available. The old npm / GitHub release instructions must not be relabeled as alpha.7.
