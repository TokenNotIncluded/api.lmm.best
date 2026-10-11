# Native Linux packages (preview)

One build-machine entry point converts verified portable archives into DEB, RPM,
APK and Arch packages. This is packaging support, not a declaration that the WIP
model, payment or database migration paths are ready for production.

## Components

| Selector | Installed program or data | Runtime dependency on another LMM package |
| --- | --- | --- |
| `core` | `/usr/bin/lmm-core` and optional service definition | None; PostgreSQL is configured separately |
| `extensions` | `/usr/bin/lmm-extensions` and external policy templates | None; remote core configured only when needed |
| `cli` | `/usr/bin/lmm` | None |
| `web` | `/usr/share/lmm/web/` | None; no reverse proxy configuration is installed |

The product has three executable programs: `lmm`, `lmm-core` and `lmm-extensions`.
Database initialization and the first administrator are explicit `lmm-core init-db`
and `lmm-core bootstrap-user` subcommands. `init-db` installs the complete core
schema in one transaction; ordinary startup verifies the complete contract
and live catalog fingerprint. These subcommands are never run during installation
or ordinary service startup. Installing extensions does not install or restart
core. Each package is independent; the user CLI is installed only by selecting
`cli`. No package installs a local PostgreSQL server, Docker or a language toolchain.

## Distribution and architecture rules

| Format | Target family | Service manager | Binary requirements |
| --- | --- | --- | --- |
| `deb` | Debian / Ubuntu and compatible derivatives | systemd 247+ | Static, or supported glibc libraries with detected minimum |
| `rpm` | Fedora / RHEL 9+ / Rocky / AlmaLinux / openSUSE | systemd 247+ | Static, or supported glibc libraries with detected minimum |
| `apk` | Alpine | OpenRC | Verified static ELF only; ordinary glibc archives are rejected |
| `archlinux` | Arch Linux / compatible derivatives | systemd 247+ | Static, or supported glibc libraries with detected minimum |

Binary architectures are `amd64` and `arm64`; nFPM maps them to the package
manager's names. Arch Linux ARM is a separate distribution: creating an aarch64
Arch package is not evidence of an official Arch Linux ARM installation test.
Web packages are architecture-independent. macOS and Windows retain portable CLI
archives; this builder deliberately refuses them.

The builder uses `readelf`, never `ldd` or execution, to check every packaged
binary. A binary compiled against glibc 2.36 must declare that minimum; changing
its filename cannot make it work on a glibc 2.34 system. Unknown libraries and
loaders are errors. Use an actually compiled and checked Rust musl static binary
for broad Linux / Alpine coverage, and `CGO_ENABLED=0` for the Go host. Static
linking is not proof of lower RAM or compatibility with every kernel/CPU.

These are upstream preview packages, not packages accepted into any distribution's
official repository. Signed publication, vulnerability review, distro lifecycle
support and end-to-end business qualification remain separate release gates.

## Build without root

Build dependencies: Python 3.11+, GNU `readelf` (binutils), and the reviewed nFPM
version in `packages.json`. Nothing here downloads or upgrades them. CI pins the
nFPM release archive SHA-256. Work only on a build machine, not a 1-core service node.

```sh
mise exec -- python3 packaging/linux/build.py \
  --archive out/distributions/lmm-core-0.1.0-preview-linux-amd64.tar.gz \
  --sha256 "$TRUSTED_ARCHIVE_SHA256" \
  --revision "$VERIFIED_SOURCE_SHA" \
  --component core --format deb
```

Repeat with `extensions`, `cli` or `web` and its own archive.
`--format rpm|apk|archlinux` changes the native format.
`--release 2` changes the packaging revision without inventing a new source
version. Prerelease labels `alpha`, `beta`, `rc` and `preview`, optionally followed
by `.number`, are converted to each package manager's version rules. The original
version remains in build evidence.

The source SHA and expected archive checksum are mandatory. A checksum supplied
by the same untrusted download is not proof of publisher identity. Validate it
against your trusted build record or signed release metadata before packaging.
The source archive is copied and verified before use; extra files, missing files,
wrong components, wrong architectures and changed payloads are rejected.

Output contains the package, `.sha256` and `.buildinfo.json`. The package installs
its own `BUILDINFO.json` with source identity, library requirements and payload
hashes. Files are staged privately; existing outputs are never overwritten.
The native formats are built by nFPM, not by a new in-house package encoder.
Build output is unsigned by default. Sign with the approved distribution/repository
key before publication. Do not teach production users to bypass signature checks;
unsigned-install switches in tests are confined to disposable test containers.
The CLI's Linux credential storage requires the user's Secret Service session;
packaging a static executable does not provide a desktop session or keyring daemon.

## Installation does not activate services

The service entry points are `/etc/lmm.conf.d/core.toml` and
`/etc/lmm.conf.d/extensions.toml`. Both templates pass the exact absolute file
through `--config`; they never scan a configuration directory or source TOML as
shell. Unknown, duplicate or missing required settings must fail before bind.
See the canonical [filesystem and configuration contract](../../docs/filesystem-layout.md).

On systemd systems, templates use static `lmm-core` and `lmm-extensions` users,
private runtime/state directories and the journal. The package provides
`/usr/lib/sysusers.d/<program>.conf`; the operator explicitly provisions accounts
with `systemd-sysusers` before starting a service. No installation hook runs it.
Current templates do not use `LoadCredential` or dynamic users: the TOML parser
does not expand `%d`, and a root-only file cannot simply be handed to an
unprivileged process. Separately provision service-owned 0600 secrets:

- Core: `/etc/lmm.conf.d/secrets/core/database-url`, containing only a restricted runtime URL for the explicitly selected isolated database, never the installation-owner URL.
- Extensions: `/etc/lmm.conf.d/secrets/extensions/host-token`, independently generated.

The shipped TOML is non-secret and root-owned 0644. Secrets and state are not
package files. Package upgrades retain locally modified TOML with
`config|noreplace`; inspect package-manager merge candidates. Use
`systemctl daemon-reload`, then explicitly start or restart only the intended
service after checking accounts, configuration and readiness.
The default service binds loopback. Configure the edge listener separately.
No package hook enables a unit, restarts a daemon, downloads a file, creates a
secret, initializes/migrates a database, creates database roles, grants privileges,
clears data or touches the old Go service.

Provision the installation owner and restricted runtime separately, with explicit
offline GRANTs before starting the service. Keep owner credentials out of the
service's TOML or credential file. The runtime must not own application objects, switch to
an owner role or directly write immutable ledger tables; see the
[runtime authority check](../../services/core/src/runtime_database.rs) and
[schema contract](../../services/core/schema/README.md). The templates load the URL
provided by the operator and do not provision these roles. Docker uses its own
explicit owner/runtime mounting and authorization process; reading a Compose
file is not evidence that this process or a native installation passed.

Alpine packages create only their own non-login system account. OpenRC service
scripts pass the same fixed TOML path, without executing its content. Private
files must be owned by the matching service user, mode 0600 (or readable 0400);
group-readable 0640 secrets are not accepted. Explicitly use
`rc-service <package> start`; adding it to a boot runlevel
is the operator's decision. OpenRC does not inherit the systemd memory limit:
configure cgroup limits separately and measure actual usage.

Default state and runtime directories are `/var/lib/lmm/<component>` and
`/run/lmm/<component>`, mode 0700. Core RPC is optional. For cross-user IPC, the
operator explicitly enables `rpc.shared_group_id` with the actual numeric GID
of `lmm-rpc`, and reviews the supplied
`/usr/share/doc/<program>/rpc.service.conf.example`. Core uses that primary group
and an IPC-only directory mode 0750; extensions gain supplementary group access.
The socket is 0660, while lock files and each service's separate token remain
0600. No shared-group membership or unit override is installed automatically.
OpenRC needs equivalent explicit service-account/group configuration. Different
UID connectivity requires a separate process test, not a template-only claim.

Removal never deletes operator-created credential files, data, audit history or
the remote database. Stop the intended service explicitly before final removal.
Package upgrades replace disk files but do not prove a running process has loaded
them. Core's 256 MiB systemd limit is a ceiling, not a measured footprint. With
restricted privileges, complete schema and configured identity, readiness can be
200 while models remain disabled; model readiness then stays 503. Optional model
and financial interfaces require their own explicit configuration and acceptance.
A single service restart can still break a
stream; this package change does not claim zero-interruption deployment.

Identity-only ETL was verified using an isolated restored snapshot and a separate
local core target, covering 888 legacy users at completion. Source funds, API keys
and sessions were not migrated, and production was not written. This does not
prove complete business migration, current user counts, Docker deployment or
native package installation on every supported distribution.

## Validation

```sh
mise exec -- python3 -B packaging/linux/test_build.py -v
mise exec -- python3 -B packaging/linux/test_native.py -v   # requires reviewed nFPM
```

`test_build.py` uses synthetic native ELF fixtures to test packaging contracts,
not product runtime behavior. `test_native.py` builds all formats, inspects DEB
contents and checks repeatability. The read-only `linux-packages.yml` workflow
builds actual `lmm`, `lmm-core` and `lmm-extensions` binaries for two architectures and tests package
installation in disposable distro containers. Record the exact run and CPU
architecture before claiming any installation matrix passed. No release or
production deployment is part of that workflow.

Primary references: nFPM's pinned `nfpm.go`/`files` schema, Debian Policy chapter
10 (configuration preservation and `/usr` paths), systemd.exec (directory and
credential ownership), systemd-sysusers, and OpenRC's service-script guide. The implemented minimum for
systemd service packages is 247; older systems need a separately qualified unit.
