# Channel marketplace publication and recovery

The channel marketplace is the public relay pool, not the merchant store or tool market.

## Publication

A contribution is not routable until an administrator approves it. Approval now creates the channel, model abilities and contribution link in one database transaction. Any failure rolls the whole operation back. Repeating approval for an already linked contribution does not create another channel.

The server chooses the channel ID, public group, enabled status and initial counters. Contributor-supplied priority and channel IDs are not copied. The upstream credentials stay out of catalog and management responses. The shared channel form uses the same provider validation and key preparation as administrator channel creation. Batch submissions become one multi-key channel; public channel names never include a key prefix.

Submissions reject blank models or keys, unknown providers, malformed base URLs, literal private/local addresses and a server proxy supplied by the contributor. These checks do not prove upstream reliability or resolve every possible DNS or provider-specific network risk. Administrators must still review the submitted upstream and access policy.

## Recover older approvals

After deploying both the Go service and frontend, open Channel market → Review. Older records that were approved but never linked to a channel appear in the review queue. Approve them again to publish the stored configuration. Invalid legacy configurations fail without creating a partial channel; the contributor must submit corrected configuration.

No automatic database migration publishes historical submissions. Already linked records are not duplicated. Disabled, deleted or unrouteable channels are hidden from the catalog and routing choices, while contributors retain access to their records and available tip balance.

## Preferences, tips and reports

Routing refreshes preserve unsaved edits. Save routing writes the displayed choice set. At most 200 distinct channels can be configured; previously saved available channels are retained when the pool exceeds the display limit.

Available tips in management responses use the same wallet-unit calculation as withdrawal, including after a credit-unit change. Displayed historical totals are not a substitute for the available balance.

The review queue requests open reports only. Reporting a previously closed issue reopens the existing reporter/contribution record with the new reason and clears the old review details. Closing an already closed report is safe. A missing report returns an error rather than a false success.

## Verification

Publication tests cover single, pooled and batch credentials, real router selection, transaction rollback, repeat approval, legacy recovery, hidden unavailable channels and report reopening. Browser DOM tests cover access boundaries, shared form markup, currency display, loading/error states and unsaved routing choices. These automated checks do not replace a deployed-site test with a real upstream.
