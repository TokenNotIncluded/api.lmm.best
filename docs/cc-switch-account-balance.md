# CC Switch account balance

The keys page can import a read-only wallet balance query into CC Switch.
In the import dialog, the owner must explicitly enable **Allow this key to read
account balance**. This immediately grants every holder of that key access to
the account's persisted wallet balance. Reopen the dialog and turn it off to
revoke access, even without importing again. Cancelling the import does not undo
an explicit permission change. Do not enable it on a shared key unless that
disclosure is intended.

The import carries `usageScript` (base64), `usageEnabled=true`, `usageBaseUrl`,
and `usageAutoInterval=5`, using CC Switch's provider deep-link protocol. The
script uses the provider credential through `{{apiKey}}`; it does not embed a
second credential or a browser session. CC Switch still asks the user to confirm
the imported configuration. Older clients without usage-script import support
need an upgrade or manual query configuration.

## API

`PUT /api/token/:id/account-balance-access` accepts `{"enabled":true}` or
`{"enabled":false}` using the existing owner-authenticated key-management API.
The ID must belong to the authenticated owner. OAuth-managed keys are excluded.
An ordinary relay API key cannot grant itself access.

`GET /v1/balance` accepts `Authorization: Bearer API_KEY`. It uses the same
persisted key authentication as `/v1/usage`: disabled, expired, deleted,
OAuth-managed and IP-restricted keys are rejected, as are disabled accounts.
Exhausted keys can still check balance. An ungranted key receives HTTP 403 with
`account_balance_access_required`, not a zero balance.

Example successful response (fictional data):

```json
{
  "valid": true,
  "scope": "account",
  "currency": "USD",
  "remaining": 23.61,
  "updated_at": 1789459200,
  "consistency": "persisted_snapshot"
}
```

`remaining` is the owner's persisted wallet quota divided by `QuotaPerUnit`,
always USD regardless of the site's display currency or `DisplayTokenStatEnabled`.
Zero and negative balances are preserved. Subscription allowances, token limits,
and cumulative spend are not included. Pending usage writes can delay the
snapshot; this is not a spending authorization or an atomic billing guarantee.
Responses use `Cache-Control: no-store`. Queries share the existing per-account
30/minute quota-query rate limit; imported clients refresh every five minutes.

`/v1/usage` remains token-scoped. An unlimited token still returns null remaining
quota there; it does not imply an unlimited account balance.

## Migration and release note

Go's normal model migration adds `tokens.account_balance_read` as a non-null
boolean defaulting to false. Existing keys gain no access automatically. Apply
the backend migration and release before the new frontend. Older Go releases
ignore the additive column. The Rust preview does not implement these new
routes in this change; this feature requires the Go provider. No production
deployment or provider ownership change is included.

Release note: keys-page CC Switch imports can include account wallet balance
after an explicit, revocable owner grant, instead of showing only token usage.
