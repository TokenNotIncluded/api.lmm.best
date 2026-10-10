# Contributing

This branch is a fresh-install microservice WIP. Read [the architecture](docs/core-migration.md) and [development guide](docs/development.md) before changing ownership or startup behavior.

## Scope

Keep Rust responsible for identity, permissions, funds and model traffic. Go modules own only their own business records and receive narrow interfaces. Do not restore removed monolith files to make a build pass. Keep core and extension updates separate.

Use isolated databases and synthetic credentials. Never register production accounts for tests. Preserve public contracts unless the change explicitly replaces them; remove a file only after checking its consumers. Keep LICENSE, NOTICE and third-party attribution.

## Verify the changed behavior

Run the relevant unit and integration tests, then report the exact tested commit and results. [The development guide](docs/development.md) lists commands; [the distribution guide](docs/release-architecture.md) explains preview artifacts. Real database tests and simulated funds adapters are different evidence.

For documentation changes, run `python3 scripts/check-docs-brand.py`. For policy or packaging changes, also run `python3 scripts/test-distribution.py` and the Go legal-package tests. Inspect user-visible changes in a real browser when applicable.

## Submit

Describe the problem, bounded change, compatibility impact and executed checks. State failed or unrun checks plainly. A passing build is not a production-readiness claim. Opening a PR, merging, publishing and deploying are separate actions; do not automatically promote this WIP branch.

Use [SECURITY.md](SECURITY.md) for vulnerability reports. Do not put tokens, private configuration or user records in commits, logs or screenshots. Community rules remain in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
