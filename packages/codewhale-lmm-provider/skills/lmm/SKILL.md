---
name: lmm
description: Help the user sign in to LMM and launch Codewhale using the OAuth companion adapter.
---

Use `codewhale-lmm` for the LMM integration. This bundle does not register a
native provider-auth hook. Do not claim `/login LMM` works inside Codewhale.

The user signs in from their own terminal with `codewhale-lmm login`, approves
in their browser, then lists `codewhale-lmm models` and launches
`codewhale-lmm run --model <exact-catalog-id>`. Do not request credentials,
authorization codes, session files or callback URLs in the conversation.

`codewhale-lmm status` reads local status without refreshing. `balance` and
`usage` make read-only LMM requests. `logout` revokes the authorization before
removing local credentials. `logout --local-only` deletes only local storage;
it must not be presented as server revocation.

Only offer model/group IDs returned by `models`. Do not invent model prices,
capabilities, group names or context windows. The current named custom-provider
route supports Chat Completions, not Responses-only or Messages-only models.
OAuth refresh tokens and LMM access tokens stay in the companion process, not
Codewhale's model configuration. Never print or inspect session.json.

If rotation is interrupted, do not retry the old refresh token. Advise logout
and a new login. After a crashed process, `unlock` refuses to remove a live
process's lock. Do not bypass Codewhale's plugin review/trust requirements.
