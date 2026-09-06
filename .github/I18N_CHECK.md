# Translation regression check

Run `node scripts/check-i18n.mjs` from the repository root to compare local
edits with `HEAD`. Before opening a PR, run:

```sh
node scripts/check-i18n.mjs --base origin/main --merge-base
node --test scripts/check-i18n.test.mjs
```

New keys in `apps/web/src/i18n/locales/en.json` require a nonempty translation
under `translation` in all seven supported locales: en, zh, zh-TW, fr, ja, ru,
and vi. Removing or emptying an existing translation also fails while the key
remains in English. Existing gaps are tolerated so unrelated changes and
translation repairs can proceed. Translate new text explicitly; this check
does not generate translations or judge linguistic quality.

The read-only workflow compares PRs with their merge base and pushes to `main`
with the previous commit. Failures report the affected locale and key without
closing PRs or modifying files.
