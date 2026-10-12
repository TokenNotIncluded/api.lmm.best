# Local development

[Documentation](README.md) · [中文首页](../README.md) · [English overview](../README_EN.md) · [Agent guide](agent-workflows.md)

Use this guide for a development instance. Existing production servers use the
[deployment workflow](deployment-workflow.md). Install only what the task uses;
a documentation edit does not need a running application or every toolchain.

## Requirements

| Task | Tools or services |
| --- | --- |
| Documentation checks | Python 3.10 or newer; Git for changes |
| Web development | Bun pinned in [package.json](../package.json); Node.js 22.12 or newer |
| Default Go backend | Go 1.25.1 or newer; dedicated development PostgreSQL and Valkey when running the app |
| Rust preview | Rust 1.91.0; only needed for that preview |
| Repository recipes | Just; inspect [justfile](../justfile) for the underlying commands |

Do not reinstall dependencies when the existing installation matches the lockfile.
Source/package configuration at the selected revision takes precedence over version
examples in this guide.

## Set up

For a new application checkout:

```bash
git clone https://github.com/TokenNotIncluded/api.lmm.best.git
cd api.lmm.best
just setup
# Only when apps/api-go/.env does not already exist:
test -e apps/api-go/.env || cp .env.example apps/api-go/.env
```

The Go development process starts from `apps/api-go` and reads `.env` there, not
at the repository root. Never overwrite an existing configuration or use production
data. Startup applies migrations by default; use a dedicated development database.

| Variable | Development value |
| --- | --- |
| `SQL_DSN` | Development PostgreSQL connection string; URL-encode reserved credential characters |
| `REDIS_CONN_STRING` | Development Valkey connection string |
| `SESSION_SECRET` | A new random secret, stable across restarts |
| `CRYPTO_SECRET` | A different random secret, stable across restarts |
| `SERVER_ADDRESS` | An explicit public HTTPS origin before enabling OAuth/external callbacks |

Generate each secret separately. Do not commit `.env` or copy production credentials.

## Start the application

From the repository root, start Go:

```bash
just dev-go
```

In a second terminal, start Web on a different port:

```bash
bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

Open <http://localhost:5173>. Go uses port 3000 by default; check the actual listen
address before exposing it. Web proxies to `http://localhost:3000`; use
`VITE_REACT_APP_SERVER_URL` for another backend. `just dev-web` defaults to port
3000, so use the explicit command above when running both on one host.

## Checks

Use [the check-selection table](agent-workflows.md), starting with the affected
files, tests and callers. Do not run unrelated Web tests for a Go-only change or build both
applications for Markdown. Shared API changes can still need Web contract tests. For broad Go/Web validation, run `just check` once;
it already includes their tests and other checks. Build a component when artifact
behavior needs validation, not as another unconditional prerequisite.

Documentation and skill text normally need only:

```bash
python3 -B scripts/check-docs-brand.py --docs-only
```

The checker's own unit tests are for checker changes. Logo changes use the full
check without `--docs-only`. Review changed examples and out-of-scope document
links separately; the checker is not an all-Markdown or command-execution test.
Do not run whole-tree formatting or production examples for documentation work.

Keep actual commands/results and reuse valid evidence. Tests run locally; consult
[Actions](ci-workflow-layout.md) rather than assuming a PR or push starts test CI.

## Optional recipes and limits

| Recipe | Limit |
| --- | --- |
| `just dev`, `just infra-up`, `just infra-down` | Need a local `docker-compose.dev.yml`, not included in this branch |
| `just dev-rust` | Needs that Compose file and its `rust-preview` profile |
| `just build-all`, `just test-all` | Include Rust preview; not proof of production readiness |
| `just docker`, `just docker-rust` | Need local Dockerfiles, not included in this branch |
| Local package | Use `packaging/local/lmm-api-go/build-local-package.sh`; see [AUR](../packaging/aur/README.md) |
| `just clean-generated` | Deletes generated output; not routine setup or cache maintenance |

Use `just --list` when recipe discovery is needed, not before every known command.
See [Rust blue-green](rust-blue-green.md) for rollout limits. The independent
[LMM CLI](../apps/lmm/README.md) is also a preview. Do not mix a refactor branch's
setup with this branch's files.

## Repository map

| Path | Purpose |
| --- | --- |
| [apps/web](../apps/web) | React/TypeScript console and public pages, built with Rsbuild |
| [apps/api-go](../apps/api-go) | Default backend |
| [apps/api-rust](../apps/api-rust) | Preview backend |
| [apps/lmm](../apps/lmm/README.md) | Preview setup CLI |
| [packages](../packages) | Client integrations |
| [scripts](../scripts) | Development, checks and deployment entry points |
| [packaging](../packaging) | Packages and immutable runtime assets |
| [skills](../.agents/skills/README.md) | Task-specific instructions and optional references/helpers |

Go and Web releases are independent. Publication does not deploy. Consult the
[release architecture](release-architecture.md) and actual installation path before
changing a server.
