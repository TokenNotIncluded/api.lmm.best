# Capability-one synthetic PostgreSQL probe

The variant PG suite requires `MERCHANT_STORE_POSTGRES_TEST_DSN` and an actual
`MERCHANT_STORE_CAP1_TEST_BINARY`. It refuses to skip or simulate the old
capability when the executable is missing. The original eight PG cases stay
unchanged; three variant cases verify shared capacity, exact stock delivery,
and a real SHARE/UPDATE transition followed by old-model write rejection.

The fixture in `apps/api-go/model/testdata` is exactly the test-only source used
by the isolated capability-one run. It is copied into an exported reviewed
capability-one source tree; no production constant or source file is modified.
The builder requires a local checkout root and its exact reviewed SHA, verifies
capability 1 and every source file against Git, and disables dependency downloads.
It performs no checkout/fetch, migration, server start, or production config read.

Server release qualification now selects 13 named merchant test parents and
requires all 20 PostgreSQL child cases, every parent, and the package to pass
without any skips or failures. Its wrapper checks out the exact reachable
ancestor `7ad0469eeaa83bddf45cdd45d225c92bf6078421`, verifies its Git signature
against the pinned public signing key, and retains the builder's clean/archive
and exact-capability-one guards. The merchant model source and Go module files
at that pin match the originally reviewed capability-one source. The full
module is not represented as an official Go 87/N-1 release.

The current module dependency cache is warmed before the old-source builder;
the builder still uses `GOPROXY=off`, `GOSUMDB=off`, `-mod=readonly`, `-p 1`, and
`GOMAXPROCS=2`. Missing cached dependencies fail the job rather than enable
network fallback. The actual old executable first runs a database-free getter
that must report compiled capability 1, then runs the activation probe in the
owned PostgreSQL schema. Evidence and failed compilation logs are uploaded by
the existing qualification artifact step.

To build the same synthetic probe manually from an explicitly reviewed local
checkout (the builder itself performs no checkout/fetch):

```sh
python3 scripts/build-merchant-store-cap1-probe.py \
  --cap1-source-tree "$REVIEWED_CAP1_CHECKOUT" \
  --cap1-source-revision "$REVIEWED_CAP1_REVISION" \
  --output-directory "$NEW_PROBE_OUTPUT" --go-jobs 1 --go-procs 2
```

The builder receipt contains source/fixture/binary hashes. Set
`MERCHANT_STORE_CAP1_TEST_BINARY` to its `capability-one-model.test`, then run
the three PG test groups against a fresh loopback-owned database:

```sh
GOMAXPROCS=2 go test -p 2 ./model \
  -run '^TestMerchantStorePostgres(Concurrency|SalesLimit|Variants)$' -count=1 -json
```

All 11 subcases must actually pass with zero skips. Source validation alone does
not verify compilation. The synthetic model test binary is not an official N-1
release, two-host rollout, deployment-provider rollback, or full production-data
preservation proof. The gate SHARE holder in this test is the feature binary at
gate 1; the actual capability-one model binary executes after activation.
This documents the CI wiring, not a claim that a particular PR CI run has
already passed. Access/catalogue business cases remain separate SQLite tests;
the PostgreSQL preparation cases prove their real schema installation and
activation checks. The explicit guest-email PostgreSQL child must pass even
though its parent also contains SQLite checks.
