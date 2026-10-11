---
name: i18n-translate
description: "Add or correct UI copy and locale keys in apps/web, including one-key fixes. Preserve all locales and interpolation. Not for README or other non-UI prose."
---

# Frontend translations

Read the changed call site and existing English copy. Locale files are
`apps/web/src/i18n/locales/*.json`, under `translation`; the helper discovers the
actual language set. Keep brand names, identifiers and `{{variables}}` intact.
Use [the glossary](../../../docs/translation-glossary.md) only when terminology
needs clarification. Never copy task instructions into translation values.

## Apply once

Create a temporary JSON patch outside the repository, containing every supported
locale and the same changed keys in each. Values must be real, non-empty
translations. Use [the existing helper](scripts/apply-translations.mjs):

```bash
node .agents/skills/i18n-translate/scripts/apply-translations.mjs /tmp/lmm-translations.json
bun run --filter @lmm/web i18n:sync
```

Use the patch path you actually created. Apply validates all locales and
placeholders before writing. A separate `--check` invocation is useful for a
requested preview, not required immediately before applying the same patch.
Do not copy a new writer or manually edit locale JSON. Writes are sequential,
not atomic across files; inspect a partial failure before retrying.

Review every changed locale and the generated `_reports/_sync-report.json` and
any `_extras` output. Preserve unrelated work. Sync is a write operation, not a
translation service: it chooses the locale with the most leaf keys as its base,
fills missing values and moves extras. Zero missing keys does not prove correct
translation. See [the sync implementation](../../../apps/web/scripts/sync-i18n.mjs)
when behavior is unclear; do not hand-edit reports to conceal errors.

## Verify the affected result

For a value-only correction, validate the patch and final locale diff. Do not
run all Web tests, typecheck or builds solely because JSON text changed. When
call sites or types change, add their tests and the Web typecheck. For text that
changes layout, inspect the affected control in long-text languages and mobile
when a browser is available. Report missing visual/language checks honestly.

Only helper-code changes require:

```bash
node --test .agents/skills/i18n-translate/scripts/apply-translations.test.mjs
```

Done means complete changed keys across the actual locales, preserved variables,
reviewed sync output, and reported checks. Delete only your own temporary patch.
