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

After Go 87 compatibility is actually published, the central CI owner must
provide that release's reviewed local checkout and exact revision. No future
release tag or unissued source SHA is presumed here:

```sh
python3 scripts/build-merchant-store-cap1-probe.py \
  --cap1-source-tree "$REVIEWED_CAP1_CHECKOUT" \
  --cap1-source-revision "$REVIEWED_CAP1_REVISION" \
  --output-directory "$NEW_PROBE_OUTPUT"
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
Until central CI supplies its reviewed source and executable, this feature is
not fully connected to Server CI.
