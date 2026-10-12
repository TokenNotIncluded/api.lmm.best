# Contributing

Start with [AGENTS.md](../AGENTS.md). This is the long-lived microservice WIP,
not the old site's upgrade branch. Read [architecture](core-migration.md) when
changing ownership or interfaces, and [development](development.md) for setup.

## Scope

Rust owns identity, permissions, funds and model traffic. Go modules own their
business records and receive narrow interfaces. Keep the core independently
usable; do not restore removed monolith code to make a build pass.

Preserve public contracts unless the task replaces them. Check consumers before
removing a file, including dynamic entry points. Keep LICENSE, NOTICE and other
attribution. Use isolated databases and synthetic credentials, never production
accounts or database contents for ordinary tests.

## Verification

Use [the working guide](agent-workflows.md) to select checks. Start with affected
behavior and callers; expand for shared contracts, security, billing and data
integrity. Documentation text normally needs:

```sh
python3 -B scripts/check-docs-brand.py --docs-only
```

Review changed examples and links outside the checker's maintained set. Do not
execute installation, initialization or payment examples to validate prose.
Checker changes need its tests; logo changes need the full asset check. Legal
implementation/resource or packaging changes need the relevant legal/distribution
tests, not every document that mentions a policy or package.

Use [the quality tool](quality.md) when layered evidence or independent acceptance
is required. Choose the required layer once; do not stack its prerequisites or
pretend a smaller check meets full acceptance. Preserve the existing failure,
debt and review rules. Record actual commands, tested input and unverified items.

## Delivery

The default development branch is `wip/rust-core-go-extensions`; `main` and
`traditional` are frozen historical snapshots. Existing legacy PRs need porting,
not just a base change; see [branch policy](development-branches.md).
Reuse the WIP workspace and branch for ongoing work. Preserve concurrent changes;
never force-push. CLI changes go to its own repository first, then update the
submodule to the verified, pushed commit.

Describe the outcome, scope, compatibility impact and checks. Review the diff
and confirm the remote result. A build is not a deployment or proof of complete
business behavior. Merge, publication, fund movement and production changes need
authorization for that action; do not request it again when unchanged and already given.

Report vulnerabilities through [SECURITY.md](../.github/SECURITY.md), without
secrets in public reports. Community rules: [CODE_OF_CONDUCT.md](../.github/CODE_OF_CONDUCT.md).
