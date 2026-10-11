# Packaging

The fresh-install microservice branch uses one component manifest and one native
Linux package builder. Runtime nodes receive prebuilt files, not source trees,
Python, Node, Go, Rust, compilers or experiments.

| Path | Owner and purpose |
| --- | --- |
| `distribution.json` | Canonical component files for independent portable archives |
| `linux/` | Native DEB, RPM, APK and Arch packages; service assets, validation and tests |
| `aur/` | Existing site's signed `lmm-api-web-bin` release; not a microservice installer |
| `common/`, `cluster/`, `local/` | Existing-site resources retained at stable paths for their current consumers |

Start with [Linux packages](linux/README.md). Portable archive construction remains
in `scripts/build-distribution.py`; native packages consume and verify those
archives rather than maintaining a second list of compiled files.

Do not repoint old package names, units, database maintenance scripts or AUR
recipes to `lmm-core` or `lmm-extensions`. These are different programs and data
contracts. Old resources are not recursively copied into new native packages.
Retire their paths together with their consumers, not while existing signed
release workflows still use them.
