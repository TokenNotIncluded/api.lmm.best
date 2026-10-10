# Fresh core database

This branch targets a new installation. It does not upgrade, import, convert, or backfill the Go database or an earlier experimental Rust schema.

The Rust core is the only owner of identity, account permissions, funding rules, ledger state, budgets, subscriptions, and model routing. Go extensions own separate databases and call the core through versioned Protobuf services. An extension must never receive a core database credential.

Schema installation must be an explicit offline command. It must run in one transaction, fail on a non-empty database, and never drop existing data. Normal startup only checks the schema contract; it must not create or alter tables. There is no reset command, old-table fallback, or automatic schema repair.

Keep account ownership, the acting user, and the payer separate. Store monetary values as integers with explicit units. Store references, funding order, and permissions in relational columns. JSON is only for bounded metadata and immutable payloads.

A schema definition is not a completed billing engine. Request reservation, settlement, refunds, budgets, duplicate protection, and crash recovery need runtime tests before the model API can become ready.
