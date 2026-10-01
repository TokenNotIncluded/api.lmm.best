# OpenCode LMM OAuth plugin

`packages/opencode-lmm-auth` is a Git submodule of
[`TokenNotIncluded/opencode-lmm-auth`](https://github.com/TokenNotIncluded/opencode-lmm-auth).
Source, compiled distribution, host integration tests and package metadata belong
to that independent repository. The parent pins its revision and registers the
public native OAuth client `lmm-opencode`.

The installer uses the official latest OpenCode release with Node.js 22.19 or
newer. OpenCode 1.18.34 is a regression baseline, not an installation version lock.
Install through
the OpenCode entry in the [public scripts page](https://api.lmm.best/scripts), or
use `opencode.sh` / `opencode.ps1` from `packages/lmm-scripts`. The installer
installs the host and the latest published, checksum-verified plugin, preserves existing
configuration and other plugins, and backs up configuration before changing it.
Rerunning the installer updates the managed plugin entry. It does not sign in
automatically. Restart OpenCode, run `opencode auth login`,
and select **LMM → Sign in with LMM (OAuth)**.

The consent profile is `catalog:read balance:read usage:read models:invoke`, plus
explicitly selected account groups. PKCE S256 and a state/issuer-bound loopback
callback protect sign-in. The plugin uses the host's credential store and rotates
refresh credentials with a local replay journal. It requests no MCP or admin
permissions. Calls use normal account pricing.

Models come from the authorized catalog. Chat Completions, Responses and
Anthropic Messages use their corresponding host SDK and exact account group;
unsupported capability profiles or billing units are omitted. Credentials go
only to the configured HTTPS issuer, with redirects rejected.

After a scripts repository update is merged, use **运维 → 脚本 → 拉取更新** in
LMM settings. That publishes root `.sh` / `.ps1` files; their shared installer is
downloaded from its immutable GitHub revision. Changing a submodule locally does
not update the public scripts page.

Run the independent package checks after initializing the submodule:

```sh
git submodule update --init -- packages/opencode-lmm-auth
cd packages/opencode-lmm-auth
npm ci --ignore-scripts
npm run build
npm run typecheck
npm test
npm run pack:check
# Requires the supported official OpenCode host and OpenSSL:
npm run test:host
npm run test:integration
```

The integration fixture verifies OAuth and streamed responses in the actual
OpenCode host. Production login and billed inference need separate verification
against the deployed server; fixture success alone does not establish those.
