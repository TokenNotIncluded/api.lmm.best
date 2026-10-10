---
name: i18n-translate
description: >-
  Add or correct frontend UI text and translations in apps/web. Use for any
  t() key, locale JSON, label, button, dialog, toast, placeholder, validation
  message, missing translation, or even a one-key fix. Requires complete locale
  patches, preserved interpolation variables, and a reviewed sync result.
---

# Frontend translations

Read this skill before changing UI text or locale files. Use the call site and
existing English copy to establish meaning. Do not copy conversation text, review
comments, or task instructions into locale values.

## Current source and scope

- Locale files: `apps/web/src/i18n/locales/*.json`, under `translation`.
- Current languages: `en`, `zh`, `zh-TW`, `fr`, `ja`, `ru`, and `vi`.
- [Sync implementation](../../../apps/web/scripts/sync-i18n.mjs) and
  [package commands](../../../apps/web/package.json) are the source of truth.
- Use the committed [write helper](scripts/apply-translations.mjs), not manual
  JSON edits or a newly copied temporary writer. It discovers the actual locale
  files and rejects incomplete locale/key sets before writing.

## Apply a focused change

1. Read the component and existing keys. Establish the action, available space,
   variables, and intended English text. Preserve product names, identifiers,
   URLs, and variable expressions. Do not rename keys without updating callers.
2. Prepare a temporary JSON patch outside the repository. It must contain one
   object per supported locale, with the same changed keys in every object.
   Each value must be a non-empty translation, not an instruction to translate.
   Do not place the patch inside the locale directory.
3. Validate without writing, then apply from the repository root:

```bash
node .agents/skills/i18n-translate/scripts/apply-translations.mjs /tmp/lmm-translations.json --check
node .agents/skills/i18n-translate/scripts/apply-translations.mjs /tmp/lmm-translations.json
bun run --filter @lmm/web i18n:sync
```

Use the temporary file you actually created; the path above is an example.
`--check` reports how many files would change and does not write. The helper
validates all locales and interpolation variables first, preserves unrelated
keys, and leaves unchanged files alone. Disk writes are sequential, **not an
all-or-nothing transaction**. After a write failure, inspect the working tree
before retrying. Do not discard another contributor's locale changes.

4. Inspect the diff for every locale. Read
   `apps/web/src/i18n/locales/_reports/_sync-report.json` and any `_extras` output.
   Check the final key sets and values, not only the report counts.
5. Run affected UI tests and `bun run typecheck` from `apps/web`. Check the affected
   layout in long-text languages and on mobile when a browser is available.
   Report a missing browser check rather than claiming visual acceptance.

## What sync does, and does not do

`i18n:sync` is a write operation, not a read-only audit or a translation service.
It selects the locale with the most leaf keys as its base; English is not fixed
as the base. It follows that base's order, fills missing values using comparison
content, and moves extra keys into `_extras`. A successful exit or zero missing
keys does not prove that every language is correctly translated.

Add the same keys to every locale before sync. Inspect unexpected removals or
large formatting changes. Keep the sync script's existing serialization and
attribution rules; do not replace it or hand-edit generated results to hide errors.
Its untranslated detection is a heuristic and can miss untranslated text.

## Copy and verification

Preserve `{{variables}}`, including format expressions. Use compact, natural text
for controls without removing meaning. English-looking brand names can be valid;
ordinary labels, actions, errors, and units still need translation. Check existing
[translation terminology](../../../docs/translation-glossary.md) where relevant.

For helper changes, run from the repository root:

```bash
node --test .agents/skills/i18n-translate/scripts/apply-translations.test.mjs
```

Delete only temporary files created for the task. Do not delete reports or other
changes blindly. Record tested commands and unverified language/layout checks.
