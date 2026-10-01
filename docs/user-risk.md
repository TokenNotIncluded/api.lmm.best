# User risk assessment

Version 1 assigns an explainable score between 0 and 1. This is a review heuristic, not a statistically calibrated probability. It does not ban accounts or change payment permissions. Scores are recomputed when administrators load the user list, and can decrease as activity changes.

The first matching rule gives the account's own score:

| Rule | Score |
| --- | --- |
| Positive check-in rewards, zero consumed quota and API requests, outgoing transfers, no successful paid top-ups and no received transfers | 0.95 |
| Outgoing transfers, zero consumed quota, no successful paid top-ups | 0.65 |
| Outgoing transfers and zero consumed quota | 0.35 |
| Positive check-in rewards, outgoing quota greater than four times consumed quota, no successful paid top-ups | 0.45 |
| Positive check-in rewards, zero consumed quota and API requests, no successful paid top-ups | 0.25 |
| Otherwise | 0 |

A recipient of a claimed transfer from an account whose own score is at least 0.8 receives an association score of `max(sender own score × 0.9)`. The displayed score is the maximum of own and association scores. Thus a direct recipient of a 0.95 sender scores 0.855. Association does not propagate recursively through other recipients. The number of distinct qualifying senders and explicit reasons are returned.

High risk is `score >= 0.8`, medium is `0.4 <= score < 0.8`, low is `score < 0.4`. The finite version-1 scores allow inclusive API maximums 0.799 and 0.399 to represent the upper-exclusive UI bands.

Pending and claimed transfers count as transferred out because balance was debited at creation. Cancelled transfers are excluded. Only claimed transfers count as received or create a recipient association. The check-in rule describes observed behavior; signup gifts, administrator grants and other funding are not proven absent, so it must not be presented as proof of abuse or as a complete source-of-funds audit.

Administrator list/search endpoints support `risk_min`, `risk_max`, `transfers=sent|received|none`, `usage=zero|consumed`, `funding=paid|unpaid` and `checkin=yes|no`. Risk bounds must be finite numbers in [0,1] and minimum cannot exceed maximum. Sorting also supports `risk_score`, `transferred_quota`, `received_quota`, `checkin_quota`, `used_quota` and `request_count`. Filtering, counts and stable sorting occur in SQL before pagination. Risk details are administrator-only and absent from normal account responses.

The UI persists sorting and filters in its URL, defaults to all trust levels, shows active filter chips outside the collapsed panel, and resets filters and sorting together. Transfer totals and risk explanations are available on desktop and mobile.
