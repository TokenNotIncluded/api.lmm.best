# Plugin compatibility update — 2026-10-06

## Scope and pinned sources

This update changes plugin Git submodules and installer support, not the Go/Rust service or the unrelated `apps/coweft` application. All pins refer to committed source, not copied vendor directories.

| Submodule | Provider version | Host target | Pinned revision |
| --- | --- | --- | --- |
| `packages/pi-lmm-provider` | `0.1.0-alpha.4` | Pi `1.0.4`, retaining older supported hosts | `e38af2a4e96500b29c473a9f6395911bf8a9cb55` |
| `packages/dsh-lmm-provider` | `0.1.0-alpha.6` | DSH `0.2.0-rc.2` and `0.2.1-alpha.1` | `da11a41b8c05fc065d0acecf22e34c7af1d29f65` |
| `packages/codewhale-lmm-provider` | `0.1.0-alpha.2` | npm Codewhale `0.10.0`; reviewed `0.10.1` development source | `62eb05b40f1dda7b844b5430ad2b735e66508aa0` |
| `packages/opencode-lmm-auth` | `0.1.0` (unchanged) | OpenCode `1.18.34` (already current) | `e589bbcc1703be5ae5de22473d5df42f16d38cfd` |
| `packages/lmm-scripts` | Git-pinned | Updated Codewhale provider and verified bootstrap launchers | `ed2a44be1146a68d8cff3950a8a67120f02ed1f1` |

DSH's nested `vendor/pi-lmm-provider` references the same Pi commit as the parent project. Its native `pi-ai` runtime stays on the DSH-supported `0.87` line; it is not replaced with the standalone Pi 1.0 SDK.

## Compatibility fixes

- Pi's development dependencies and lockfile use 1.0.4. Its native loader checks all four LMM commands, including `lmm-cache`. CI retains 0.86.1, 0.87.1 and 0.99.2 alongside 1.0.4/latest.
- DSH prereleases are explicitly admitted by npm semver. `scripts/select-host.mjs` resolves the CLI release tag once and selects matching service packages and the official Cordis peer.
- DSH authorization, home paths and pi-ai adapter packages are host-owned peers, not independently installed runtime dependencies. A packaged-plugin regression exposed alpha services shadowing stable services: DSH correctly disabled the incompatible services and the LMM auth route returned 404. The dependency declarations and lockfile are fixed; a regression test prevents reintroducing host-owned runtime dependencies. No compatibility exemption is granted.
- Codewhale's ambient `CODEWHALE_PROFILE` and `DEEPSEEK_PROFILE` cannot override the generated provider configuration. Filtering is case-insensitive, including Windows spellings such as `CodeWhale_Profile`. Forwarded `--profile` is rejected before credential access; native `-p` and `-c` remain available. The parent process environment is unchanged.
- The native Codewhale test overrides uppercase and lowercase proxy variables so inherited lowercase settings do not weaken its local-only test setup.
- Bash and PowerShell Codewhale bootstraps use the same immutable helper revision and the SHA-256 of its exact bytes. Integrity checks are not bypassed. Both install the Windows isolation fix pinned above.
- OpenCode already targets 1.18.34. Its source, generated distribution and native host integration are revalidated without a speculative version bump.

## Validation and limits

Pi passed 118 tests, type checking, package inspection and the official 1.0.4 RPC loader; its full legacy/latest matrix also passed. DSH passed 14 unit tests and actual stable/alpha Web hosts, including source and packed-plugin installs, local OAuth/RPC/catalog/streaming/sign-out fixtures. The initial Codewhale update passed 38 tests and official native config/catalog discovery against a local OAuth fixture. The Windows follow-up adds exhaustive casing and real child-process regression tests; both passed on Windows with Node 22 and 24. Installer validation includes Node/Python contracts, Bash syntax, ShellCheck and PowerShell; the Codewhale/OpenCode cross-platform installer workflows also passed for the initial update.

The read-only `Plugin compatibility` workflow validates the exact parent Git pins, including the DSH tarball rather than source loading alone. Current child and parent CI results, not this dated note or earlier revisions' results, determine merge readiness. The new Codewhale and installer pins must pass their full checks before acceptance.

These checks do not establish production OAuth acceptance, TUI/Desktop GUI acceptance, billing accuracy or Windows credential ACL support. No production login, billable inference, package publication or deployment is part of this update. Codewhale remains a companion adapter rather than a native provider-hook plugin; its development source version is not the npm stable release.

## Related changes

- https://github.com/TokenNotIncluded/pi-lmm-provider/pull/13
- https://github.com/TokenNotIncluded/dsh-lmm-provider/pull/4
- https://github.com/TokenNotIncluded/codewhale-lmm-provider/pull/1
- https://github.com/TokenNotIncluded/codewhale-lmm-provider/pull/2
- https://github.com/TokenNotIncluded/lmm-scripts/pull/9
- https://github.com/TokenNotIncluded/lmm-scripts/pull/10

Review and merge the child changes before merging the parent pin update. Source version bumps do not publish npm packages or GitHub release assets. Existing release download links must not point to nonexistent assets.

After updating a checkout to the accepted parent commit:

```sh
git submodule sync --recursive
git submodule update --init --recursive
```
