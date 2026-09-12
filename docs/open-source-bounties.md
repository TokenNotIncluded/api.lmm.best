# Open-Source Bounty Playbook

## 1. What This Is

The Open-Source Bounty program is a real-defect bounty mechanism for public GitHub projects.

A project sponsor can escrow repair rewards using API balance, pay a low, platform-visible bounty fee, and invite contributors to accept challenges. Contributors can submit reproducible Issues, corresponding fix PRs, or both.

After the project owner approves submissions, the locked reward is transferred directly into the contributor’s API balance.

This playbook is independent of any Challenge II restoration process. The platform does not preconfigure, default-publish, or auto-pin any repository, including `TokenNotIncluded/api.lmm.best`. All projects must be created manually by a sponsor using their own balance, and owners, admins, and regular users follow the same fee rules.

## 2. Core Principles

1. Only genuine, reproducible code defects are eligible.
2. Submitted Issue/PR must belong to the GitHub repository specified by the bounty.
3. PR submissions must focus on bounty goals and include relevant tests or validation; if an Issue is submitted, it must be referenced by the PR.
4. Publishing a bounty immediately deducts the admin-configured and publicly visible bounty fee and locks the full reward pool.
5. Daily check-in rewards are credited to general balance and can offset part or all of bounty fees.
6. Only unused escrowed reward is refunded when a project is closed. Collected bounty fees are non-refundable.
7. After approval, rewards are transferred directly from escrow to contributor balance, and each challenge is paid only once.
8. Low-quality reports, fabricated defects, duplicates, unrelated PRs, mechanical cleanup-only changes, and actions taken solely to collect rewards are disqualified.

## 3. How Sponsors Run a Bounty

### 3.1 Create a Draft

In **Profile -> Submit Ticket -> Open-Source Bounty**, select **Create Bounty** and provide:

- GitHub repository URL
- Bounty title
- Scope and target defect description
- Acceptance and verification rules
- Reward amount per fix
- Number of reward slots

Draft creation and edits do not charge and are not visible in the bounty market.

### 3.2 Publish and Escrow Rewards

Total cost at publish time:

`reward per fix × number of slots + bounty fee`

Where:

- The reward pool enters system escrow and can only be used for approved transfers or refund of unused escrow when a project is closed.
- The bounty fee is set by admin and publicly shown; it is calculated as a percentage of the total reward pool. Current default is `1%`. This fee snapshot is saved with the project. The fee is charged from the sponsor, not deducted from contributor rewards, and is not refunded when the project is closed.
- Daily check-in rewards add directly to account balance and can be used to pay all or part of the bounty fee.
- Publishing fails if sponsor balance is insufficient.
- Platform owners or admins publishing their own project must also pay from their own balance; no special exemption applies.

### 3.3 Manage an Active Bounty

After publishing, sponsors can:

- Pause: stop accepting new challengers temporarily while keeping existing accepted challenges.
- Resume: reopen remaining slots.
- View full lifecycle: participants, Issue/PR status, completion notes, review result, and balance ledger.
- Cancel unsubmitted challenge: if a contributor accepted but did not submit, sponsor can cancel and release the slot.
- Tip: pay goodwill for partial but valuable work by sending immediate tip from own balance.
- Close: end bounty and refund any unused escrowed reward.

A bounty cannot be closed while challenges remain in `accepted` or `submitted` states.

Sponsors may cancel only challenges without submissions. Submitted challenges must be approved or rejected first; cancellation is not a way to bypass review.

Contributors may also withdraw voluntarily.

After rejection there is a 7-day appeal window. During appeal and while no dispute is created yet, the affected reward slot and escrowed funds remain frozen; sponsor cannot immediately close and refund those resources.

Once a contributor files a dispute, freezing remains until administrator judgment. Frozen funds are released only after rejection is denied or the 7-day period expires with no appeal.

### 3.4 Review and Transfer

After contributors submit results, sponsors must verify:

- Issue submissions describe a real and reproducible defect.
- Issue includes affected project/module, reproducible steps, expected behavior, actual behavior, and impact.
- For Issue+PR submissions, PR must link to the Issue.
- PR only changes files needed to solve the bounty objective.
- The fix is valid and includes reasonable tests or validation.
- Issue/PR belongs to the bounty repository.

Review outcomes:

- Approved and transferred: system deducts one reward and adds it directly to contributor balance. Operation is idempotent and cannot be duplicated.
- Rejected: no reward paid and a 7-day appeal period starts. Rewards and slots stay escrowed during appeal.

Escrow is only released and slot freed after appeal is denied by third-party admin or after 7 days with no appeal.

When all reward slots are approved, the project is marked complete automatically.

Sponsors must give each contributor a `1-5` rating and public feedback. Score, comment, and contributor average rating are visible to both parties and dispute admins.

## 4. How Contributors Participate

### 4.1 Accept a Challenge

In bounty market, choose a project with available slots and click **Accept Challenge**, then provide your GitHub username.

The system reserves one reward slot for that contributor. The same user cannot accept the same bounty multiple times.

Sponsors cannot accept their own bounty.

### 4.2 Find and Record Real Defects

For Issue submissions, create a valid GitHub Issue in the bounty repository and include at least:

- affected project/module
- reproducible steps
- expected behavior
- actual behavior
- defect impact

Do not create synthetic issues, duplicate issues, non-reproducible cases, or submit support-only questions as bounty issues.

### 4.3 Submit Focused Fixes

For PR submissions, ensure the PR:

- links to the corresponding Issue when both are provided
- touches only files required to fix the defect
- adds meaningful automated tests, regression coverage, or verifiable validation steps
- belongs to the bounty repository

A PR cannot be used to claim reward more than once.

### 4.4 Submit in System

Go to **Open-Source Bounty -> Accepted Challenges** and submit:

- GitHub Issue URL or GitHub PR URL (at least one; both optional)
- optional completion note

The system checks repository ownership and rejects repeated use of the same PR.

### 4.5 Direct Review and Settlement

After contributor submission, the project owner reviews Issue, PR, and notes directly.

After approval, reward is credited to contributor API balance for use with platform-supported models.

If rejected, contributor can dispute within 7 days. Reward and slot remain escrowed during this window.

If appeal expires without action or is denied, challenge ends and slot becomes available again.

Contributors may exit before review completion. After exit, the reserved slot is released. Time already invested by contributor is not compensated.

After review completion, contributors can rate publisher/validator with `1-5` and public comment. Sponsor reputation appears in bounty market; contributor reputation appears on acceptance screen. Single review, comment text, average score, and count are visible to both parties.

This reputation system creates social accountability against bad-faith rejections, unfair low scores, and low-quality delivery.

## 5. Partial Work and Tipping

Tips are direct off-chain transfers outside bounty reward:

- Sponsor can tip while challenge is active, including partial completion with meaningful progress.
- Tip is deducted from payer balance and credited directly to contributor.
- Tips do not reduce escrow reward and do not represent formal acceptance.
- Tips are non-refundable once sent; the UI and MCP must confirm amount, recipient, and consequence before submission.
- Admins can see cumulative tip records to understand prior fund movement between parties.

## 6. Dispute Tickets and Third-Party Arbitration

When payment or acceptance is disputed, either party can open a ticket from challenge card by selecting **Open-Source Bounty Dispute**, and fill challenge ID, reason, title, and fact summary.

Regular refund, technical, or billing tickets are still handled through email; bounty disputes are persisted in-system and reviewed by a third-party administrator.

A rejected contributor must submit a dispute within 7 days of rejection. The system preserves escrowed reward and slot during this period, so sponsors cannot close/reconcile to remove appeal opportunity.

Only one final dispute case per challenge can be opened by one party. Once resolved, the case cannot be reopened to re-freeze funds; the other party may file its own case only when no pending case exists.

Typical scenarios:

- Fix merged and public requirements met, but sponsor refuses payment.
- Submission matches agreement yet is rejected.
- Sponsor changes acceptance rules after publication.
- Harassment, abuse, or other inappropriate behavior.
- Other bounty disagreements requiring neutral review.

The system links disputes to canonical backend records instead of trusting URL query parameters. Linked records include project, repository, Issue, PR, original commit, acceptance notes, reward, tip, escrow flow, ratings, and comments.

During open dispute:

- Both parties see `waiting for third-party review` status and original submission.
- Escrowed reward and reward slots remain frozen.
- Existing ratings and trust history are visible.
- Admin must preserve original acceptance reason, dispute statement, and final conclusion; conclusions cannot erase original evidence.

Admin may take two actions:

- Pay from escrow: confirm Issue, PR, completion validity and transfer one reward from bounty escrow to contributor. Mark challenge as approved. This action is single-use.
- Reject appeal: no formal reward transfer, record neutral admin conclusion. Rejection indicates current evidence is insufficient or claim is not established; it does not automatically imply bad-faith from claimant.

An arbitrator must be a true third party. If an admin is the bounty owner or assignee for the same challenge, system blocks both pay and reject actions in the same database transaction.

Arbitration result, status, and timeline are visible to both parties. Pay/reject actions require explicit second confirmation in both web and MCP.

## 7. MCP End-to-End Support

Each user can generate a dedicated personal MCP token and access `/mcp` with Bearer Token.

MCP follows MCP `2026-07-28` with stateless streamable HTTP. The database stores only a token hash; plaintext appears only once at generation or rotation and can be revoked any time.

The web app provides one-click copy prompts containing endpoint, MCP spec version, auth method, and operation principles.

MCP covers full lifecycle: draft, publish, accept, submit evidence, accept, pay, reject, tip, rating, dispute open, query dispute, and admin adjudication.

For sensitive actions involving balance, escrow, refunds, exits, public ratings, or dispute rulings, AI must display exact action, target, evidence, fee, and balance impact before explicit confirmation. Confirmation state is bound to user, tool, and normalized parameters with short life and one-time use.

Publishing, acceptance payment, rejection, closure refund, tip, rating, dispute creation/judgment, draft deletion, and withdrawal are all persisted within the same transaction as business mutation. If a response is lost, retries return committed results and do not duplicate charges, transfers, ratings, or freezes.

## 8. Balance and Refund Rules

Assume sponsor sets:

- reward per fix: `20`
- slots: `3`
- admin bounty fee: `2.5%`

One-time publish charge:

`20 × 3 + 60 × 2.5% = 61.5`

Funds movement:

- `60` enters reward escrow.
- `1.5` is platform bounty fee and non-refundable.
- `1.5` check-in reward can be paid from sponsor general balance.
- Each approved fix transfers `20` from escrow to contributor.
- If the project is closed after 1 approved fix, remaining `40` returns to sponsor balance.
- Transferred rewards and platform bounty fee are non-refundable.

System logs include fee records, escrow in/out, normal and dispute transfers, tip transfers, and escrow refunds.

## 9. Status Definitions

### Project Status

- Draft: not paid and not visible.
- Published: visible in bounty market and accepting challengers.
- Paused: no longer accepting new challengers.
- Completed: all reward slots paid.
- Closed: manually closed and unused escrow returned.

### Challenge Status

- Accepted: contributor reserved a slot; no evidence submitted yet.
- Submitted: Issue or PR and completion notes submitted, waiting review.
- Approved: review passed and reward transferred.
- Rejected: review failed; reward and slot remain escrowed for 7 days.
- Withdrawn: contributor left challenge.
- Sponsor canceled: sponsor canceled an unsubmitted challenge and slot is released; submitted challenges cannot enter this state.

### Dispute Status

- Pending: evidence and funds frozen, waiting administrator verification.
- Resolved by payment: admin confirmed and executed escrow transfer.
- Rejected: admin did not approve and saved conclusion/time as visible record.

## 10. Reward Ineligibility Conditions

Examples include but are not limited to:

- Non-reproducible or no-impact issues.
- Fake, forced, or inflated defects.
- Already existing duplicate Issue.
- PR unrelated to bounty or clearly out of scope.
- Mechanical-only edits (formatting, dependency bumps, mass search/replace).
- Missing tests or unverifiable fixes.
- Issue/PR not in bounty repository.
- Duplicate submission of same PR.
- Spam, harassment, automated reward farming.
- Code changes motivated solely by reward collection rather than solving real engineering problems.

Initial acceptance is sponsor responsibility. If dispute arises, third-party administrator decision based on system evidence is final.

## 11. Short Promotional Copy

Find a real bug, document reproducible steps, and submit both Issue and fix PR. After sponsor review, escrowed reward is transferred directly to your API balance.

Anyone can publish open-source bounties with their own balance, including maintainers. No default projects are pre-pinned by the platform: full reward is escrowed at publish time, and a low, admin-visible fee is charged. Daily check-in rewards can directly offset this fee. Partial-tip support, public two-sided reputation, and third-party dispute tickets are available. If a merged fix is rejected without basis, administrators can verify evidence and force escrow transfer.
