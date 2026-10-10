# Assistant display preferences

The built-in assistant can prepare a browser action to change mode, theme,
interface language and balance display currency. Both the console conversation
and the L0 conversation render the same receipt and Undo control.

Examples:

- “切到暗色。”
- “切换到英文，金额用美元显示。”
- “换成海洋主题，别改其他设置。”
- “换个颜色玩一下，不要保存。”
- “恢复刚才的设置。”

## Tools

`get_ui_preference_options` lists supported values. It does not claim to read
the current browser state.

`set_ui_preferences` accepts any nonempty subset of `mode`, `theme`, `language`
and `currency`. Omitted fields stay unchanged. `mode` is `light`, `dark` or
`system`. Currency values are `USD`, `CNY`, `CREDIT` and `auto`; `auto` is stored
as the existing empty-string account preference. Currency is display only.

`temporary: true` permits only a mode/theme preview. It lasts eight seconds,
writes no preference cookies or account settings, and stops when the card is
closed. Rapid repeated previews are rejected. Language and currency must not
be used as a prank.

`restore_ui_preferences` stops the current preview or undoes the latest
assistant change in this page session. Undo changes only fields that still
match the assistant's applied values. An older card cannot undo a newer change.
Reloading the page discards the in-memory Undo record; it is not a factory reset.

## Storage and boundaries

Mode and theme reuse the existing browser preference providers. Language and
currency reuse `PUT /api/user/self`; related changes use one request and merge
only their fields into the latest account settings. No exchange rate, balance,
settlement currency, permission or other account is modified.

The tools use the normal live browser session, access-level and administrator
policy checks. The `ui_preferences` group and individual tools can be disabled
or restricted in the existing tool settings. Actions bind to the requesting
user and login session, expire after two minutes, and run at most once per page.
Expired actions and old-session results cannot change a new account's display.

The model receives `browser_action_prepared`, not a success claim. Only the
browser receipt reports whether the actual operation completed. Failed or
uncertain profile writes are never automatically retried.

## Validation

Run the existing project checks:

```sh
cd apps/lmm-extensions
go test ./controller ./setting -run AssistantUIPreference
cd ../web
bun run typecheck
bun run test
bun run format:check
```

The focused tests cover allowed values, invalid fields, session binding,
replays, failed writes, preview expiry, conditional Undo, real theme provider
cookies, and explicit Anthropic preset choices surviving the legacy migration.
