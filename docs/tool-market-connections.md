# Tool market connection lifecycle

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

Account-resource reads follow the existing 100-record pages. A failed page or the bounded pagination ceiling fails the read instead of exposing a successful partial permission snapshot. This is offset pagination, not a transactional snapshot across concurrent account changes. Calls and income remain recent-record views.

Market search accepts up to 120 valid Unicode code points after trimming. SQL wildcard characters remain escaped literally, and visibility checks are unchanged. No schema migration or wallet change is required.

## Validation

```sh
cd apps/api-go
go test -p 2 ./model ./controller ./router ./service -run ToolMarket -count=1
cd ../web
bun test --preload ./scripts/test-preload.mjs --timeout 15000 ./src/features/tool-market/connection-utils.test.ts ./src/features/tool-market/connection-i18n.test.ts
bun run typecheck
bun run format:check
```

Model regressions cover account/client isolation, repeated disconnects, transaction rollback, in-flight settlement, reserved client IDs and disabled accounts. Search regressions cover Chinese length limits, malformed UTF-8, literal wildcard matching and private visibility. Frontend helper regressions cover permissions, client validation, configuration safety, expiry, multi-page reads and complete seven-language copy.

Before deployment, verify the page at desktop and mobile widths, both themes, token creation/copy/revocation, client disconnect confirmation, editing budgets, a failed later resource page, and logout/login while a token is displayed. Unit tests alone are not visual or end-to-end acceptance.
