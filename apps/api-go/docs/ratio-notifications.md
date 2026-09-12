# Ratio change notifications

An accepted option batch creates one durable `ratio.changed` event in the same
transaction as the option writes. Re-saving equivalent JSON creates no event.
Price-locked changes are filtered before the event is created. An event storage
failure rejects the save; a later delivery failure does not change saved prices.

Supported maps: ModelPrice, ModelRatio, CompletionRatio, CacheRatio,
CreateCacheRatio, ImageRatio, AudioRatio, AudioCompletionRatio, GroupRatio,
GroupGroupRatio. Changes include `option`, `model` or `group`, optional
`user_group`, `old`, and `new`. Null means the explicit entry was absent or
removed; the configured fallback now applies. `effective_at` is Unix seconds.
Dynamic pricing calculations are outside this option-change feed.

## In-app feed and management APIs

- `GET /api/ratio-notifications?before=<event_id>`: authenticated user feed,
  at most 50 source events per page. Follow `next` even for an empty filtered page.
  Each entry contains `event_id`, `effective_at`, `type`, and `changes`.
- `GET /api/ratio-notifications/deliveries?after=<delivery_id>`: root-only status
  list, at most 100 rows in increasing ID order. Records contain event/user IDs,
  status, attempts, next attempt time and a bounded failure reason; no secrets.
- `POST /api/ratio-notifications/deliveries/:id/retry`: root-only, audited retry
  of a failed delivery. Resets its five-attempt budget, retaining its event ID.
  Delivered, pending, sending and skipped records cannot be retried here.

The frontend must render this feed as announcements; no frontend is included in
this backend change. Deduplicate announcements by event ID. These announcements
must not be copied into an unfiltered public/global announcement option.

Visibility is deliberately conservative: a user sees their current billing
group and models backed by enabled channels for that group. Overrides belonging
to other user groups are excluded. Additional selectable groups are not included.
Both feed reads and every webhook attempt recheck current visibility and account
status. A changed scope may therefore change a retry's payload for the same event.

## Webhook subscription and verification

Reuse profile notification settings: `notify_type: "webhook"`, `webhook_url`,
and a nonempty `webhook_secret`. No separate endpoint or email subscription is
created. The dispatcher enumerates enabled subscribers asynchronously in batches
of 100; subscriptions are selected when that batch is processed and rechecked
before each attempt. Email/Bark/Gotify preferences are never sent these events.

The JSON body contains `type: "ratio.changed"`, `event_id`, `effective_at`, and
filtered `changes`. `X-Webhook-Event-ID` repeats the event ID.
`X-Webhook-Signature` is lowercase hexadecimal HMAC-SHA256 of the exact raw body,
using the configured webhook secret. Verify in constant time before parsing,
then record event IDs transactionally to deduplicate. Retries use the same ID;
delivery is at least once, including a crash after receiver acceptance.

Only HTTPS port 443 is allowed. Proxy environment variables and worker forwarding
are not used. DNS answers are checked at dial time and the checked IP is dialed
directly; private, loopback, link-local and special-use ranges are rejected.
Redirects are not followed. The policy cannot be disabled by fetch settings.

The worker polls every 10 seconds, sends sequentially, and times out each request
after 15 seconds. Attempts use a 60-second database lease and at most five sends,
with exponential delays of 30, 60, 120 and 240 seconds before the next attempt.
A crash consumes its claimed attempt; the final expired lease becomes failed.
Recipient/event uniqueness and conditional claims prevent normal duplicate work
across instances. Current preferences are used on retry, so operators/users can
repair an endpoint or signing secret before manually retrying a failed row.

The two new tables are included in normal schema migrations. Apply the repository's
normal migration/deployment procedure before activating the binary. There is no
automatic deletion/retention job in this change.
