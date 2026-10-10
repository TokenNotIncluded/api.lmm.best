# Promotions extension (task 09)

This module owns immutable campaign policies, bounded application counters, claim state and operational audit. It does not own identities, balances, a treasury, coupon redemption or the ledger. All amounts are integer `credit_500k_usd` units: 500,000 units are USD 1. Discounts use integer basis points (100 basis points = 1%). Display currency does not change these units.

## What is implemented

A policy defines enabled/time-bounded campaigns, revision, coupon value/referral reward ceiling, discount ceiling, campaign-wide budget and claim count. Seven explicit level policies independently bound coupon value, referral value, discount, lifetime total value and total claim count. Pending claims consume budget. Defaults are finite at every level, including L5/L6; L0 referral rewards are disabled. Defaults are conservative configuration examples, not permission to issue real value. No campaign is enabled by default and the model cannot change policy. The constructor copies policies so caller mutation cannot silently change limits.

Every operation starts with fresh Rust identity authorization. Only personal session scope is supported. The verified referral subject must differ from the referrer, and its core proof must bind the beneficiary, account, campaign/revision, subject, value, discount and expiry. A user-supplied evidence reference is only a lookup hint, never proof that an invite or purchase happened.

Coupon claims are unique per campaign and beneficiary. Referral claims are unique per campaign and core-verified subject, even if request IDs, proof IDs, app instances or sessions change. The first accepted terms are immutable. A hashed application lookup supports recovery even after eligibility expires or is consumed. This link contains no raw evidence reference or credential. There is at most one original application link per claim; using another alias cannot expand a budget or change the grant.

The extension first reserves the user's and campaign's bounded counters and stores `pending` with a deterministic grant key in one transaction. It then asks Rust for that exact grant outside the database transaction. It accepts only a receipt bound to the same request hash/key and required coupon/journal identity. Explicit permanent rejection releases counters once. Timeouts, unavailable dependencies and malformed receipts do **not** release reservations or create compensating transfers. Retry the original request, or call `Reconcile` with the same claim key. A `pending` HTTP/tool result is a recovery handle, not a usable coupon or a posted reward. The assistant ends its turn on that result rather than looping.

## Required Rust integration — intentionally unavailable here

The existing `CoreControl` contract only supplies identity/team reads. Existing ledger/payment contracts do not provide a campaign grant with independent limits and verified referral evidence. A generic transfer, fake payment credit or service token is not an acceptable substitute.

`Rewards.Evaluate` and `Rewards.Grant` are **Go service ports**, not new wire contracts or implemented Rust RPCs. `UnavailableRewards` fails closed. Production assembly must keep it until Rust implements and advertises a narrowly scoped reward capability. This task does not change Protobuf files or core authority.

The Rust implementation must:

1. Independently authenticate the service and current user on every call and replay. Reject unknown campaigns, invalid scopes, self-referrals and unverified events. The extension must not be able to authorize its own reward.
2. Own the campaign's approved treasury/budget, level ceilings, stable entitlement proof, referral attribution and maximum cumulative reward. Validate the submitted proof/reference rather than trusting Go's amount or beneficiary fields. Policy changes cannot increase an already reserved request.
3. Within one core transaction, enforce global entitlement uniqueness and limits, consume the entitlement, post the balanced ledger transfer or issue an account-bound single-use coupon, record the exact operation receipt and enqueue the corresponding outbox event. Coupon redemption and any discount/value cap must be enforced by Rust/the authoritative purchase path, never solely by the assistant.
4. Return the same durable receipt for an exact retry, including a permanent rejection. Reject a changed request under the same key. A missing response may mean committed. The receipt's `RequestHash` uses the exact `GrantRequest` JSON field order/types emitted by `RequestHash`; version and test this canonical representation before defining a wire adapter.
5. Preserve completed grant/entitlement uniqueness across restarts and extension data loss. Go counters are a second, conservative gate; they are not the only money-safety control.

Until that integration and real-ledger acceptance are complete, this module can validate requests, expose limits, stage/recover fixture-backed tests and serve nonfinancial assistant features; it **cannot issue live rewards**. Do not deploy the branch as a working rewards service.

## Storage and routes

Install `schema.sql` explicitly in a fresh dedicated promotions database; inject a pool with only `lmm_promotions` access. The adapter rejects a database-name mismatch, an absent/wrong contract or any `core_*` schema. It never opens a pool, runs a migration or accesses another module's tables.

The constrained `records` families are claims, original application links, per-user totals, campaign totals and metadata-only events. Totals are application limits, not user or wallet records. A database-wide promotion scope lock serializes short transactions across service instances. It intentionally favors correctness over maximum campaign throughput; core calls happen after commit. Keep claim/application tombstones for the lifetime of the campaign identifiers. Do not reuse campaign IDs to reset uniqueness.

The service implements `modules.Module`; register it explicitly during final assembly. Routes are relative to `/extensions/v1/promotions`:

| Method | Route | Input |
| --- | --- | --- |
| GET | `/limits` | authenticated personal session |
| POST | `/eligibility` | `campaign`, `kind` (`coupon`/`referral`), `evidence_ref` for referrals |
| POST | `/apply` | same input; never amount, recipient, role or user ID |
| GET | `/claims/{key}` | owner only |
| POST | `/claims/{key}/reconcile` | owner only; retries the original grant |

Unit tests cover all levels, duplicate/concurrent claims, lifetime and campaign limits, unknown commit recovery, stale eligibility replay, permanent rejection, malformed proofs/receipts, account isolation, revocation and unavailable dependencies. The sibling `assistant/pgtest` suite additionally tests actual PostgreSQL transactions and independent pools. Reward posting remains a controlled core fixture in these tests; it is not evidence of live Rust funds integration.
