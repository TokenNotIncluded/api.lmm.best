# Legacy SQLite backup bundle

This directory is retained for forensic review and offline regression testing of
the historical SQLite-to-ArchCzy backup mechanism. It is **not** the current
production backup or installation path. Do not install or enable
`lmm-api-sqlite-backup.service` or `lmm-api-sqlite-backup.timer` from this
directory on production.

Current production runs PostgreSQL with dedicated Valkey. When an operator
explicitly opts into business backups, use the package-owned production
controller:

```text
/usr/bin/lmm-api deploy production plan ... --with-backups --age-recipient-file FILE
/usr/bin/lmm-api deploy production stage|promote|status|confirm|rollback ...
```

The controller requires the production three-copy protocol:

- a root-only target copy under
  `/var/lib/lmm-api-go-deploy/backups/<deployment-id>`;
- an encrypted durable controller copy under
  `$HOME/backup/lmm-api/<verified-host>/<deployment-id>`;
- an encrypted off-host copy under
  `/home/arch/.local/state/lmm-api-production-backups/<deployment-id>` on the
  ArchCzy host through the case-sensitive SSH alias `archczy`.

Backup work remains optional and requires explicit current-turn authorization.
Follow `.agents/skills/lmm-deploy-safely/references/path-map.md` and
`.agents/skills/lmm-deploy-safely/references/safety-contract.md`; do not infer
current database or host state from this legacy directory.

The historical script, unit, timer, and their test remain tracked because they
can still provide forensic evidence for old SQLite archives and rollback
investigations. Their presence does not authorize installation or execution
against a live host.

## Offline historical regression test

```sh
bash deploy/backup/test-backup-sqlite-to-archczy.sh
```

The test uses temporary SQLite WAL databases and fake local `ssh`/`scp`; it
makes no network connection and never touches `/var/backups`.
