# Product analytics

The seller center's **Product analytics** shows only that seller's products.
Administrators may switch to all sellers; only a superadministrator may change
retention settings. The responses contain aggregate counts and product/seller
identities, with no buyer identity, pickup token, email, IP or user-agent data.

An impression requires at least half of a catalogue card to remain visible for
500 ms in a visible browser tab. A detail visit means a product detail page
successfully loaded, including a direct link. These are event counts, not unique
people. Renders, card remounts, filters and view changes within one mounted page
do not count a second time. Merchant self-visits, private test products and
management previews are excluded. Tracking is optional: reporting failures
never block browsing, payment or delivery. New traffic is collected only after
capability seven is activated; historical impressions and visits are not
invented or backfilled. Until then, the API reports unknown traffic as `null`.

Orders come directly from permanent order records. Windows use UTC calendar
days and select orders by their creation date. Paid and completed-refund counts
describe those orders as of the query, including later refunds. Self-purchases
are excluded from the public sales funnel. A verified positive payment counts
as paid; a free claim does not manufacture a monetary payment. Net paid quantities
subtract the actual returned quantity recorded by completed refunds, capped at
the original quantity. This includes full refunds and final amount refunds that
retire the remaining delivery. Partial amount refunds with no returned items do
not reduce item quantities. An order
can belong to both refund-type subsets while the total refunded-order count
still counts it once. Neither traffic nor analytics changes a price, stock
reservation, wallet or financial record.

Anonymous daily traffic aggregates default to 365 days, and hashed
page/product/event receipts default to seven days. Both periods are configurable
through the superadministrator's retention controls. Each accepted report
removes at most 100 expired receipts and 100 expired aggregate rows. The API
also filters expired aggregates immediately when reading. No permanent order,
payment, refund or user records participate in this cleanup. Reports from pages
older than the receipt period are refused, so a discarded receipt cannot cause
a normal old-page retry to increase a counter again. As with other browser
analytics, these client-reported events are not proof of a sale or fraud-proof
unique-user measurements.

The all-time view retains all order history but can show only retained traffic;
it hides rates rather than comparing these different periods. Rates are also
hidden if a chosen window exceeds traffic retention. Counts with matching
windows still compare events rather than attributing a specific order to a
specific visit, and may exceed 100%.

Capability seven uses the same explicit prepare/verify/activate sequence as
fixed-content delivery. Its exact additions are the two encrypted content
tables and `merchant_store_product_traffic_days` /
`merchant_store_product_traffic_receipts`. Previous floor verification excludes
all four additions. Installing them does not activate the traffic writer.
