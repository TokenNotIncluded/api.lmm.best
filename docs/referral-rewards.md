# First-paid-top-up referral rewards

An invitation is bound at registration to an existing enabled account. Registration increments the invitation count but no longer issues inviter/invitee referral credit. General `QuotaForNewUser` gifts are unchanged. Existing balances and historical affiliate totals are preserved; old signup rewards are not retroactively attributed or clawed back.

## Policy and rollout

Configure **Billing → Quota Settings**. `QuotaForInviter` is the fixed reward in raw quota units (0 disables new awards). `ReferralMinTopUpQuota` is the minimum credited platform quota of the first paid top-up, not its currency amount. `ReferralMaxRewardQuota` caps a reward (0 means no additional cap). `ReferralPenaltyPercent` defaults to 20 and accepts integers 0–100. `ReferralMaxPenaltyQuota` caps the extra penalty (0 means no additional cap). All quota values must be non-negative JavaScript-safe integers. Payment compliance confirmation is still required. Existing reward amount is retained, not enabled or increased by this migration. `QuotaForInvitee` is a retired compatibility option with no reward effect.

The first verified cash top-up sets a permanent eligibility marker even if rewards are disabled, the threshold is not met, the inviter is ineligible, or the account already has a historical successful cash top-up. Do not reset that marker on refund or appeal. No retroactive catch-up is performed. Manual order completion is explicitly excluded from referral eligibility and does not consume the first real top-up. Historical manual completions cannot reliably be distinguished from older paid orders, so legacy successful cash rows conservatively consume eligibility.

Only signed settlement through `CompleteExternalTopUp` grants rewards: Stripe, Creem, Waffo, Waffo Pancake, and CNY ePay cash payments. LinuxDO/community credits, wallet transfers, gifts, admin adjustments, manual completions, and subscriptions do not qualify. The standard payment-evidence and quota-capacity validation remains in force. There is one reward per invitee and one per top-up, enforced in the database. Payment credit, first-payment marker, reward credit and ledger entry commit or roll back together.

## Moderation and refunds

Ordinary **Disable/Enable** does not touch rewards. Use **Ban for abuse** for a confirmed abuse or bulk-registration decision; evidence is required. The corresponding reward is revoked. The extra penalty checkbox defaults off and requires explicit administrator confirmation of inviter involvement. Same-IP inference alone is not a supported automatic penalty trigger. Policy is snapshotted when a reward is earned; later setting changes do not alter that grant's penalty.

`POST /api/user/manage` accepts `action=ban_abuse`, `reason=abuse|bulk_registration`, `evidence` (1–1000 bytes), `penalize_inviter` and a stable `request_id`. Reuse the exact payload and request ID on retry; changing it is rejected. The acting administrator is revalidated against persisted status/role and must outrank the target. No reward means no affiliate financial deduction. Repeated bans never deduct the same grant again.

Use **Overturn abuse ban** (`action=restore_referral`, `reason=mistaken_ban`, evidence, a new request ID, no penalty flag) to enable the account and append reversal entries. Replaying a previously processed ban after an appeal cannot disable the account or charge again. An ordinary re-enable does not silently restore rewards. A subsequent new confirmed ban may revoke a restored award once again, with a new revision.

Cumulative full refund of the original top-up revokes its referral reward without a new abuse penalty. Partial refunds retain it until the original paid amount is fully refunded. If a full refund follows a ban, an appeal may reverse the penalty but cannot restore the invalid reward. Refund provider verification and existing payer-wallet refund constraints are unchanged.

## Accounting, privacy and operations

`users.aff_quota` is a signed reward-only balance. Negative values are debt; future rewards repay it before transfer becomes possible. Purchased `users.quota` is never reduced by referral moderation, even if a former reward was already transferred or spent. `aff_history` remains lifetime gross earned credit, not net earnings. The self API exposes non-negative `aff_quota` plus `aff_debt`. The reward history API `GET /api/user/self/aff/rewards?before=<cursor>` returns at most 50 entries, available balance, debt and current public policy. Identity comes only from authentication; no invitee emails, payment IDs or private evidence are returned. Entries show award, clawback, penalty and appeal reversal separately.

Main and fast migrations create `referral_rewards`, `referral_ledger_entries`, `referral_moderation_events` and add the first-payment/manual-exclusion columns. Do not drop them on application rollback. Roll all settlement/moderation writers to this version together; old binaries do not understand the first-payment contract and still issue signup rewards. Keep a database backup and run the provided payment/concurrency/moderation/refund tests before enabling the policy. No production balances or options are modified by tests.
