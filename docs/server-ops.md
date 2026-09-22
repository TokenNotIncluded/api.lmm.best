# Server operations

## Backend: manual

GitHub Actions does not perform server diagnosis, repair, or backend
deployment. The server operations workflow, incident-request trigger, and
general SSH transport remain removed.

Use operator-controlled SSH and the native deployment CLI for server work.
Package-owned installations keep their signed package transaction; standalone
systemd installations use the separate manual deployment path.

## Frontend: automatic, restricted

`deploy-web-frontend.yml` publishes a completed web release to both production
origins with no operator involvement. This is the one exception, and it is
deliberately narrow:

- It runs only after `LMM web release` concludes successfully; a Go release or
  any backend change never triggers it.
- The production key is a dedicated deploy key restricted by the remote
  `authorized_keys` forced command `/usr/local/sbin/lmm-web-deploy`. The key
  cannot open a shell, run arbitrary commands, or reach the backend.
- That script accepts only `<semver>:<pkgrel>`, refuses any release id that
  already exists, and publishes through the native `frontend publish` entry
  point, which switches an atomic symlink and retains the previous release.

Revoking `LMM_WEB_DEPLOY_SSH_KEY` and removing its `authorized_keys` line on
both hosts disables automatic frontend deployment without touching anything
else.

- [Native transaction and acceptance](production-release-transaction.md)
- [Manual systemd deployment](manual-systemd-deployment.md)
- [Workflow responsibilities](ci-workflow-layout.md)

Historical operation logs and recovery state on the servers remain available for
manual inspection. Removing workflow automation does not authorize deleting
backups, transaction locks, or recovery evidence.
