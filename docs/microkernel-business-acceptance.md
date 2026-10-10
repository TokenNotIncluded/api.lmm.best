# Business extension acceptance still required

The old single-server qualification tests cannot execute after their database, router and models have been removed. Their removal does not demonstrate that replacement business modules work. The following behaviors must receive executable tests when the corresponding Rust commands and Go modules are implemented.

- Paid tools: separate buyers, client identities and authorization scopes. Duplicate calls must not charge twice. Concurrent reservations must respect balances and all applicable budgets.
- Settlement: concurrent completion, expiry, refund and cancellation must yield one valid accounting result. A late job must not undo a later committed state. Each change must have an audit record.
- Team funds: platform administration is not permission to spend team money. Team keys cannot fall back to personal wallets. Membership removal and rejoin cannot restore old credentials or reset budget consumption.
- Payments and shops: provider callbacks must be deduplicated. Prices and payment sources must be fixed per operation. Refunds must use the original source. Go cannot directly edit core balances.
- Streaming: extension shutdown, upgrade and overload must not break an accepted core stream or its settlement. A core crash requires a separately tested recovery design.

Current executable coverage is the Rust identity/account schema and authorization tests, Go host/client tests, shared Protobuf contracts and Docker process-isolation tests. It is not a complete store, payment, billing or streaming qualification.

The retired Go-only portions of the CoWeft, CLI OAuth, editor/provider registration, profile aggregation, wallet and tool-market workflows have also been removed. Their retained client/frontend checks remain useful but do not verify a replacement identity or financial backend. Native Rust OAuth and each future Go module need new executable backend acceptance tests.
