# Sealed credit transition preparation

The ordinary Go provider always requires the four credit anchors to be 500000.
The preparation entry point makes the same provider installable before the
financial transaction without permitting any legacy-unit business operation.
After the financial transaction, the same installed provider can run normally
with its preparation environment removed. It is an actual compatible N-1
provider, provided its source includes all refund, referral, pending payment and
subscription reset/renewal migration semantics from the final reviewed build.
Version numbers and `migrate --verify` alone do not establish that compatibility.

Preparation is requested only by both process environment variables:

```
LMM_CREDIT_TRANSITION_PLAN=/etc/lmm-api-go/credit-transition-prepare.json
LMM_CREDIT_TRANSITION_SHA256=<sha256 of the exact file bytes>
```

Either variable alone closes startup. The file must be a root-owned regular
file, have one hard link, and have mode 0600 or 0640. Mode 0640 supports a service
group with read permission. Its canonical parent directories must be root-owned,
without group/other write permission or symlinks. No `.env` file may be present
in the command's working directory. The binary checks its own SHA-256 against
the plan, so changing a candidate requires a new reviewed preparation file.

The sealed JSON has this contract; all placeholder identities must come from
the actual target and reviewed transition intent:

```json
{
  "format": "lmm-credit-transition-prepare-v1",
  "transition_id": "credits-20261006",
  "transition_intent_sha256": "<64 lowercase hexadecimal characters>",
  "provider_sha256": "<64 lowercase hexadecimal characters>",
  "target_credits_per_usd": 500000,
  "database": {
    "system_identifier": "<PostgreSQL system identifier>",
    "database": "<database name>",
    "database_oid": 0,
    "schema": "<application schema>",
    "server_version_num": 0,
    "database_user": "<effective SQL_DSN role>"
  },
  "options": {
    "CreditsPerUSD": "<exact existing string>",
    "LegacyPricingQuotaPerUnit": "<exact existing string>",
    "QuotaPerUnit": "<exact existing string>",
    "PublicCreditsPerUSD": "<exact existing string>",
    "USDExchangeRate": "<exact frozen FX string>"
  }
}
```

The preparation configuration rejects a database whose four anchors are already
canonical. It never derives a conversion from the old anchor or from FX, and
never publishes the old anchor through `common.SetCreditCurrencyBasis`.
The runtime database role must be able to read `pg_control_system()`; lacking
that permission is a failed gate, not permission to assume an identity.

With this environment, the existing commands have a separate bounded behavior:

* `migrate --apply` opens only the primary PostgreSQL connection and adds the
  three dormant pending-credit columns on `top_ups` and nullable `reset_amount`
  and `renewal_amount` columns on `user_subscriptions`, in one transaction.
  It does not run the ordinary migration, setup, authorization, built-in
  catalog, options, log database, Redis or any financial update. It validates
  existing columns instead of repairing incompatible definitions.
* `migrate --verify` is read-only and requires the exact target identity,
  sealed option strings, all five compatible columns, empty optional financial
  audit tables and dormant new columns.
* `serve` performs that read-only verification and starts only a loopback HTTP
  listener. There is no business router, worker, cache warm, option synchronizer
  or subscription/payment task. Only direct local GET `/api/status` and
  `/api/livez` requests can report preparation readiness. Forwarded requests,
  nonlocal hosts and every other path/method receive HTTP 503 with the exact
  body `lmm-credit-transition:<transition_id>` and `Cache-Control: no-store`.

Preparation health includes `maintenance=true`, `business_enabled=false`, and
`data.credit_transition` binding the transition ID, transition intent hash,
preparation file hash, actual provider hash and target 500000. It means only
that the sealed preparation is intact. It must be consumed by the explicit
maintenance deployment contract, never treated as an ordinary business health
response. Every health read verifies the database again; anchor/FX changes,
activated preparation fields or financial audit rows close readiness.

On a clean signal shutdown the process emits the distinct evidence
`credit_transition_prepare shutdown_complete=true business_enabled=false`
and `server exited`. It does not fabricate normal refund-worker completion
reports. Normal deployment owners must recognize this evidence only when the
provider and preparation health binding have already been verified.

Preparation alone does not transfer deployment locks, open admission, execute
the reviewed financial SQL, confirm all writer nodes or establish production
readiness. Those actions belong to the supported maintenance handoff and normal
deployment owners. Before money changes, install and confirm this exact bridge
on each writer node with admission closed. After money changes, remove only its
bound preparation environment, start the compatible canonical provider, verify
each node, and release admission only through the deployment owner's explicit
maintenance-release action after the complete cluster is ready.

Focused validation uses `GOMAXPROCS=2 go test -p 1 ./internal/credittransition
./model . -run 'Test(SealedPreparation|Preparation|CreditPreparation)'`. The real
PostgreSQL test additionally requires `CREDIT_TRANSITION_TEST_DSN` pointing to a
local isolated database named `credit_transition_test`; it refuses application
database names or remote servers. It proves schema preparation preserves the
fixture's old wallet, used quota, payment facts, subscription facts and options,
and checks idempotence plus fail-closed changes. A restored production rehearsal
and the final integrated artifact's full runtime compatibility checks remain
separate acceptance gates.
