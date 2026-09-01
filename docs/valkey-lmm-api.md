# Dedicated Valkey for lmm-api

Production uses a dedicated native Valkey instance, separate from any existing
`valkey.service` on `127.0.0.1:6379`.

## Layout

| Resource | Value |
| --- | --- |
| Unit | `valkey-lmm-api.service` |
| Listener | `127.0.0.1:6380` only |
| Configuration | `/etc/valkey/lmm-api.conf` (`0640 root:valkey`) |
| ACL | `/etc/valkey/lmm-api.acl` (`0640 root:valkey`) |
| Application environment | `/etc/lmm-api/valkey.env` (`0600 root:root`) |
| Persistent state | `/var/lib/valkey-lmm-api` |
| Cache memory | 64 MiB, `noeviction` |
| Unit limits | `MemoryHigh=80M`, `MemoryMax=112M`, `MemorySwapMax=32M` |
| Persistence | AOF, `appendfsync everysec` |
| Kernel policy | `vm.overcommit_memory=1`; THP `madvise` |

Immutable service, configuration, sysctl, and tmpfiles inputs live under
`packaging/common/valkey`. The retired root deployment scripts are not operator
entry points.

## Security and data role

PostgreSQL is the persistent authority. Valkey carries rebuildable cache plus
security-sensitive session, revocation, and rate-limit state. `noeviction`
causes writes to fail rather than silently discarding security state. The
default user is disabled; the `lmm-api` ACL user is denied `@dangerous`.
Protected mode and loopback binding provide independent containment.

The connection URL is read only from protected configuration. Never print,
commit, package, or pass its credential on a command line. Go and Rust must use
the same endpoint, key contract, and `CRYPTO_SECRET` before any shared route
ownership.

## Package and CLI verification

Provider/runtime packaging installs immutable inputs. Production inspection and
health operations enter only through the verified public symlink:

```text
/usr/bin/lmm-api doctor
/usr/bin/lmm-api status
/usr/bin/lmm-api deploy production status ...
```

Before application mutation, verify:

- `valkey-lmm-api.service` is active with the expected PID and cgroup limits;
- only the expected loopback listener owns port 6380;
- the pre-existing 6379 service/PID/listener is unchanged;
- authenticated `PING` succeeds without exposing credentials;
- the application ACL cannot run dangerous administration commands;
- both backend providers resolve the same sanitized endpoint identity.

Package installation or a CLI preflight must fail closed on unsafe modes,
symlinks, ownership mismatches, invalid directives, listener collisions, or
ambiguous runtime state.

## Changes and rollback

Valkey configuration changes are part of the marker-owned production
transaction. Before the first live mutation, record exact prior presence,
bytes, type, ownership, mode, service enablement/activity, kernel values, and
checksums. Publish files atomically and verify the isolated instance without
restarting the unrelated 6379 service.

There is no rollback timer and no automatic rollback. A post-mutation failure
becomes `ROLLBACK_REQUIRED` and retains the transaction lock/evidence. An
explicit operator rollback restores only the selected verified configuration
snapshot and prior service/kernel state, then re-runs authenticated health and
listener-isolation checks. It never restores PostgreSQL or treats Valkey AOF as
a database backup.

A provider deployment must remain N/N-1 compatible with the current Valkey key
contract. Valkey presence alone never grants Rust production route ownership;
`apps/api-rust/tests/fixtures/routes/route-gate.tsv` remains authoritative.
