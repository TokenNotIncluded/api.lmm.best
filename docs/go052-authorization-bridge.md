# Go 0.2.52 authorization compatibility bridge

This branch starts from the signed `go-v0.2.52` source at
`6d3879865`, retains its business implementation, and backports the current
native deployment CLI, manual-only release tooling and package hooks. It does
not restore server deployment workflows, PR formatting checks, or a zero-open-
Issue/PR release gate.

The authorization verifier still requires every known baseline grant and the
exact built-in role definitions. It may retain only structurally valid admin
read/write grants for an entirely unknown resource. Such rows cannot authorize
non-superusers in the older runtime, because `Can` checks the compiled permission
registry before looking up policy. Extra grants on known resources, sensitive
actions, root policy rows, denials, wildcards and malformed rows remain errors.
Verification and policy reload do not rewrite user-specific permissions.

The change is also intended for main. Deploying main directly from the existing
0.2.52 still cannot make the original binary understand new authorization rows;
the proposed migration sequence therefore has two distinct steps:

1. Original 0.2.52 -> bridge, preserving the original authorization baseline.
2. Bridge -> current main, whose additional resource defaults are accepted but
   unusable by the bridge during an N-1 schema verification.

## Isolated database evidence

An encrypted production snapshot captured on 2026-09-19 at 17:27 UTC was fully
decrypted and restored to a private PostgreSQL 18 cluster on the local controller.
The entire rehearsal, including PostgreSQL and Valkey, ran in a separate Linux
network namespace containing only loopback and no external route. Production
configuration credentials were not imported. No production database connection,
package installation or server deployment was performed.

The actual retained 0.2.52 executable matched snapshot payload SHA-256
`24d89d25b1e0ad56e9b7601b664e8d3f57c1a32fbdffa09d73b79a64be8233ea`.

- Negative control: original verification, candidate migration and candidate
  verification passed; the original rollback executable rejected the new
  acquisition defaults, reproducing #386.
- Positive control: bridge migration/verification and original rollback
  verification passed; main migration/verification and bridge rollback
  verification then passed. Candidate migration replay also passed.
- All 126 pre-existing tables were compared using sorted, length-delimited
  row hashes over their original columns. Only `casbin_rule` changed. Existing
  business rows and user-specific permission rows, including their IDs, were
  unchanged. The bridge preserved baseline permission semantics; repeated main
  migration preserved the final permission set. Built-in rule IDs are recreated
  by the existing seeder and are not claimed to be byte-idempotent.
- Race-enabled authorization tests cover future-resource denial, preserved user
  allow/deny overrides, reload, missing known defaults and forbidden extra grants.

## Not yet a complete deployment approval

These are database migration and N-1 verifier results, not proof of successful
native package rollback. #391 separately tracks the native managed-billing guard,
which currently rejects changed-provider rollback whenever managed subscription
records exist. The current frontend/package drift must also be reconciled through
the signed native frontend path and tested against both backend versions.

Do not deploy this branch or describe the upgrade plan as ready until those
independent gates and the final combined-source checks have been completed and
the owner has manually approved the concrete rollout plan. Existing encrypted
backups and private rehearsal reports remain on the local controller.
