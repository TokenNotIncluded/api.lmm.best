# Tool market connection lifecycle

Browser OAuth is the recommended connection when the native OAuth server is enabled. See [provider presets and MCP OAuth](tool-market-provider-presets.md) for the Codex command, resource boundary and current limitations. The personal-token lifecycle below remains the fallback.

The marketplace connection page groups personal tokens, loaded tools and explicit tool grants by client ID. Creating a new connection defaults to tool invocation without permission to change the client's loaded tool set. Users can independently enable management or choose discovery-only access and select a 1, 7, 30 or 90 day lifetime. A token is not a paid-tool authorization.

The JSON preview uses a placeholder. Only an explicit copy action includes the issued secret in the Authorization header. The issued secret stays in component state, is hidden after five minutes or observed revocation/expiry, and is discarded on account changes. It is not returned as mutation data. Clipboard contents copied explicitly by the user are not automatically erased.

## Disconnect endpoint

`POST /api/tool-market/clients/disconnect`

Authenticated, rate-limited JSON input:

```json
{"client_id":"my-agent"}
```

Success returns the ordinary API envelope with data containing `client_id`, `tokens_revoked`, `grants_revoked` and `tools_unloaded`. The account comes only from authentication, never from the request body.

The Go default backend locks the authenticated account using the same lock order as token creation, grants, installation and call reservation. In one transaction it revokes all unrevoked personal market tokens and grants for the client and removes its installations. A failure rolls back all changes. Repeating a completed disconnect returns zero changes without an additional audit event. Other accounts and client IDs are not changed.

Disconnect does not refund reservations or change execution/settlement records, transfers, accumulated spending or budgets. A call that was already accepted can still settle through the trusted execution path. It is not a cancellation endpoint. Users can intentionally reconnect later by issuing a new token and reloading/re-authorizing tools.

`web-market` and `oauth:` clients are excluded from this endpoint. OAuth credential revocation remains with the OAuth consent lifecycle. This change does not claim Rust implementation parity or move marketplace route ownership.

## Listing and search

The default client view excludes revoked tokens and tool grants before grouping
and counting clients. Revoked records remain available in a collapsed history
section until their owner explicitly removes them. Loaded tool installations
remain in the current client view until unloaded or removed with the client.

`DELETE /api/tool-market/grants/:id/record` and
`DELETE /api/tool-market/tokens/:id/record` remove a previously revoked record
from account-resource lists. They require ownership and explicit revocation;
an unrevoked record returns a conflict, and another account's record is not
found. The existing `DELETE /grants/:id` and `DELETE /tokens/:id` endpoints
continue to revoke without hiding the stored record.

`POST /api/tool-market/clients/remove` accepts the same authenticated
`{"client_id":"my-agent"}` shape as disconnect. All of that account's tokens
and grants for the exact personal client ID must already be revoked. In one
account-locked transaction it hides those records and removes installations.
It returns `client_id`, `tokens_hidden`, `grants_hidden`, and `tools_unloaded`.
An unrevoked token or grant prevents the entire operation. Reserved `web-market`
and `oauth:` client IDs are rejected. Repeating removal returns zero changes.

Removal records account-owned `grant.hide`, `token.hide`, and `client.remove`
events in the existing event table. It preserves authorization rows, token
digests, revocation timestamps, audit events, calls, reservations, transfers,
usage and cumulative budgets. This change requires no DDL. An N−1 binary ignores
the visibility events and can display those revoked rows again; it still
rejects their revoked credentials. Reconnecting creates a new token or grant
through the existing setup and choose-tools flow; no old row is restored.

Account-resource reads follow the existing 100-record pages. A failed page or the bounded pagination ceiling fails the read instead of exposing a successful partial permission snapshot. This is offset pagination, not a transactional snapshot across concurrent account changes. Calls and income remain recent-record views.

Market search accepts up to 120 valid Unicode code points after trimming. SQL wildcard characters remain escaped literally, and visibility checks are unchanged. No schema migration or wallet change is required.

## Validation

```sh
cd apps/lmm-extensions
go test -p 2 ./model ./controller ./router ./service -run ToolMarket -count=1
cd ../web
bun test --preload ./scripts/test-preload.mjs --timeout 15000 ./src/features/tool-market/connection-utils.test.ts ./src/features/tool-market/connection-i18n.test.ts
bun run typecheck
bun run format:check
```

Model regressions cover account/client isolation, repeated disconnects, transaction rollback, in-flight settlement, reserved client IDs and disabled accounts. Search regressions cover Chinese length limits, malformed UTF-8, literal wildcard matching and private visibility. Frontend helper regressions cover permissions, client validation, configuration safety, expiry, multi-page reads and complete seven-language copy.

Before deployment, verify the page at desktop and mobile widths, both themes, token creation/copy/revocation, client disconnect confirmation, editing budgets, a failed later resource page, and logout/login while a token is displayed. Unit tests alone are not visual or end-to-end acceptance.


## AI tool management

The built-in `metamcp` tool is discoverable on every marketplace connection and its search, details, status, usage and call-history operations are free. Loading requires the connection's existing manage permission; paid authorization additionally requires explicit owner delegation. The connection form preserves the existing permissions and never silently enables manage.

A newly issued personal token can opt into AI tool management with an integer Credit budget, initially 0. Saved 0 permits no paid spending; a missing budget is not the same as a saved zero budget. Account and tool budgets still apply. AI can only tighten existing client/tool limits, cannot restore a tightened limit, change another connection, reset spent/reserved counters or change the account budget. Owner settings display any stricter effective cap.

The owner API is `GET/PUT /api/tool-market/meta-delegations/personal/:tokenID` or `/oauth/:clientID`. `GET /api/tool-market/meta-delegations/oauth-clients` lists only OAuth clients whose current family and valid token both retain discover, invoke and manage permissions. It does not widen prior consent or replace the invoke-only authorization picker.

If connection creation succeeds but delegation setup fails, the once-only token remains available and the page reports the delegation failure. Retry settings on that token rather than issuing a replacement. Existing active personal tokens are configured separately; another token for the same client does not inherit delegation. The page clears private drafts and late responses on account changes and refreshes displayed budgets after delegation writes.
