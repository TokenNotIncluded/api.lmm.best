# Setup record persistence at startup

When migration-write startup finds an existing root user without a setup record,
it creates the existing singleton setup record before marking setup complete.
Failure to persist that record now returns a startup error through the existing
migration-session cleanup path, instead of logging the error and continuing with
an initialized in-memory state.

An empty database remains legitimately uninitialized. An existing setup record
is preserved, and repeated successful checks do not create extra records.
Read-only startup still requires exactly one setup record and a root user.
This change does not alter first-time setup's transaction/concurrency protocol.
