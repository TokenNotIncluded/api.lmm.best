---
name: review-open-source-bounty
description: "Review or settle an api.lmm.best bounty-linked submission using repository and escrow evidence. Not ordinary PR review or merge. Settlement needs the exact server preview and confirmation."
---

# Review an open-source bounty

Use the connected `open_source_bounty_operator` prompt and
`open_source_bounties.*` tools at `https://api.lmm.best/mcp`, with Streamable HTTP
and the negotiated version. Never expose credentials. The publisher reviews
ordinary submissions; an administrator handles disputes. This skill does not
establish which service modules the WIP has implemented or deployed.

## Review

Read the exact project/challenge, role, publisher/contributor, state, price/fee,
net escrow, submitted URLs/note, ratings and disputes. An open dispute freezes
ordinary approval, rejection, merge, refund and settlement. No submission means
no invented result or unsupported review action.

Inspect the linked issue/PR, full diff, source/base, authorship, current state,
relevant checks and unresolved findings. Compare against the published contract.
Issue-only work is valid when reports or ideas are rewarded; do not demand code
for a report bounty. Distinguish contributor work from publisher/third-party fixes.

Run proportionate local checks and reuse valid evidence for the reviewed source,
configuration and environment. Expand for uncovered criteria or findings. A
production Rust migration still needs designated Linux deployment, smoke and
integration tests, affected Go parity, performance evidence and no unresolved
correctness/security finding. Use infrastructure only with authorization.
Missing evidence stays unverified. A docs-only bounty is not a production migration.

For review-only work, report findings and stop before settlement or GitHub mutation.
Do not require a payment preview just to deliver a code review.

## Decide and settle

Mark criteria passed, failed or unverified, with evidence and a concise public
evaluation. Score truthfully: 5 exemplary fulfillment, 4 fulfillment with minor
shortcomings, 3 acceptance threshold, 2 incomplete/materially flawed, 1 unusable
or contrary to contract. Never choose a score to force payment.

Refresh mutable challenge, dispute, ownership and escrow state. Obtain the intended
action's `input_required` preview. Show exact IDs, action, publisher/recipient,
public score, gross price, fee/net reward, escrow/balance impact, submitted URLs,
evidence and evaluation. Describe paid, retained or refunded funds as returned.

Approval, payment, rejection, closing/refunding, tipping, rating, disputes, draft
deletion and withdrawal require confirmation of the exact preview. Reuse only
unchanged confirmation. Tips are separate and non-refundable; never infer another
person's rating. After confirmation, execute the same payload and read back the
challenge, project, ledger/escrow, ratings and disputes. Do not blindly retry.

## GitHub and completion

GitHub mutations need authorization covering them. Merge only the reviewed commit
with required checks satisfied. Before an authorized closure, ensure relevant
threads have an accurate explanation and evidence; reuse an adequate existing
comment instead of posting duplicates.

Rejecting a fix does not resolve the bug. Keep its issue open unless a valid,
authorized closure reason exists; identify the replacement for a duplicate.
Read back changed GitHub state. Report review, actual checks, MCP/GitHub states,
score, payment/non-payment, fee/escrow effects and merge commit if any. Reconcile
specific discrepancies rather than inventing success. Disputes retain their own
confirmed, evidence-based flow.
