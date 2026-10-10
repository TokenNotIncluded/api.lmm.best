# Original-table preservation fingerprints

`scripts/fingerprint-credit-rebase-original-tables.py` only generates SQL offline.
It has no database client, apply mode, or connection arguments. The release
custodian executes its private artifacts while all writers remain frozen.

Generate an inventory query first:

```sh
python3 scripts/fingerprint-credit-rebase-original-tables.py inventory \
  --schema public --output /private/work/original-inventory.sql \
  --receipt /private/work/original-inventory-sql.receipt.json
```

Execute with `psql -X -qAt`, saving the single JSON result in a private
`original-inventory.json`. It inventories every ordinary and partitioned table
in non-system schemas of the target database, including existing audit tables.
The top-level `schema` identifies the plan's business schema; each table also
retains its own schema, logical column types, collations, and partition topology.

Generate each stage using that unchanged inventory and the independent verifier:

```sh
python3 scripts/fingerprint-credit-rebase-original-tables.py stage \
  --inventory /private/work/original-inventory.json \
  --plan /private/work/sealed-plan.json --stage after \
  --verifier scripts/verify-credit-rebase-plan.py \
  --output /private/work/original-after.sql \
  --receipt /private/work/original-after-sql.receipt.json
```

Use `--stage before` for the baseline. Run stage SQL with `psql -X -qAt`;
only qualified table name, row count and SHA256 are selected. The metadata guard,
independent stage assertions, and fingerprints share one repeatable-read,
read-only transaction. Foreign tables and RLS-filtered reads fail closed.

The fingerprint queries execute one table at a time on that same connection.
The outer query sorts labels and SQL text by the original qualified table label
with `COLLATE "C"`. `psql`'s
[`\gexec`](https://www.postgresql.org/docs/current/app-psql.html#APP-PSQL-META-COMMAND-GEXEC)
then executes each independent `SELECT` in that order. PostgreSQL never plans all tables as one
large `UNION ALL`. `ON_ERROR_STOP` preserves failure handling. Result bytes
remain `table|count|fingerprint`, in the same order, including empty tables and
duplicate rows. Keep using `psql -X -qAt`; a database driver that does not process
psql commands cannot execute these artifacts.

Every generated transaction uses `SET LOCAL jit=off`,
`max_parallel_workers_per_gather=0`, `work_mem='4MB'`, and
`hash_mem_multiplier=1`. These settings apply before the stage assertions and
all per-table queries, then revert when the transaction ends. The inventory,
normalization, historical-content checks and deterministic receipt schema are
unchanged; newly generated SQL and generator hashes must be sealed together.

Each original column is encoded from its SQL text output inside canonical row
JSON, preserving SQL NULL versus JSON null, raw JSON text and array bounds.
Every row is hashed separately with PostgreSQL's built-in SHA256 over UTF-8;
sorted fixed-width row hashes are concatenated and hashed again, without removing
duplicates or aggregating original row text. Plan restorations reverse only exact
declared fields. Audit exclusions require the complete primary key, all original
columns, and prior independent stage verification. Other historical audit rows
remain in the fingerprint. Added columns and newly created tables are outside
the original projection.

Seal the generator, independent verifier, original inventory, both stage SQL
files, their deterministic receipts and the actual baseline result bytes.
Compare production and restored-clone results as complete bytes. Never recapture
the inventory after applying the migration. Generated SQL and receipts use
exclusive creation and mode 0600; existing output files are never overwritten.

This proves logical schema/content retention, not unchanged physical OIDs after
restore. Zero-column and foreign tables are rejected. PostgreSQL's approximately
1 GiB aggregate-value limit also bounds one table to fewer than 16,777,216 row
hashes; an unsupported table produces an error rather than a partial proof.
