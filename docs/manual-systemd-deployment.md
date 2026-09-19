# Manual Systemd Deployment

Standalone Linux builds can be deployed on Ubuntu, Debian, Arch, and other
systemd distributions without a package manager. This path updates an existing
`lmm-api.service`, `/usr/bin/lmm-api -> lmm-api-go`, and
`/srv/lmm-api-frontend` installation. Package-owned binaries are rejected; those
installations retain the package transaction path.

Build the Go binary and frontend from the same reviewed revision. Transfer both
to a private target directory. On the target, run the repository script as root:

```sh
scripts/lmm-api-deploy.sh systemd stage --release REVISION \
  --binary /absolute/path/lmm-api-go --frontend /absolute/path/frontend
scripts/lmm-api-deploy.sh systemd apply --release REVISION --confirm api.lmm.best
scripts/lmm-api-deploy.sh systemd status --release REVISION
# Check the public site and observe the service for at least two minutes.
scripts/lmm-api-deploy.sh systemd confirm --release REVISION --confirm api.lmm.best
```

The standalone equivalent is `python3 scripts/deploy-systemd.py`. The target
requires Python 3.11+, systemd, nginx, and the existing environment file at
`/etc/lmm-api-go/lmm-api-go.env`. The health endpoint is the local service on
port 3000. Ubuntu is the first live-validated target; other distributions must
meet these prerequisites and have not yet been exercised live.

The normal path requires an already-compatible PostgreSQL schema. If schema
updates are required, add `--migrate` to both `stage` and `apply`. This mode
requires `pg_dump` and `pg_restore`, and currently supports the active PostgreSQL
schema without a separate log database. It validates backup connectivity
before stopping, waits for clean writer shutdown and refund completion, saves
a custom-format database dump, checks its archive inventory, applies the native
migrations, then verifies the resulting schema before starting the candidate.
Archive validation is not a full restore rehearsal. Unrelated historical tables
can be excluded at stage with `--backup-exclude-table schema.table`; exact names
are required and exclusions are retained in the immutable staged plan.

Private transaction state, old binary, environment copy, database backup (when
selected), and logs remain in `/var/lib/lmm-api-deploy-systemd/REVISION`.
Failures leave recovery evidence and never silently retry activation or roll
back. Check `status`, the private logs, and the service journal before recovery:

```sh
scripts/lmm-api-deploy.sh systemd rollback --release REVISION --confirm api.lmm.best
```

Rollback restores the previous binary and frontend, not the database. Schema
migrations must remain compatible with the old binary; destructive migrations
require a separate database recovery procedure. The script does not create a
new server installation, configure a database, or support non-systemd hosts.
