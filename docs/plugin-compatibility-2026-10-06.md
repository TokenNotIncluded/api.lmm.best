# Plugin compatibility update — 2026-10-06

## Scope and pinned sources

This update changes plugin Git submodules and installer support, not the Go/Rust service or the unrelated `apps/coweft` application. All pins refer to committed source, not copied vendor directories.

| Submodule | Provider version | Host target | Pinned revision |
| --- | --- | --- | --- |
| `packages/pi-lmm-provider` | `0.1.0-alpha.4` | Pi `1.0.4`, retaining older supported hosts | `e38af2a4e96500b29c473a9f6395911bf8a9cb55` |
| `packages/dsh-lmm-provider` | `0.1.0-alpha.6` | DSH `0.2.0-rc.2` and `0.2.1-alpha.1` | `5add9949fd7bbc8c2c9f6dead808f1ed0447791e` |
| `packages/codewhale-lmm-provider` | `0.1.0-alpha.2` | npm Codewhale `0.10.0`; reviewed `0.10.1` development source | `adad64b7ac77ec997b5be4662c6b6ccf36998357` |
| `packages/opencode-lmm-auth` | `0.1.0` (unchanged) | OpenCode `1.18.34` (already current) | `e589bbcc1703be5ae5de22473d5df42f16d38cfd` |
| `packages/lmm-scripts` | Git-pinned | Updated Codewhale provider and verified bootstrap launchers | `879e6bcaa566c45777c29c77e63fb5c9ad0ad620` |

DSH's nested `vendor/pi-lmm-provider` references the same Pi commit as the parent project. Its own native `pi-ai` runtime stays on the DSH-supported `0.87` line; it is not replaced with the standalone Pi 1.0 SDK.

## Compatibility fixes

- Pi's development dependencies and lockfile use 1.0.4. The native loader test checks all four registered LMM commands, including `lmm-cache`. CI retains the 0.86.1, 0.87.1 and 0.99.2 baselines as well as 1.0.4/latest.
- DSH prereleases are explicitly admitted by npm semver. `scripts/select-host.mjs` resolves the CLI release tag once, then installs the matching service packages and official Cordis peer instead of mixing each library's independently published `latest` tag. Source and packed plugins are tested against the actual Web host.
- Codewhale's ambient `CODEWHALE_PROFILE` and `DEEPSEEK_PROFILE` cannot override the generated provider configuration. Forwarded `--profile` is rejected before credential access. Native `-p` and `-c` remain available. The parent process environment is not mutated.
- The Bash and PowerShell Codewhale bootstraps are bound to one immutable helper revision and the SHA-256 of the exact helper bytes. Integrity checks are not bypassed.
- OpenCode already targets 1.18.34; no speculative version bump is necessary. The parent CI revalidates its existing source, generated distribution and native host integration.

## Validation and limits

The Pi upgrade validation passed type checking, 118 tests, package inspection and the official 1.0.4 RPC loader. DSH upgrade validation passed both stable and alpha Web hosts, including local OAuth/RPC/catalog/streaming/sign-out fixtures and a packed alpha plugin. Codewhale passed 38 tests and official native config/catalog discovery against a local OAuth fixture. Installer validation passed Node/Python contract suites, Bash syntax, ShellCheck and PowerShell tests.

The `Plugin compatibility` workflow separately checks the exact submodule pins in this parent project. Child PR workflows provide the wider host/OS matrices; their current check results, rather than this dated note, determine merge readiness.

Native smoke tests do not establish production OAuth acceptance, TUI/Desktop GUI acceptance, billing accuracy or Windows credential ACL support. No production login, billable inference, package publication or deployment is part of this update. Codewhale remains a companion adapter rather than a native provider-hook plugin. Its development source version must not be confused with the npm stable release.

## Related changes

- https://github.com/TokenNotIncluded/pi-lmm-provider/pull/13
- https://github.com/TokenNotIncluded/dsh-lmm-provider/pull/4
- https://github.com/TokenNotIncluded/codewhale-lmm-provider/pull/1
- https://github.com/TokenNotIncluded/lmm-scripts/pull/9

Review the child changes before merging the parent pin update. Source version bumps do not publish npm packages or GitHub release assets; existing release download links must not be changed to nonexistent assets.

After updating a checkout to the accepted parent commit:

```sh
git submodule sync --recursive
git submodule update --init --recursive
```
