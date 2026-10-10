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

A temporary whole-origin proxy can bypass the published frontend: Go resolves
its frontend directory at process startup, so a later `current` switch can leave
the public HTML and scripts on the previous release. Check the public
`/index.html`, a console navigation and their referenced scripts against the
signed release; a successful symlink switch alone does not prove they are served.
`/index.html` must return the expected bytes directly with HTTP 200, rather than
a backend redirect with an empty body.

For a captured proxy whose default location forwards to `@lmm_api_backend`,
`scripts/render-frontend-proxy-overlay.py` renders a reviewable local candidate
from that exact configuration, the verified release's generated
`apps/web/src/routeTree.gen.ts`, and its extracted frontend directory. It keeps
the backend hop and default fallback, serves only known frontend navigation and
public files through the publisher's `current`/`assets` paths, and preserves
non-GET/HEAD requests through the backend. It does not use a general API-to-SPA
fallback, contact a host, verify signatures, or install the output.

Deploy the reviewed bytes through a single authorized, pinned ingress action:
preserve the prior file in the controller's Backups root; check exact file and
service identity; replace atomically; run `nginx -t`; restore the prior bytes if
validation fails; reload once and check the public hashes and unchanged Go
generation. Preserve maintenance and Rust probe fragments. An unknown reload
result requires inspection before any separately authorized recovery. Keep the
new configuration as the recovery input, so returning to a local backend does
not also return to the superseded frontend. Do not modify a historical sealed
maintenance owner or its hash-bound recovery files to perform this repair.


- [Native transaction and acceptance](production-release-transaction.md)
- [Manual systemd deployment](manual-systemd-deployment.md)
- [Workflow responsibilities](ci-workflow-layout.md)

Historical operation logs and recovery state on the servers remain available for
manual inspection. Removing workflow automation does not authorize deleting
backups, transaction locks, or recovery evidence.
