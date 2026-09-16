# Incident 343 owner operations

The production repository is TokenNotIncluded/api.lmm.best. The earlier personal
fork did not hold SSH credentials; those credentials were not copied or exported.

The `server-ops.yml` workflow now owns both the manual main-only operator path and
the existing explicit-owner read-only request path. `deploy-production.yml` is only
a compatibility adapter for releases whose immutable source predates inline deploy.
All paths share the upstream production environment and deployment concurrency.

The fixed `.github/server-ops-343-request.json` request still requires an actual
LIghtJUNction actor/sender, a fresh unforced single-parent commit changing only that
request, matching parent SHA and diagnostic digest, and explicit confirmation.
It permits no arbitrary repair and no replay. Manual repairs have separate script
selection and operator checks. Both call the one reviewed `scripts/server-ops.py`
transport; there is no fork-specific transport or impersonated dispatch.

The corrected diagnostic accepts the real FatalLog bracket suffix. It only reads
selected service metadata and bounded logs and publishes fixed labels. It does not
migrate, restart, roll back, open admission, or confirm the native transaction.
A failing local/public check remains failure, not successful recovery.
