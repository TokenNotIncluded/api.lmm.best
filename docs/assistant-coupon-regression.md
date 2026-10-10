# Coupon requests and referral explanations

An explicit coupon request may contain polite filler and references to daily check-in. `优惠券`, `有券就申请` and English `coupon` now reach the weekly-discount workflow. A mention of check-in inside an actual coupon application must not silently route it to a different reward. Refusal and read-only status questions retain their no-write behavior.

The decision still requires two substantive user turns, uses the current account-level discount ceiling and allows only one stored decision per UTC week. A zero-percent decision is a genuine declined award, not an unavailable application endpoint. No administrator restriction, anti-abuse guard or private-code ownership check is removed.

Incomplete conversation, invalid decision arguments and disabled account policy are distinct results. Storage failures are `service_error`, not evidence that an entry is closed or that the user used a weekly chance. Because a failed commit acknowledgement can be ambiguous, the assistant must read weekly status before retrying. It must never promise that a service error will disappear next week. Replaying an offered or claimed decision shows its existing card; it does not generate a second coupon.

The referral tool now separates earned transferable affiliate credit, wallet credit, debt and gross lifetime earnings. These values do not establish an individual invitee's first-payment status. The retired invitee reward option is not advertised as an active gift.

Regression coverage includes the reported Chinese coupon poem, its budget/use-case follow-up, explicit refusals, status-only questions, an incomplete conversation followed by a valid same-week application, the existing private-code ownership/claim replay tests, and the existing read-only status no-write tests. No production account or live reward setting is modified by these tests.
