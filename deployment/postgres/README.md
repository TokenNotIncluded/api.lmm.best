# Explicit offline core roles

These scripts target a new isolated PostgreSQL 18 database. They do not initialize
schema, upgrade data, change production or run during startup/package installation.
Keep the administrator, NOLOGIN object owner and restricted runtime separate.
Go extensions never receive a core credential.

Run `create-core-roles.sql` explicitly as database administrator before
explicit `lmm-core --config /absolute/private-init.toml init-db`. It creates NOLOGIN `lmm_core_owner` and unprivileged LOGIN
`lmm_core_runtime` without a password. Existing same-name roles reject the
transaction; this is not a repair/reset tool. PUBLIC loses database CREATE/TEMP
and public-schema CREATE; only the offline owner receives installation CREATE.

Set the runtime password in an interactive private administrative psql session:

```text
\password lmm_core_runtime
```

For Docker, use the complete `dc` function from the [Docker guide](../docker/README.md):

```sh
dc exec core-db psql -X -U lmm_core_admin -d lmm_core
```

Use the same secret in the runtime URL file. Never put passwords in `psql -c`,
`-v`, Docker arguments, shell history or evidence. The isolated fixture uses
synthetic passwords and sends administrative SQL over private stdin.

The install URL authenticates the administrator and includes connection option
`options=-c%20role%3Dlmm_core_owner`. Session user remains administrator while
current user and new application objects belong to the NOLOGIN owner. Runtime
gets no owner membership. Verify actual object ownership after installation;
do not use whole-cluster `REASSIGN OWNED`. Mount this URL only into `core-init`.

Select an absolute version 1 init TOML whose `[database] url_file` references only
the private owner URL file. Do not select the runtime TOML or use legacy environment
configuration. The Docker `core-init` service already selects its separate
[init template](../docker/config/core-init.toml) and owner secret.

Run that explicit `--config` init command once, then execute `grant-core-runtime.sql` as
administrator. Its non-secret, quoted psql identifier variable `runtime_role`
defaults to `lmm_core_runtime`. Feature switches default to false:

| psql variable | Explicit capability |
| --- | --- |
| No feature flag | Identity, read-only ledger, controlled ledger functions |
| `enable_events=true` | RPC events and durable inbox/outbox |
| `enable_console=true` | Read-only config/setup and append-only announcement reads |
| `enable_models=true` | Charging and controlled model checkpoint/recovery functions |
| `enable_web_identity=true` | Budget management and read-only consumption for legacy Web identity |
| `enable_commerce=true` | Commerce, charging and events |
| `enable_payments=true` | Payment intents/refund holds and append-only receipts/proofs |

These flags grant rights only: they do not enable HTTP modules or supply provider
credentials. There are no wildcard grants or default ACLs for future objects.
Legacy Web identity alone cannot write model requests, holds or consumption.
Import records remain read-only. Runtime receives no opening-balance import
function, owner switching, DDL, TRIGGER, direct ledger or model checkpoint writes.
A false flag does not revoke previous grants; revocation is a separately reviewed
offline operation, never an automatic startup mutation.

Startup independently checks exact authority and enabled capabilities before
binding a listener. Roles/GRANTs are excluded from the catalog fingerprint and do
not change schema definitions. Actual container acceptance remains separate from
static SQL/Compose checks; run the new fixture on final frozen sources before
claiming the Docker flow works.
