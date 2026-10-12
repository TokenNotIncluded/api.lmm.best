---
name: publish-open-source-bounty
description: "Draft, price or publish an api.lmm.best open-source bounty. Use for bounty listings and escrow, not ordinary GitHub issues. Publication needs an exact server preview and confirmation."
---

# Publish an open-source bounty

Use the available `open_source_bounty_operator` prompt and
`open_source_bounties.*` tools at `https://api.lmm.best/mcp`, with Streamable HTTP
and the server-negotiated version. Reuse the connected account; credentials stay
in its secret source, never in docs, logs or shell history. This skill neither
connects a tool nor proves the WIP has deployed that service.

## Draft

Read identity/ownership and the matching project or draft. Inspect the real
repository/issue. Do not invent defects, duplicate drafts or create external
artifacts without authorization. Define scope, objective acceptance evidence,
allowed issue/PR submissions, platform, exclusions, payable fix count and gross
price per fix. Use server-supported deadlines; keep each fix independently reviewable.

For a production Rust migration contract, retain designated Linux test-server,
Go parity, integration and performance requirements. Windows-only work is not
a substitute. Do not add those requirements to an unrelated documentation bounty.

Save through the draft tool and read back fields; correct discrepancies. A
draft-only request ends here, without a publication/payment preview, unrelated
ledger history or a claim that escrow is locked. Refresh draft state before a
further mutation if it may have changed.

## Publish

Read current project state, balance, fee configuration and relevant ledger/escrow.
Load the operator prompt and verify the acting role, including enabled super-
administrator status. Do not trust old amounts or ownership from a conversation.

Obtain the publication action's `input_required` preview. Show its exact action,
publisher/destination, any public score, gross price per fix, count, total debit,
fee, net reward/escrow, acceptance evidence, current/resulting balances and net
balance decrease. Use returned amounts rather than competing rounding. For a
super-administrator publisher, show gross debit and fee credit separately.
Do not claim check-in rewards without ledger evidence.

Complete the server flow only after the user confirms that exact preview. Reuse
confirmation for the unchanged action. Changed amount, recipient, criteria or
payload needs a new preview and confirmation. General bounty management is not
confirmation of a specific monetary preview.

Read back project, public listing, balance and ledger/escrow. Reconcile amounts
and report IDs, debit, fee credit, net escrow and final balance. An uncertain
result requires a state read, not another payment attempt.

Withdrawal, draft deletion, closing/refunding, tipping, rating and dispute actions
retain their own preview and explicit confirmation. Tips are separate and non-
refundable. The publisher reviews ordinary submissions; an administrator handles
disputes, not an extra routine approval stage.
