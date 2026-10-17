# A10 client checks

These checks exercise the real HumanGrant client code. The HTTP provider and
Cookie/Origin adapter are synthetic; they do not prove browser cookie behavior,
server authorization, PostgreSQL persistence, or full A10 acceptance.

Run from the repository root with its installed, pinned tools:

```sh
MISE_DISABLE_TOOLS=rust mise exec -- bun test \
  ./apps/web/src/features/auth/lib/humangrant.test.ts \
  ./apps/web/src/features/auth/lib/humangrant-session.test.ts \
  ./apps/web/src/lib/server-error-message-a10.test.ts

LMM_A10_CLIENT_MODULE="$PWD/apps/web/src/features/auth/lib/humangrant.ts" \
  MISE_DISABLE_TOOLS=rust mise exec -- bun test \
  ./tests/e2e/a10/client-http.mjs
```

For Node, compile the dependency-free `humangrant.ts` module to an ES module in a
private temporary directory, then use its absolute path in
`LMM_A10_CLIENT_MODULE` with `node --test tests/e2e/a10/client-http.mjs`.
Do not point the test at production: it creates only random loopback listeners,
accepts no backend URL, and refuses the protected local ports. The output records
the client module hash, not request bodies, credentials or CSRF values.

## Translation integration

`translations.json` contains complete seven-language updates for the affected
keys. It is input to the existing translation helper, not a runtime locale
resource. Merely committing it does **not** apply the translations.

After checking locale ownership in the actual workspace, use the existing helper:

```sh
MISE_DISABLE_TOOLS=rust mise exec -- node \
  .agents/skills/i18n-translate/scripts/apply-translations.mjs \
  tests/e2e/a10/translations.json --check
MISE_DISABLE_TOOLS=rust mise exec -- node \
  .agents/skills/i18n-translate/scripts/apply-translations.mjs \
  tests/e2e/a10/translations.json
MISE_DISABLE_TOOLS=rust mise exec -- bun run --filter @lmm/web i18n:sync
```

Inspect all seven locale diffs and the sync report. Do not replace the whole
locale files with main's copies: the WIP versions contain separate changes.

## Checks still needed

Run the complete Web typecheck, affected lint/format checks and the real React
settings page, including batched account switches, Strict Mode, keyboard and
narrow-screen use. These commands still require the project's pinned tools.

A02/S11 must provide and verify the actual human-cookie/CSRF routes, login,
consent, claim, profile, owner revocation and off/on behavior. The other A10
business journeys and independent CLI checks remain separate requirements.
This directory does not mark any of those journeys complete.

## Optional Chromium transport checks

`client-browser.mjs` loads the **compiled** HumanGrant client in a fresh
Chromium context and serves a local synthetic HTTP fixture. It does not
authenticate against LMM or prove the Rust/Go backend, Cookie policy, React
settings screen, database, or payment behavior.

Run only in an isolated environment that permits fresh loopback listeners,
with approved installed Playwright/Chromium. Do not weaken browser security:

```sh
LMM_A10_CLIENT_MODULE=/absolute/private/a10/humangrant.js \
PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs \
node --test tests/e2e/a10/client-browser.mjs
```

The Chromium runner was syntax-checked but its 11 browser cases did not reach
business assertions in the previous restricted environment. Until a real
browser run passes, mark it **PENDING**, not browser acceptance.
