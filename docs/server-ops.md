# Server operations are manual

GitHub Actions no longer performs server diagnosis, repair, or deployment.
The server operations workflow, incident-request trigger, and SSH transport have
been removed. Production SSH secrets are not kept in GitHub Actions.

Use operator-controlled SSH and the native deployment CLI for server work.
Package-owned installations keep their signed package transaction; standalone
systemd installations use the separate manual deployment path.

- [Native transaction and acceptance](production-release-transaction.md)
- [Manual systemd deployment](manual-systemd-deployment.md)
- [Workflow responsibilities](ci-workflow-layout.md)

Historical operation logs and recovery state on the servers remain available for
manual inspection. Removing workflow automation does not authorize deleting
backups, transaction locks, or recovery evidence.
