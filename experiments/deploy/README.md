# Deployment research only — not a runtime service

These directories contain bounded experiments and evidence collected while building [WIP #675](https://github.com/TokenNotIncluded/api.lmm.best/pull/675). They are **not** deployed, compiled into the Rust core, installed in its binary distribution, or used to decide whether the production service is ready. Experimental scripts must not be executed against production hosts or funds.

| Study | What it evaluates |
| --- | --- |
| [DP-22](dp-22/README.md) | Request admission and draining during a Rust rolling update |
| [DP-23](dp-23/README.md) | Independent Go extension rollout and local handoff |
| [DP-24](dp-24/README.md) | Identity and transport policy for cross-server RPC |
| [DP-25](dp-25/README.md) | PostgreSQL connection budgets and recovery |
| [DP-26](dp-26/README.md) | Component CPU/memory measurements, duplicate JSON decoding |
| [DP-27](dp-27/README.md) | Smaller build artifacts, integrity checks, image transfer |
| [DP-28](dp-28/README.md) | Disk lifecycle, resource preflight and safe cleanup |
| [DP-29](dp-29/README.md) | On-demand release controller and rollback prototype |
| [DP-30](dp-30/README.md) | Repeatable load evidence for multiple updates |

Some verified component changes were copied into `apps/lmm-core` and `apps/lmm-extensions`; the evidence here remains separately scoped. A passing Python or simulated-provider experiment does **not** mean Rust compilation, real money settlement or 1-core/1-GiB acceptance passed.

The root `.dockerignore` excludes `experiments/` from Docker build contexts. Official distributions contain only explicitly listed binaries and resources. Archiving or deleting an experiment after its acceptance requirements are met is a repository housekeeping decision; deletion does not reduce the running core's RAM. Preserve any evidence required for unfinished production readiness checks.
