# Server operations

## Backend: manual

GitHub Actions does not perform server diagnosis, repair, or backend
deployment. The server operations workflow, incident-request trigger, and
general SSH transport remain removed.

Use operator-controlled SSH and the native deployment CLI for server work.
Package-owned installations keep their signed package transaction; standalone
systemd installations use the separate manual deployment path.

## Frontend: manual, restricted

`release-web.yml` signs and publishes a Web artifact without deploying it. An
operator starts `deploy-web-frontend.yml` with an existing signed release tag
only after checking that both origins' active Go backends support the Web
change. Changes that require a new Go backend use the native combined release
transaction. This manual gate remains until paired and independent Web releases
have an explicit compatibility contract.

The frontend-only workflow remains narrowly restricted:

- It runs only through `workflow_dispatch`; a signed Web release alone cannot
  change either production origin.
- The production key is a dedicated deploy key restricted by the remote
  `authorized_keys` forced command `/usr/local/sbin/lmm-web-deploy`. The key
  cannot open a shell, run arbitrary commands, or reach the backend.
- That script accepts only `<semver>:<pkgrel>`, refuses any release id that
  already exists, and publishes through the native `frontend publish` entry
  point, which switches an atomic symlink and retains the previous release.

Revoking `LMM_WEB_DEPLOY_SSH_KEY` and removing its `authorized_keys` line on
both hosts disables the frontend deployment workflow without touching anything
else.

The web archive also carries the static `/terms` and `/privacy` legal pages
and their shared stylesheet. Nginx reads them through
`/srv/lmm-api-frontend/current/legal/` so both origins switch the same copy
with the frontend release. Preserve these aliases on manually configured hosts;
these pages remain available when the backend is down.


- [Native transaction and acceptance](production-release-transaction.md)
- [Manual systemd deployment](manual-systemd-deployment.md)
- [Workflow responsibilities](ci-workflow-layout.md)

Historical operation logs and recovery state on the servers remain available for
manual inspection. Removing workflow automation does not authorize deleting
backups, transaction locks, or recovery evidence.
