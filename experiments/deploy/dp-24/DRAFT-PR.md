# Draft: dp-24 isolated RPC identity experiments and Go peer-policy helper

Base: `wip/rust-core-go-extensions`
Head: `research/dp-24-cluster-rpc`
Exact parent: `72667564c0431754d4856dc2e0db55f360bd2745`
Relates to #675. Do not close #675. Do not merge into main.

**Offline PR text only. No remote branch or PR was created. Do not trigger remote CI.**

This change adds a Go standard-library peer-policy helper and reproducible isolated network experiments.
The existing Rust listener and Go client remain UDS-only. Protobuf, schemas, workflows and deployments are unchanged.

Evidence: 32 local fixture scenarios pass, 1 is blocked because netem is unavailable; the runner exits 2.
Five Go unit tests pass with the race detector. The opt-in Go HTTP/2/gRPC wire probe passes separately in the isolated network.
The Python/SQLite fixtures are not the Rust core, PostgreSQL ledger, real multi-host setup or a 1c1g capacity result.

This is not ready for production enablement. Rust/Go runtime wiring, durable policy versions, root rotation,
real identity/payment verification, automatic discovery and bounded old-connection drain remain open.
See REPORT.md and HANDOFF.md for the exact scope and remaining acceptance gates.

No push was made because a no-CI remote-write path was not established for all applicable repository workflows.
The local patch preserves the exact requested parent and does not overwrite other agents' work.
