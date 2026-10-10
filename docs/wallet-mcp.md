# Free wallet MCP tools

The tool market offers five built-in wallet tools at **0 quota per MCP call**:

| Tool | Result | Permission |
| --- | --- | --- |
| `wallet.balance` | Your available raw credits and true USD balance; deprecated legacy batch alias | Exact read tool grant |
| `wallet.topup_link` | Official wallet navigation link and locally generated PNG QR | Exact read tool grant |
| `wallet.transfers.list` | Your own transfer statuses and private pending links | Exact read tool grant |
| `wallet.transfer.create` | Hold the confirmed amount and create a recipient link and QR | Exact write tool grant and user confirmation |
| `wallet.transfer.cancel` | Cancel your unclaimed transfer and refund its hold | Exact write tool grant and user confirmation |

Loading a tool does not grant execution permission. A general market OAuth scope
or an old market token does not silently authorize wallet writes. The market
checks the exact installed tool, version and active grant before dispatch and
again when resuming a confirmation. Revoking a grant prevents future dispatch;
an operation that has already been accepted may finish.

Top-up links only navigate to `/wallet?topup_amount=25` on the configured HTTPS
console origin. The amount is a whole legacy batch amount between 1 and
1,000,000. The wallet validates it and only prefills the form. The user still
chooses a payment method and reviews its current limits, quote and confirmation.
Generating the link does not create a payment-provider order, charge money or
credit the wallet. Duplicate or malformed values are ignored.

One current `CREDIT` is one raw quota unit: `wallet.balance` returns
`available_credits`, `credit_unit:1`, and `available_usd = available_credits / credits_per_usd`
using the durable USD anchor K. The deprecated `quota_per_platform_credit` field
retains immutable legacy batch Q for older clients; it is marked `LEGACY` and is
not a raw-credit or USD unit. This read-only alias does not establish payment
availability; the existing legacy payment bridge checks its own calibration.
A top-up amount of 25 is 25 legacy batches, not
25 raw credits or 25 USD. Transfers accept integer raw credits directly.
The confirmation displays the exact
quota amount and account, and explains that the balance is held. The MCP tool
fee remains zero; the amount being transferred is real wallet balance. Image
model calls through the free drawing MCP are separately billed by model usage.

The persisted wallet tool-market catalog retains its historical description
metadata so existing exact-version installations and grants remain valid.
Public market and MCP descriptions explain the current `LEGACY` and `CREDIT`
names. This presentation clarification changes no input type, permission,
tool price or transfer amount; no currency is treated as equal to another.
The public descriptions are an equivalent presentation view; `version.digest`
identifies the stored canonical snapshot, not a hash of that presentation view.

Pending transfer links are bearer credentials. Their secret appears only in the
fragment of `/transfer#<token>` and is not sent to a third-party QR service.
Anyone with the link or QR can claim its held balance, so share it only with the
intended recipient. The sender can cancel only an unclaimed transfer. Recipient
contact details are excluded from MCP transfer history.

A confirmation is bound to the authenticated account and auth version, market
client, exact tool/version/grant, market request and amount. The marketplace
call expires after 2 minutes. Changing those inputs invalidates the confirmation. Its consumption,
wallet debit/refund and operation receipt commit together, so failed operations
roll back and simultaneous retries do not repeat the hold or refund. Resume only
the original market request; do not create a new request to retry an uncertain
operation.
