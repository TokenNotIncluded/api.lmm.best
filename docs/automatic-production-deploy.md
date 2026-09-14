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
