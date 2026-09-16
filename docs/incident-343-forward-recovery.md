# Incident 343 forward recovery

Live owner diagnosis run 35132619961 confirmed Go 0.2.51 fails route startup
because red_packets is absent. The installed Go/Web pair remains signed
0.2.51-1 / 0.1.71-2. Managed billing records prohibit an old-writer downgrade.

The fixed incident helper corrects only missing schema. It requires the exact
incident identity, existing closed admission, verified original writer stop,
unchanged database connection, candidate package/archive/frontend identity and
all three target relations absent. It takes a fresh private pg_dump snapshot
and verifies its readable archive and digest before stopping the failed unit.
Only three reviewed model tables are created in one PostgreSQL transaction,
under the existing migration advisory lock. No existing table/data is altered.
Every model index must actually belong to its new table, be valid/ready and
match its unique flag and ordered columns before commit. GORM's IF NOT EXISTS
must not turn an unrelated index-name collision into incomplete recovery.

After correction the same installed binary performs native verify, readiness,
restart/memory/package checks, native admission reopening and full original
observation. Success means AWAITING_CONFIRMATION, not CONFIRMED. The transaction
lock and all previous evidence remain. A failed step is not automatically
retried or rolled back. Inspect the private outcome before a new decision.

The existing protected production workflow accepts a separate explicit request
operation recover-red-packet-schema-343. It is not disguised as diagnosis. All
owner identity, request-only commit, parent, freshness, script digest and replay
checks remain. Before production credentials are loaded, the exact checkout
runs all transport tests and real disposable PostgreSQL/native recovery tests,
then builds the static helper. A revision and SHA-256 receipt binds its bytes.
The helper goes through pinned SSH stdin to an exclusive private audit path;
no binary, credential or raw production error is printed to Actions. The normal
signed release/deployment path remains unchanged. This helper does not replace
the installed runtime, publish a release, downgrade or automatically confirm.

Tests are synthetic and isolated; passing CI does not establish live recovery.
Public version/pages/resources and the native transaction must be checked after
the operation. Confirmation is a separate normal native action after review.
