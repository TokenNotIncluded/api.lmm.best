# User risk assessment

Version 2 assigns an explainable score between 0 and 1. This is a review heuristic, not a statistically calibrated probability or legal finding. The score itself does not ban accounts or change payment permissions. Scores are recomputed from committed records when administrators load the user list, and can decrease as activity or review resets change. Strict-mode review penalties are a separate, explicitly configured balance operation; see [OpenAI Moderation safety review](./moderation-security-review.md).

The final score is `max(own wallet score, wallet association score, own moderation score)`. The sources are not added together. Content-review risk never transfers to the recipient of a wallet transfer.

The first matching wallet rule gives the account's own wallet score:

| Rule                                                                                                                                      | Score |
| ----------------------------------------------------------------------------------------------------------------------------------------- | ----- |
| Positive check-in rewards, zero consumed quota and API requests, outgoing transfers, no successful paid top-ups and no received transfers | 0.95  |
| Outgoing transfers, zero consumed quota, no successful paid top-ups                                                                       | 0.65  |
| Outgoing transfers and zero consumed quota                                                                                                | 0.35  |
| Positive check-in rewards, outgoing quota greater than four times consumed quota, no successful paid top-ups                              | 0.45  |
| Positive check-in rewards, zero consumed quota and API requests, no successful paid top-ups                                               | 0.25  |
| Otherwise                                                                                                                                 | 0     |

A recipient of a claimed transfer from an account whose own wallet score is at least 0.8 receives a wallet association score of `max(sender own wallet score × 0.9)`. Thus a direct recipient of a sender with a 0.95 own wallet score has a 0.855 association score. Only the sender's own wallet score qualifies: neither the sender's moderation score nor their received association score propagates to the recipient. Association does not propagate recursively through other recipients. The number of distinct qualifying senders and explicit reasons are returned.

Completed OpenAI Moderation jobs for user input (`relay_input` or `assistant_input`) contribute to the account's own moderation score. Jobs are grouped by account and request ID before counting, so duplicate input sources for one real request count once. A request is flagged if any of its completed input jobs is flagged. Model outputs, failed or cancelled jobs, pending jobs and unreviewed content do not contribute to these counters.

| Flagged reviewed user requests | Moderation score |
| ------------------------------ | ---------------- |
| 0                              | 0                |
| 1                              | 0.4              |
| 2–4                            | 0.6              |
| 5–9                            | 0.8              |
| 10 or more                     | 0.95             |

`moderation_reviewed_count` counts completed user-input requests, while `moderation_flagged_count` counts those flagged. These are raw counters, not probabilities or model confidence. The thresholds are administrative heuristics rather than statistically calibrated measurements. Classification can be mistaken and is not proof of unlawful behavior.

An administrator's review reset changes the current content-risk view: only review jobs created after that account's reset cutoff are counted. It does not erase historical review records or penalty receipts, change the wallet-risk formula, or remove transfer associations. Without a reset, completed input requests are counted across the available history rather than a rolling 30-day window. Content review defaults to disabled, so introducing version 2 does not itself enable reviews or create violations.

High risk is `score >= 0.8`, medium is `0.4 <= score < 0.8`, low is `score < 0.4`. The finite version-2 scores allow inclusive API maximums 0.799 and 0.399 to represent the upper-exclusive UI bands.

Pending and claimed transfers count as transferred out because balance was debited at creation. Cancelled transfers are excluded. Only claimed transfers count as received or create a recipient association. The check-in rule describes observed behavior; signup gifts, administrator grants and other funding are not proven absent, so it must not be presented as proof of abuse or as a complete source-of-funds audit.

Administrator list/search endpoints support `risk_min`, `risk_max`, `transfers=sent|received|none`, `usage=zero|consumed`, `funding=paid|unpaid` and `checkin=yes|no`. Risk bounds must be finite numbers in [0,1] and minimum cannot exceed maximum. Sorting also supports `risk_score`, `transferred_quota`, `received_quota`, `checkin_quota`, `used_quota` and `request_count`. Filtering, counts and stable sorting occur in SQL before pagination. Risk details are administrator-only and absent from normal account responses.

The UI persists sorting and filters in its URL, defaults to all trust levels, shows active filter chips outside the collapsed panel, and resets filters and sorting together. Transfer totals and risk explanations are available on desktop and mobile.
