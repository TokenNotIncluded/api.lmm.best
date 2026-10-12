---
name: i18n-translate
description: "Add or correct UI copy and locale keys in apps/web, including one-key fixes. Preserve all locales and interpolation. Not for README or other non-UI prose."
---

# Frontend translations

Read the changed call site and existing English copy. Locale files are
`apps/web/src/i18n/locales/*.json`, under `translation`; the helper discovers the
actual languages. Keep brand names, identifiers and `{{variables}}` intact.
Use [the glossary](../../../docs/translation-glossary.md) when terminology needs
clarification. Never copy task instructions into translation values.

Create a temporary JSON patch outside the repository, with every supported
locale and the same changed keys in each. Values must be real, non-empty
translations. Use [the existing helper](scripts/apply-translations.mjs):

```bash
mise exec -- node .agents/skills/i18n-translate/scripts/apply-translations.mjs /tmp/lmm-translations.json
mise exec -- bun run --filter @lmm/web i18n:sync
```

Use the actual patch path. Apply validates locales and placeholders before
writing; a separate `--check` is for a requested preview, not required before
applying the same patch. Do not copy a writer or manually edit locale JSON.
Writes are sequential, not atomic across files; inspect a partial failure first.

Review every changed locale and generated `_reports/_sync-report.json` and
`_extras` output. Preserve unrelated changes. Sync is a write operation, not a
translation service: it chooses the locale with the most leaf keys as its base,
fills missing values and moves extras. Zero missing keys does not prove correct
translation. Read [the implementation](../../../apps/web/scripts/sync-i18n.mjs)
only when needed; do not hand-edit reports to conceal errors.

For value-only changes, validate the patch and final locale diff. Call-site or
type changes also need affected tests and `mise exec -- bun run typecheck:web`.
Check affected long-text/mobile layouts when text changes layout. No full Web
build is needed solely for JSON text. Report missing language/browser checks.
Only helper-code changes require its `node --test` self-test suite.

Done means complete changed keys across actual locales, preserved variables,
reviewed sync output and reported checks. Delete only your own temporary patch.
