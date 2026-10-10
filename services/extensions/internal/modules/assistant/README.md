# Assistant extension (task 09)

This is an independent backend module for the fresh-install microkernel. The canonical directory is `apps/lmm-extensions`, renamed from `apps/api-go` on the base branch. No old database data or runtime is imported.

## Status and assembly boundary

`New(repository, authority, promotions, Options)` constructs a service implementing the existing `modules.Module` interface. Register it explicitly with `modules.New`. Construction does not open databases, start workers, call models, or enable any module globally. `internal/app/run.go` and all Protobuf files are unchanged. Frontend routes and production host composition remain final integration work.

`access.New(coreClient)` uses the existing bounded Rust identity client. Every request and every tool invocation, including retries, requires a current Rust-verified **session**. API keys are denied: the present identity contract does not carry assistant management scopes. L0 through L6 come from Rust; L5 is administrator and L6 super administrator. No request can supply its own identity or level. Neither platform administration nor a host service token bypasses another account's ownership.

Personal scope always means the authenticated user. A `team_id` selects a team only after `ListTeams` confirms membership. A team member can read team display preferences. Only a current team owner or administrator can change them. Sessions, logs and explicit feedback remain private to the author within that account context, even among team administrators. Team display preferences are shared. Currency changes affect display only, never prices, balances, exchange rates or ledger units.

The optional `Model` adapter **must** call Rust's authenticated model request path, honor cancellation and bound its wire response before parsing. This task provides a tested runner, not an unmetered upstream relay. There is no production model adapter in this module. With no adapter, `/chat` returns dependency-unavailable; account tools remain usable. A Go test fixture or Unix RPC fixture is not a live Rust model or funds integration.

## Tools and bounded conversations

The first model request contains exact input definitions for only `tools.search`, `tools.describe` and `conversation.end`. Search returns at most 20 permitted summaries and a cursor, without all schemas. Describe loads one permitted definition. At most eight additional tool definitions are retained for a turn. The registry is an immutable snapshot of trusted code registrations, not a user or model plugin loader. Required fields, enums, bounds and rejection of additional fields are enforced by the same definitions used for discovery.

Built-in tools include account identity; display preferences; task, drawing and operational status records; explicit feedback; and optional coupon/referral eligibility, application, limits and claim recovery. Operational events additionally require L5 or L6. Reward tools are personal-only, with no balance, actor, role, custom amount or policy-edit parameters.

The runner defaults to 12 tool calls per session (maximum 32), a 30-minute session (maximum one hour), four calls per model response, 8 KiB input/output limits and 32 KiB cumulative results. Model request JSON is capped at 48 KiB. Each model call has a 15-second deadline, each tool a five-second deadline. Dependency implementations must honor these contexts; compiled modules are trusted code, not a security sandbox. The existing host supplies per-module admission limits and process isolation remains a deployment concern.

A terminal tool stops the current batch immediately and causes no further model call. Empty responses end the conversation. Repeated identical actions, call limits, expiry, errors and uncertain rewards stop the runner. A durable turn lease rejects simultaneous chats/direct tool injections. Calls are staged before execution; replaying a call ID with changed arguments fails. Replaying an identical call returns only its status and **does not repeat the effect or retain its original output**. After a process dies during an arbitrary tool, a pending call is not blindly rerun. Start a new session and use the relevant business recovery operation; rewards retain their independent durable key.

## Storage and privacy

Install `schema.sql` explicitly in a **new dedicated assistant database**, using a deployment role, then inject a pool restricted to `lmm_assistant`. `NewPostgres` verifies the expected database name, absence of any `core_*` schema and contract version. It does not open a pool or install/upgrade tables. Do not grant the runtime role access to core or another extension database.

`lmm_assistant.records` contains four constrained record families: session state, call audit, display preferences, and entries. Primary keys include account/user scope. Transactions use a scope-specific database lock across processes. Call audit stores tool name, canonical request hash, state and time, not the credential, input text, response text, raw arguments, results, drawing prompt or image. Explicitly submitted feedback text and chosen display preferences are intentional business records. Treat status references as opaque IDs; never use them to store chat text or secrets.

No conversation transcript is stored. Account revocation does not erase already-required audit records. A deployment must set a retention policy for expired session/call metadata and feedback before production. Do not delete promotion claim uniqueness records as part of conversation cleanup.

## HTTP surface

These routes are relative to `/extensions/v1/assistant`. The host's private service credential and `X-LMM-User-Credential` are separate; browsers must not receive the host credential.

| Method | Route | Input |
| --- | --- | --- |
| POST | `/sessions` | `{ "team_id": 0 }` (zero/omitted means personal) |
| GET | `/tools` | `q`, `after`, `limit` (default 8), optional `team_id` |
| GET | `/tools/{name}` | optional `team_id` |
| POST | `/invoke` | `session_id`, `call_id`, `name`, `arguments`, optional `team_id` |
| POST | `/chat` | `session_id`, `input`, optional `team_id` |
| GET | `/entries` | `after`, `limit` (default 20), optional `team_id` |

Bodies are limited to 16 KiB. Unknown/duplicate fields, duplicate query parameters used by the route, multiple JSON documents, invalid UTF-8, excessive nesting and invalid values fail. Service exceptions are mapped to fixed error labels, never arbitrary dependency error text.

## Verification

From `apps/lmm-extensions`:

```sh
go vet -mod=readonly ./...
go test -mod=readonly -race -count=1 ./...
CGO_ENABLED=0 go build -mod=readonly -trimpath ./cmd/extensions
```

The separate `pgtest` module keeps a PostgreSQL driver out of production dependencies. Its tests require a disposable **local** PostgreSQL instance and fail rather than skip when configuration is absent:

```sh
cd internal/modules/assistant/pgtest
LMM_MK09_TEST_DSN='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable' \
  go test -mod=readonly -race -count=1 ./...
```

Tests create uniquely named databases, use two independent pools, exercise rollback, account boundaries, least-privilege role behavior, duplicate applications, campaign caps and recovery after a core-result loss, then delete the test databases. Core money/model behavior in these tests is a controlled fixture, **not** live Rust ledger posting. The task workflow records the tested commit and full test output without deploying anything.
