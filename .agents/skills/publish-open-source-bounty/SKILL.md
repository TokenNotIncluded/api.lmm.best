---
name: publish-open-source-bounty
description: "Draft, price or publish an api.lmm.best open-source bounty. Use for bounty listings and escrow, not ordinary GitHub issues. Publication needs an exact server preview and confirmation."
---

# Publish an open-source bounty

Use the available `open_source_bounty_operator` prompt and
`open_source_bounties.*` tools. The service is `https://api.lmm.best/mcp` with
Streamable HTTP and a server-negotiated version. Reuse the connected account;
credentials stay in its secret source, never in docs, logs or shell history.
A skill does not connect a tool or grant account permissions.

## Draft path

Read identity/ownership and the matching project or existing draft once. Inspect
the real repository/issue for the requested work. Do not create duplicate drafts,
invent defects or open external artifacts without authorization.

Define scope, acceptance evidence, allowed issue/PR submission forms, target
platform, exclusions, payable fix count and gross price per fix. Use actual server
capabilities for deadlines. Keep each paid fix independently reviewable. For a
production Rust migration, retain the designated Linux test-server, Go parity,
integration and performance requirements; Windows-only work is not a substitute.

Save through the draft tool and read back its fields. Correct discrepancies.
For a draft-only request, stop here: do not query unrelated ledger history,
request a payment confirmation, publish or claim escrow is locked.

## Publication path

Read current project state, balance, fee configuration and relevant ledger/escrow
before mutation. Load the operator prompt and confirm the acting role, including
whether the publisher is the enabled super administrator. Do not rely on an old
conversation for amounts or ownership.

Obtain the publication action's `input_required` preview. Show its exact action,
publisher and destination, any public score, gross price per fix, count, gross
debit, fee, net reward/escrow, acceptance evidence, current/resulting balances and
net balance decrease. Use returned amounts, not a competing rounding calculation.
For a super-administrator publisher, report gross debit and fee credit separately.
Do not claim check-in rewards exist without ledger evidence.

Complete the confirmation flow only after the user explicitly confirms that
exact preview. Reuse that confirmation for the unchanged action; changed amounts,
recipient, criteria or payload require a new preview and confirmation.
A broad instruction to manage bounties is not confirmation of a monetary preview.

## Verify and finish

Read back the project, public listing, balance and relevant ledger/escrow. Confirm
publication and reconcile the amounts. Report IDs, gross debit, fee credit, net
escrow and final balance. An uncertain result requires a state read, not another
payment attempt.

Publication, withdrawal, draft deletion, closing/refunding, tipping, rating and
dispute actions each retain their server preview and explicit confirmation.
Tips are separate and non-refundable. The publisher reviews ordinary submissions;
an administrator intervenes in disputes, not as a routine extra approval step.
