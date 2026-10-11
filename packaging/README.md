# Packaging

The fresh-install microservice branch uses one component manifest and one native
Linux package builder. Runtime nodes receive prebuilt files, not source trees,
Python, Node, Go, Rust, compilers or experiments.

| Path | Owner and purpose |
| --- | --- |
| `distribution.json` | Canonical component files for independent portable archives |
| `linux/` | Native DEB, RPM, APK and Arch packages; service assets, validation and tests |
| `../config/core.example.toml`, `../config/extensions.example.toml` | Non-secret service configuration examples included in their own archives |

Start with [Linux packages](linux/README.md). Portable archive construction remains
in `scripts/build-distribution.py`; native packages consume and verify those
archives rather than maintaining a second list of compiled files.

New native service packages install fixed TOML files at
`/etc/lmm.conf.d/{core,extensions}.toml` with `config|noreplace`; templates pass
that exact path through `--config`. Read-only Web and policy resources use
`/usr/share/lmm/web/` and `/usr/share/lmm/legal/`. Secrets, persistent state,
runtime IPC, logs and client directories follow the canonical
[filesystem layout](../docs/filesystem-layout.md). Secrets and backup contents
are never package inputs. Existing installations need an explicit reviewed
conversion; packages do not move old data or execute old shell configuration.

The product exposes exactly three programs: `lmm` is the client, `lmm-core` owns
identity, billing and model routing, and `lmm-extensions` runs Go business modules.
Core setup is explicit through `lmm-core init-db` and `lmm-core bootstrap-user`;
there is no separate administrator or deployment executable. Web output is static
data. The legacy monolith's AUR, shared, cluster and local package resources are
removed from this branch together with their retired consumers. Existing installed
versions and historical signed packages retain their original source revision;
new packages do not upgrade or rewrite them.
