# Retained product retirement and writer floor 3

This Go/Web source candidate supports writer floors 1, 2 and 3. Merchant variant
names and integer-credit prices remain configurable. Variant inventory is
isolated, and orders freeze the selected variant ID and name.

Unlisting and deleting products require floor 3. At floor 1 or 2 the new
retirement endpoints fail without changing listings, inventory, orders or money.
Existing floor-1 and floor-2 binaries reject the value `3`; their original gate
parsers do not recognize it. Older binaries without the gate require the earlier
compatibility rollout first. These source properties are not a two-host release
or native artifact verification receipt.

After reviewed variant schema and every serving writer are ready, the separate
private operator action raises floor 2 to 3:

```text
lmm-api merchant-store-writer-gate activate-lifecycle --expected-current=2 --reviewed-lifecycle-ready
```

The action does no DDL, does not initialize runtime resources, refuses skipping
floor 2, and never downgrades. Repeating activation at floor 3 is idempotent.
Do not run this action as part of native verification-only checks or before the
separate deployment and compatibility proof has completed.

Deleted rows and their existing order, stock, payment and audit references remain
stored. New variant edits, listing changes and checkout fail; already reserved
or paid orders retain settlement and pickup. Replays return the original frozen
order rather than issuing a second delivery.
