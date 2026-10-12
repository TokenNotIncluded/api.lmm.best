# A09 isolated resource and recovery checks

These are test tools, not installed LMM programs. They do not enable Core DDL,
update a service, or authorize production work. Core's ordinary startup remains
read-only. Do not use them with a production dump or an existing database.

## Local checks

Python 3.11+ and `cryptography` must be available in the selected Python
environment. No tool or dependency is downloaded by these scripts.

```sh
MISE_DISABLE_TOOLS=rust mise exec -- python3 -B -m unittest discover \
  -s tests/architecture_completion -p 'test_*.py' -v
```

[Backup tests](test_backup.py) exercise real AES-256-GCM and private file handling.
Complete synthetic dumps stay below `/home/lightjunction/Backups`, with private
0700 directories and 0600 files. Recovery keys remain outside Backups. The tests
create and remove only their own random test directories. A GCM tag, source/target
binding, byte count and plaintext digest must all match before decrypted output
is published. Existing files are never replaced. This private test format is not
a production backup format or an external compatibility promise.

[Resource tests](test_pg_fixture.py) inspect commands and rejection rules with
mocked Docker results. They are not PostgreSQL, container or restore evidence.
[Build lease tests](test_build_lease.py) use real Linux child processes; they do
not qualify a PostgreSQL maintenance fence or database takeover.

## Explicit PostgreSQL restore probe

The [runner](run_postgres_recovery.py) needs the local Docker socket and the
already-cached, pinned image in [pg_fixture.py](pg_fixture.py). Supply a new,
absolute, private parent directory. No external DSN or existing container is
accepted. The runner never pulls an image or changes global shell settings.

```sh
MISE_DISABLE_TOOLS=rust mise exec -- python3 -B \
  tests/architecture_completion/run_postgres_recovery.py \
  --private-root "$A09_PRIVATE_ROOT"
```

It creates one random, resource-limited PostgreSQL container and ten independent
synthetic databases. Each task has a NOLOGIN owner, a separate installation login,
a restricted runtime login, and private pgpass files. Connections use a random
loopback port, never the protected original service or migration ports.

The probe installs a small synthetic schema, fences its test login writers,
captures a custom-format dump, encrypts it, reloads the separate key and authenticates
the whole dump. It restores into a different empty database, then compares rows,
sequence state, contract values, columns, owners, ACLs, functions, indexes and
triggers. Runtime denials must specifically return PostgreSQL SQLSTATE 42501;
a failed connection or a syntax error is not accepted as a security test pass.

Restore input is checked again against the authenticated digest. A persisted,
exclusive restore intent prevents automatic replay after a lost response. A
successful `pg_restore` result is not enough: the snapshot and runtime checks
must also pass. Missing Docker is reported as `BLOCKED` with exit code 2.

Only an exact owned container generation can be removed. Cleanup checks the
cluster system ID, boot, data directory and port; it closes new database
connections and rechecks for existing clients before removal. Unknown identity,
a changed container, a live client or a driver error retains the resource.
Backups, keys and private receipts are retained after the real probe. Failed
initial provisioning is retained for explicit inspection; there is no prefix
cleanup, TTL takeover or adoption of a previous run.

## Integration still required

The probe does not install the full Core schema or reconstruct global roles in
a second cluster. It does not implement Core's writer fences, schema-only worker,
PostgreSQL dead-holder CAS, pgroll compatibility, or old/new product startup.
Those require the frozen A01–A10 interfaces and S11's shared integration. Never
replace these checks with a successful small-schema probe.

Final Docker/native installation and 1C1G workload evidence must bind the actual
final product source and binaries. These helpers are not shipped by
`packaging/distribution.json`; only the existing three product programs remain.
