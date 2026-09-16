# Incident 343 owner diagnosis

The maintainer delegated production recovery on 2026-09-16. The manual
operator workflow in LIghtJUNction/api.lmm.best is a fork; its first authorized
request (35131181048) passed all 50 controller/diagnostic/authorization tests
but had no production SSH credentials. No server connection was made there.

Use the original protected production environment in this repository; do not
export or copy credentials between repositories. The `LMM assistant server ops` workflow in this production repository
shares the deployment lane and accepts an explicit owner commit changing only
`.github/server-ops-343-request.json`. All other pushes cannot diagnose or
deploy. Component releases deploy through their final job; all native safety gates
remain unchanged. The diagnostic trigger was moved out of the old deployment
subscriber, so a request is handled exactly once.

The request validator permits only actual LIghtJUNction actor/sender identities,
main, an unforced single-parent request-only commit less than 30 minutes old,
a matching base SHA, confirmation and fixed diagnostic-script digest. It
rejects bots, replays and arbitrary repairs. This is a distinct authenticated
push entry, not a forged workflow_dispatch. Protected environment approval,
pinned SSH, timeout, private audit logs and failure propagation still apply.

The copied transport is the exact blob already tested in the owner's ops
workflow, pinned again before credentials. It is used only as a transport by
the new validator; its original fork-only manual CLI is not an upstream entry.
The diagnostic script only reads service metadata and bounded logs. It makes
no migrations, restarts, rollbacks, admission changes or transaction changes.
A failing public health check remains failure, not successful recovery.
