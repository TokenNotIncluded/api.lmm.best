# Automatic production deployment

Publishing a successful `go-vX.Y.Z` or `web-vX.Y.Z` release starts
`.github/workflows/deploy-production.yml` through the `workflow_run` event.
The job downloads the immutable release assets, verifies their SHA-256 and
Sigstore identity, builds the matching Arch package, reads the installed
production Go/Web packages as rollback candidates, and invokes the native
`lmm-api deploy production plan`, `stage`, and `promote` commands on `ArchDmit`.

The workflow requires a GitHub Environment named `production` with these
secrets:

- `PRODUCTION_SSH_PRIVATE_KEY`: a dedicated deploy key whose public key is
  authorized for `root@ArchDmit` on TCP port 222.
- `PRODUCTION_SSH_KNOWN_HOSTS`: the pinned `known_hosts` line for that exact
  host and port. Do not use `ssh-keyscan` in the workflow.

Protect the `production` Environment with the repository's required reviewers.
The job intentionally stops after `promote` when the native controller reaches
`AWAITING_CONFIRMATION`; a reviewer must run the exact `confirm` command from
the emitted deployment plan, or run `rollback` if health evidence is not
acceptable. GitHub Actions never receives application credentials, database
credentials, OAuth tokens, or API keys.

The workflow is serialized with a single concurrency group. A failed release
workflow, draft/prerelease, unsupported tag, signature mismatch, missing
rollback package, SSH host-key mismatch, or production health failure stops
the deployment without automatic rollback.


### Reruns and external acceptance

Each workflow attempt has a unique native deployment ID:
`release-<component-tag>-<run-id>-attempt-<attempt>`. A rerun must not recycle or
remove an older workspace: its activation receipt and rollback data may still
be needed. A new workspace does not override any pending native transaction.

After native promotion, the workflow performs a credential-free public probe
of `/api/status`, `/`, `/login` and `/console`, and downloads every referenced
same-origin JavaScript/CSS entry once. Backend releases must report the exact
released version. An HTTP 200 page served instead of a missing JS/CSS file is
an error, not a healthy asset. Requests retain TLS checks, reject cross-origin
redirects, and have bounded response sizes and an overall deadline.

This probe does not exercise paid model requests or privileged account actions,
and does not replace the native authenticated health checks. In particular,
`AWAITING_CONFIRMATION` still requires the existing confirmation procedure; the
external probe never auto-confirms a transaction or suppresses rollback.
