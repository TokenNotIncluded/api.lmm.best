# Unified external-provider mock lab — first increment

**This is not completed application E2E acceptance. Do not merge or deploy on the strength of mock-component PASS.**

Reviewed integration branch: `wip/rust-core-go-extensions`, PR #675, commit
`bbde7468dcbd08091bc6a666c27946c2a385f2bf`. These additions do not enable public
model routes, register paid extensions, supply a fake core, or change production
network/verification policy. There is no new Rust dependency or resident core
worker. The Python process starts only when requested.

## Existing extension points and actual gaps

| Area | Existing code reused or inspected | This increment |
|---|---|---|
| Payment | `services/extensions/internal/modules/payments/{types,stripe,epay}.go`: `Adapter`, `NewStripe`, `NewEPay`, exact-body verification, merchant/event lookup | New `_test.go` files inject a local-only HTTP transport into the existing private client. Real adapters construct/parse requests; they are not replaced with a success stub. Full `Service.WebhookHandler` + database + Rust settlement remains blocked. |
| Model | `services/core/src/relay/`: real decoder/encoder, `RouteProvider`, `Billing::reserve/finalize`; upstream Chat/Responses/Anthropic/Gemini | Four separate wire fixtures. Their component tests run. No new Rust parser/relay integration test is supplied in this increment. Existing `relay_http` remains required. |
| Channel | Core routing plus the relay's immutable complete upstream URL/model/credential | Independent upstream instances can fail, recover and record attempts. No fake router or fallback loop is inserted. Actual channel registration/selection, breaker recovery, priority/group/weight and billing assertions still need a real application driver. |
| Tool market | `services/extensions/internal/modules/toolmarket/{mcp,network}.go`: actual MCP client and schema discovery | New `_test.go` tests actual wire client against the lab. They do not pretend to register/publish a paid tool or verify a wallet. |

The reviewed `services/core/src/http.rs` deliberately returns
`503 core_migration_incomplete` for readiness/model operations. The payment
README documents its missing registered Rust money service. The tool-market
README documents missing real `Authority` / `Funds` integration and disabled
paid-call registration. Preserve these gates. A mock gateway must not unlock them.

The relay README describes its public client side as Chat/Responses and its
upstream side as all four protocols. It explicitly rejects image/audio/video
content and other unsupported features. Consequently this increment supplies no
made-up multimodal support. Opaque reasoning/signature metadata and real Rust
conversion coverage have not been added here.

Existing payment, OAuth/MCP, PostgreSQL and relay fixtures are retained: this
increment does **not** replace all their coverage. Deleting them would lose tests.
No production file is changed.

## Run

Requirements: Python 3.11+; Linux `unshare` and `ip` for isolated execution.
The stdlib Go transport helper works with Go 1.23.2. The **real extension module**
requires the toolchain/dependencies declared by the checked-out `go.mod` (the
reviewed file says `go 1.25.0`). Do not lower it. The relay README mentions other
toolchain versions; use each checked-out module's actual pin, not that older prose.
Dependencies must be prepared beforehand in an approved development environment;
the test runner uses `GOPROXY=off`, `GOTOOLCHAIN=local`, `-mod=readonly` and no proxy
or production/database environment variables. It never downloads during testing.

From the repository root, the two foreground/local commands are:

```sh
# Terminal 1. Only providers start here; not an application or database.
umask 077
python3 -B scripts/mocklab.py up \
  --config tests/mocklab/example.json --manifest /tmp/lmm-mock-run.json

# Terminal 2. The output directory must not already exist.
python3 -B scripts/mocklab.py run \
  --manifest /tmp/lmm-mock-run.json --output /tmp/lmm-mock-report-01
```

The manifest must be new and mode 0600. Do not reuse another run's manifest.
Use a different path when another run exists. `Ctrl+C` stops only this provider
process and its sockets. Its private manifest remains as evidence; remove that
exact file after the process has stopped. No database, global host mapping,
system service or existing process is changed.

**Preferred self-contained, network-isolated run:**

```sh
python3 -B scripts/mocklab.py isolated --output /tmp/lmm-mock-isolated-01
```

This creates an unnamed user/network namespace with only loopback, brings `lo`
up, starts the provider, runs component/probe/Go checks and closes the provider.
The namespace, provider state, manifest and private Go build cache are removed
on normal completion. There is no default route, proxy or external provider.
The child cleans its owned process group on interruption; application commands
are fixed in the runner, not supplied through an arbitrary shell string.

Go adapter execution is **blocked** outside this isolated mode. Lack of namespace
support is also BLOCKED, not a fallback to public networking. A full WIP checkout
is required: an additions-only patch directory is not a substitute for source.
The isolated command currently starts **no real WIP application or database**;
a one-command full application stack is an outstanding delivery item.

For the component tests alone:

```sh
python3 -B scripts/mocklab.py selftest --output /tmp/lmm-mock-selftest-01
```

No command merges, releases, deploys production, invokes paid services or opens a
production database. Do not run Python with `-O` or `PYTHONOPTIMIZE`: assertions
are required and the entry point rejects optimized mode.

## Reports and meanings

`report.json` and `report.md` contain layer, scenario ID, seed/run identifiers,
reviewed baseline, **actual** checkout SHA when available, source/config digest,
duration, status and evidence. `actual_repository_sha: null` means the runner is
in a patch-only directory; it must not be reported as a full checkout build.
Component rows with a server record their own run IDs and counters. The parent
report run ID identifies the separately running provider process.

`network_isolated: null` means the kernel interface inspection failed, not that
isolation was established. `network_inspection_error` retains the diagnostic and
an environment `network-inspection` row is BLOCKED. Completed component results
are still saved. If isolated startup fails, its BLOCKED report and child log also
remain available. Go transport/adapter checks and the isolated child require a
positive loopback-only inspection; unknown status never permits them to run.

`component-tests.log`, bounded Go JSON logs, `provider-trace.json` and
`provider-final.json` supply evidence. Traces omit authorization headers, raw
form arguments and callback signatures. Tests use only visibly fake test values.
Do not add production traffic recordings to fixtures.

* `PASS`: this particular layer's assertions actually executed and passed.
* `FAIL`: an assertion, required test count, command, deadline or resource check failed.
* `BLOCKED`: an environmental prerequisite or actual business integration is unavailable.
* `NOT RUN`: intentionally skipped, or a reachable ready app still has no implemented business driver.

Exit codes: 0 = all selected rows passed; 1 = failure; 2 = BLOCKED/NOT RUN remains;
130 = interrupted. An all-skipped Go test command cannot be PASS. The default
report includes the unmet real-application requirements, so it currently exits 2
even when every provider component passes. Never suppress exit 2 and claim E2E
success.

## Configuration and provider boundaries

`example.json` is complete and uses an OS-assigned port (`port: 0`). JSON rejects
duplicate/unknown fields, non-finite numbers, wrong types and out-of-range limits.
`marker: LMM-MOCK-ONLY-v1` and `environment: test` are mandatory. Only
`listen.host: 127.0.0.1` is supported in this increment. Numeric IPv4 loopback
callback targets must be declared by name; DNS/proxy/redirect fallbacks do not exist.

A provider URL is `/r/{run_id}/{instance}/{protocol_path}`. Each request also needs
`X-Mock-Run: {run_id}`, or the manifest's `provider_token` through the existing
Bearer / `X-Api-Key` / `X-Goog-Api-Key` field. This lets normal local model channel
credentials explicitly select the fixture without adding a production mock flag.
Stripe and ePay additionally require their **fixed fake test merchant credentials**;
live keys are rejected. The production Stripe URL remains unchanged. Only a test
client can map that URL into this local process.

The Go helper permits exactly one original HTTPS host per client, maps it to one
run/instance, never resolves that original host and never falls back to the real
network. It adds the explicit run header. All other hosts, URL credentials,
redirects and nonlocal manifest destinations fail. Imports of this helper belong
only in `_test.go`; never import it from a production composition root.

`seed` controls namespaced deterministic IDs. Run IDs are deliberately unique;
compare normalized payloads and event order rather than expecting IDs from two
runs to be identical. There is no random timing generator in this increment.
`epoch` is a fixed provider clock origin, not the actual wall-clock time.

Per-instance options are protocol, model, fixed text, chunk size, usage/cache
counters, output kind (`text`, valid fixed `json`, or `tool_call`), currency,
maximum calls and a closed scenario. Model input/output counts are fixture
numbers, not tokenizer measurements. A new model name does not enable a new wire
protocol or capability.

Scenarios include normal/429/503, first-N failures, unavailable-until virtual
time, first-byte and model-chunk delays, empty result, missing usage, declared
context error, stream interruption, Stripe response loss after a committed
provider write, and MCP tool error/uncertain transport. `recover_at_ms` gates
availability before that time, then the configured scenario applies. `first_failures`
counts HTTP attempts, including payment account lookup; it is not a synthetic
application retry counter. There is no generic arbitrary failure-location language.
Unsupported scenario/protocol combinations fail configuration validation.

Virtual delays use the control clock; a large advance can release all already-due
chunks. There are separate real-time safety deadlines, including a slow-header
watchdog. The **application** clock is not changed. Application order expiry,
lease and breaker tests need the actual application's supported clock hook; that
integration is outstanding.

Default limits: 512 provider requests, 8 active provider requests, 64 KiB body,
4 MiB total input, 256 KiB response, 20-second real request deadline, 600-second
run lifetime and 512 provider records. The example explicitly uses 1024 records.
Configuration caps concurrency at 16 and per-request deadline at 60 seconds.
Control traffic has a separate 4096-request cap; callbacks have a queue of 32,
at most 8 copies and 3 retries. Two bounded connection slots are reserved beyond
the data concurrency cap. Reset does not reset lifetime request/input counters.

```sh
python3 -B scripts/mocklab.py control --manifest /tmp/lmm-mock-run.json \
  --data '{"op":"advance","ms":60000}'
python3 -B scripts/mocklab.py control --manifest /tmp/lmm-mock-run.json \
  --data '{"op":"stats"}'
# Refuses active requests, pending callbacks or an active callback delivery.
python3 -B scripts/mocklab.py control --manifest /tmp/lmm-mock-run.json \
  --data '{"op":"reset"}'
```

A reset clears only this provider run's state and increments its generation.
It never resets application accounts/databases. Start a fresh run after exhausting
a lifetime cap. A fresh run or safe provider reset is needed before repeating the
same adapter suite. No global `kill`, cleanup glob or production data reset is used.

## Protocol support matrix

| Adapter | Implemented external surface | Boundaries and not implemented |
|---|---|---|
| Stripe fixture v1 | Fixed one-time Checkout; merchant/event/session/PI lookup; exact-body HMAC events; stable request-key replay/conflict; pending/success/failure snapshots; partial/multiple refunds and cumulative cap; refund query; Checkout expiry | Direct-account subset only. Header `mocklab-v1-fixture` is a fixture label, **not a real Stripe API version**. No Connect, subscription, tax, FX, real hosted page or real account. Checkout expiry is a provider primitive, not a claim that the application exposes close/cancel. |
| Legacy ePay MD5/CNY | Signed GET/POST checkout form, success notification, authenticated order query, exact two-decimal CNY values | One documented legacy profile; no universal ePay-clone compatibility. Automatic/partial refunds remain unsupported because a stable signed refund contract is absent. |
| OpenAI Chat Completions | Normal JSON, SSE delta, tool argument fragments, fixed JSON content, final `finish_reason`; optional final usage-only chunk; `[DONE]` | One candidate, text/function tools. No images/audio/video, logprob generation, live model, tokenizer or provider SDK certification. |
| OpenAI Responses | Separate response/item/content/text/function-argument events with sequence numbers; completed response and usage | Ends with `response.completed`, not `[DONE]`. No stateful previous-response flow, hosted tools or complete Responses API surface. |
| Anthropic Messages | Message/content-block lifecycle; tool-use JSON deltas; separated cached/uncached input; cumulative output usage | Ends with `message_stop`. Missing-usage fixtures are robustness inputs, not a promise that omission conforms to every supplier schema. No signed thinking replay. |
| Gemini GenerateContent | Normal candidates, SSE chunks, structured function call, final finish reason and usage metadata | Uses distinct generate/streamGenerateContent methods. No fabricated OpenAI terminator, multimodal generation or safety/grounding feature emulation. |
| MCP 2025-11-25 | Initialize, initialized notification, private sessions, one-page discovery, calls, delete; deterministic add/upper/lookup/delay/fail tools | Exact repository protocol pin. No arbitrary HTTP tool API, stdio, OAuth flow, pagination, subscription, protocol-version negotiation, upstream price feed or variable-price estimator in this mock. Existing OAuth fixtures are retained. |

Amounts are integer provider minor units. The Stripe fixture declares USD/CNY/JPY/KWD
without doing currency conversion; ePay accepts only CNY decimal text. Wallet quota,
USD-millionth ledger values and credits are not computed by the mock. Frozen
application conversion quotes must be checked by the eventual real ledger driver.

Payment event snapshots are immutable. For late failure, capture a failure first,
then succeed, then deliver those event IDs in the desired order. `transition`
returns an event ID; `delivery` returns test-signed material for the real adapter;
`schedule` performs asynchronous delivery only to an allowlisted callback label.
Example control body after actual local order creation:

```json
{"op":"schedule","instance":"stripe","event":"EVENT_FROM_TRANSITION","target":"app","delay_ms":1000,"copies":2,"retries":1,"retry_delay_ms":1000}
```

The `app` label must exist in `callbacks`. A separate scheduled `drop: true` records
a lost delivery. Retry signs a fresh delivery at the current virtual provider time;
event content/identity do not change. Callback 2xx means delivery acceptance, **not
wallet credit**. Control commands cannot create a journal or mutate a wallet.

The current production `Tool` type exposes input schema only. This increment does
not add output-schema validation or claim that the market consumes it.

## Tool prices and real-market acceptance

MCP discovery is not a supplier price feed in this repository. The market's real
publisher creates immutable release prices. `tool-release-price.example.json`
contains a versioned set of test price rules, not a complete HTTP release-create request.
Attach the actual registered server identity in the real market driver. Prices are
in **millionths of USD per invocation**, not payment
minor units. Its source is explicitly test evidence, never verified external
pricing. Generate `valid_until` from the actual application test clock when the
market driver is implemented. Do not disable expiry checking to fit this file.

A future real-market driver must register the server, discover tools, publish a
MOCK ONLY release, install with the test user's own credentials, load a pinned
quote, execute and inspect both market storage and Rust receipts. Fixed per-call
pricing in the current contract charges an accepted `isError` invocation. An
ambiguous transport failure instead stays in doubt until reconciliation. The mock
never decides whether to charge or release a reservation.

## Main/WIP local comparison

`run --targets PATH` accepts explicit literal-IP connection URLs and separately
configurable virtual-host headers. The host system does not need `main.local` or
`wip.local` DNS entries. An example file is `targets.example.json`; replace its
ports and both declared SHAs with the actual disposable local processes.

**Currently this is readiness comparison only**, not comparison of equivalent
business operations. A 503 `core_migration_incomplete` blocks all associated
acceptance rows. A 200 readiness response leaves the unimplemented business
scenarios NOT RUN. There is no path that turns a healthy endpoint into a passing
ledger test. The report labels target SHAs as declared, not independently verified
by the health endpoint. Main's different entry shape needs its own real driver.

## Required follow-through, without fake completion

`scenarios.py` contains 14 real application acceptance specifications. Each has
initial state, input, mock event order, expected HTTP/business result, required
final database/ledger state and scoped cleanup. `requirements` prints them as
JSON. They are **specifications, not completed executable E2E drivers**.

Missing work is explicit: disposable application/account/database setup; real
Rust payment and tool-market authority/funds bindings; actual model relay/router
integration with these fixtures; quote and ledger read-only assertions; application
clock hooks; recovery across processes; full main/WIP intent drivers; and full
stack startup. The present patch supplies runnable external providers and narrow
production-adapter test code, not a fake replacement for those business modules.

Run the existing `relay_http`, real PostgreSQL payment tests and core funding
checks with their declared toolchains and disposable databases before accepting
any money scenario. Do not lower dependency versions, point to production, set
`verified=true`, supply a fake Funds/Core success object, or treat source submission
as a passing test.

## Add a protocol without duplicating business code

Implement the external wire adapter in its own module using `protocol.Reply` and
`ProviderError`. Add its exact protocol ID/capabilities to closed configuration,
then one dispatch entry and deterministic fixtures. Add tests against the real
production adapter, including endings, usage and unsupported inputs. Update this
matrix. Keep channel fault state separate from model content, and keep all
wallet/permission/order settlement logic in the application.
