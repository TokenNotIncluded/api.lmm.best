---
name: review-open-source-bounty
description: "Review or settle an api.lmm.best bounty-linked submission using repository and escrow evidence. Not ordinary PR review or merge. Settlement needs the exact server preview and confirmation."
---

# Review an open-source bounty

Use the connected `open_source_bounty_operator` prompt and
`open_source_bounties.*` tools at `https://api.lmm.best/mcp`. Use Streamable HTTP
and the negotiated version; never expose credentials. The publisher reviews
ordinary submissions. An administrator handles a dispute, not every review.

## Review the submitted work

Read the exact project/challenge, acting role, publisher/contributor, state,
price/fee/net escrow, submitted URLs/note, ratings and disputes. An open dispute
freezes ordinary approval, rejection, merge, refund and settlement. No submission
means no invented result or unsupported review action.

Inspect the actual linked issue/PR, complete diff, source/base revisions,
authorship, current state, relevant checks and unresolved review findings.
Compare against the published contract. Issue-only work can be valid when the
bounty rewards reports or ideas; do not require code for a report bounty.

Run proportionate local tests. Reuse valid evidence for the reviewed revision,
configuration and environment; expand only for uncovered acceptance criteria or
findings. Distinguish contributor work from publisher or third-party fixes.
A production Rust migration still needs the designated Linux deployment,
smoke/integration tests, Go parity for affected behavior, performance evidence
and no unresolved correctness/security finding. Use infrastructure only when
authorized. Missing evidence remains unverified, not a pass.

For review-only requests, report findings and stop before settlement or GitHub
mutation. Do not demand a payment preview to deliver a code review.

## Decide and preview settlement

Mark criteria passed, failed or unverified, with evidence and a concise public
evaluation. Scores must be truthful: 5 exemplary fulfillment, 4 fulfillment with
minor shortcomings, 3 the acceptance threshold, 2 incomplete/materially flawed,
1 unusable/contrary to the contract. Never choose a score to force payment.

Refresh mutable challenge, dispute, ownership and escrow state before acting.
Obtain the intended action's `input_required` preview. Show the action, exact IDs,
publisher and recipient, public score, gross price, fee/net reward, escrow/balance
impact, submitted URLs, evidence and evaluation. State whether funds are paid,
retained or refunded exactly as the server reports.

Approval, payment, rejection, closing/refunding, tipping, rating, disputes,
draft deletion and withdrawal require explicit confirmation of their exact
preview. Reuse confirmation only for the unchanged action. Tips are separate and
non-refundable. Do not infer a score on another person's behalf.

## Verify the outcome

After confirmation, complete the same payload and read back challenge, project,
ledger/escrow, ratings and disputes. Do not blindly retry an uncertain mutation.

GitHub actions need authorization covering them. Merge only the reviewed commit
with required checks satisfied. Before an authorized closure, ensure each
relevant thread has an accurate explanation and evidence; reuse an existing
sufficient comment instead of posting duplicates. A rejected fix does not mean
the underlying bug is resolved: leave its issue open unless there is a valid,
authorized closure reason. Identify the replacement for a duplicate.

Read back changed GitHub state. Report the review, actual checks, MCP/GitHub
states, score, payment/non-payment, fee/escrow effects and merge commit if any.
If these systems disagree, report and reconcile the specific discrepancy rather
than inventing success. Disputes require their own confirmed evidence-based flow.
