# Required Rust action authority

This is a proposed integration contract for the optional Web compatibility
package, not a shipped protobuf definition or permission grant. The current
read-only `Authorize`, `ListTeams` and funding-candidate RPCs do not satisfy it.
Core/protobuf/client changes require their assigned owners and independent tests.

The Go module's existing port is:

```go
Authorize(ctx context.Context, credential, account, action string) (Principal, error)
```

`credential` is the live user credential, separate from the service credential.
The service credential authenticates the extension process and must be scoped
to the allowed module. It never supplies the actor. The requested account is an
external personal user ID or team ID, not an internal ledger account or wallet.
The proposed RPC should use closed module/action enums and the existing typed
account reference. Return only the validated actor user ID and requested account
reference; do not return an implied spend grant or replace account references
with funding candidates.

| Go action | Required decision |
| --- | --- |
| `toolmarket.read` | Live interactive session, active identity/account, current developer entitlement, and current access to the target account. |
| `toolmarket.manage` | The read conditions plus explicit Core permission to manage that account's tool-market business records. |
| `toolmarket.authorize` | Explicit Core permission to manage installations/favorites for that account. This is not a reservation, payment or execution grant. |
| `toolmarket.review` | A separate platform review decision matching enabled administrator/root authority in main; account ownership alone is insufficient. |
| `toolmarket.config` | A separate root platform configuration decision; account ownership alone is insufficient. |

Rust must reject API keys for this first interactive Web contract, unknown
modules/actions, inactive/expired/revoked sessions, disabled identities/accounts,
missing developer entitlement and unauthorized targets. Team read/manage/action
semantics must be defined explicitly in Core; merely listing a membership or
observing `can_spend` cannot approve tool-market writes. The legacy Web package
currently rejects team ownership because its DTO exposes only personal
`owner_id`; the adapter must preserve this restriction.

Each request requires a new Core decision. The adapter must reject malformed,
wrong-actor or wrong-target responses, map authentication/permission errors
without leaking provider/database text, and fail closed on Core outages. The
module still checks exact business ownership, visibility and immutable version
after the Core decision; remote validation reauthorizes before committing.

Required cross-process acceptance includes live session success, API-key denial,
session revocation, user/account disablement, entitlement removal, wrong personal
target, team removal/demotion, platform review/config boundaries, timeout and
malformed responses. Synthetic `Authority` unit tests are not this evidence.

There is no first-package execution/funding action. Disabled Web invoke, grant
and budget requests check only read/session entitlement, then return HTTP 503.
The native module's `toolmarket.execute` must stay unavailable in a real adapter
until Core supplies the independent funds contract: server-side price and payer,
explicit permission and budgets, idempotent reservation/commit/recovery and exact
original-operation references. Neither successful action authorization nor a
zero submitted price may substitute for that contract.
