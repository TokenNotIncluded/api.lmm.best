# [Draft] DP-30 bounded update evidence and local probe regression

Base branch: `wip/rust-core-go-extensions`
Head branch: `research/dp-30-update-chaos`
Audited base: `72667564c0431754d4856dc2e0db55f360bd2745`
Related: #675

## Scope

Only `experiments/deploy/dp-30/`. Adds bounded HTTP/SSE probes, a provisional 20-round plan, evidence contracts, an offline reconciliation tool, fault contracts and retained local test results. No changes to other DP implementations.

## Executed

40 local tool tests passed. The final Python fixture suite ran 18 cases with three repetitions per case type, 156 model probes and 156 fixture upstream receipts. All expected outcomes were observed. Earlier development results are also retained.

## Not accepted yet

This is not application capacity evidence. The fixed base still disables Rust model/billing routes; the Go host only enables identity. No actual 1c1g application run, real ledger recovery, 20 application updates, node failure or 3/5-node test was executed. Final integration SHA and DP-21 common workload/budget remain missing.

## Review focus

Evidence must not promote fixture success to application acceptance. Missing metrics stay unknown. No request retries or redirect following. Slow headers have a total deadline. Partial SSE terminals are rejected. Ledger checks use integer amounts and per-account revisions.

## Safety and delivery

No remote writes, CI, deploy, real funds, notifications or merge. This text is a local PR draft only; no GitHub PR exists for this delivery. Use the patch in a full checkout. A normal remote push or PR may trigger workflows, so it was not attempted under this task's restrictions.
