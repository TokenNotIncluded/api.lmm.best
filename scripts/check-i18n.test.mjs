/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { checkRepository, findRegressions, locales, parseLocale } from './check-i18n.mjs'

const translations = (entries = { old: 'Existing translation' }) =>
  Object.fromEntries(locales.map((locale) => [locale, { ...entries }]))

test('legacy gaps pass, including translation-only repairs', () => {
  const before = translations()
  delete before.fr.old
  before.vi.old = ''
  assert.deepEqual(findRegressions(before, structuredClone(before)), [])
  assert.deepEqual(findRegressions(before, translations()), [])
})

test('every new English key needs a nonempty string in all supported locales', () => {
  for (const locale of locales) {
    for (const invalid of [undefined, '', ' \n\t', null, {}, 12]) {
      const after = translations({ new: 'Translated' })
      if (invalid === undefined && locale !== 'en') delete after[locale].new
      else after[locale].new = invalid
      assert.deepEqual(findRegressions(translations(), after), [
        `${locale}.json: new key needs a translation: "new"`,
      ])
    }
  }
  assert.deepEqual(findRegressions(translations(), translations({ new: 'Translated' })), [])
})

test('deleting or blanking existing translations fails even without English changes', () => {
  const after = translations()
  delete after.fr.old
  after.ru.old = ' '
  assert.deepEqual(findRegressions(translations(), after), [
    'fr.json: existing translation removed or emptied: "old"',
    'ru.json: existing translation removed or emptied: "old"',
  ])
})

test('unused keys can be removed consistently with English', () => {
  assert.deepEqual(findRegressions(translations(), translations({})), [])
})

test('dotted and prototype-like keys are treated as literal own keys', () => {
  const before = translations({})
  const after = translations(JSON.parse('{"ui.action": "Action", "__proto__": "Prototype"}'))
  delete after.ja['ui.action']
  delete after.vi.__proto__
  assert.deepEqual(findRegressions(before, after), [
    'ja.json: new key needs a translation: "ui.action"',
    'vi.json: new key needs a translation: "__proto__"',
  ])
})

test('malformed locale files fail with a useful error', () => {
  assert.throws(() => parseLocale('{', 'fr.json'), SyntaxError)
  for (const document of [null, [], {}, { translation: [] }]) {
    assert.throws(() => parseLocale(JSON.stringify(document), 'fr.json'), /fr.json: expected a translation object/)
  }
})

test('git baseline checks the working tree and uses the PR merge base', (t) => {
  const cwd = mkdtempSync(join(tmpdir(), 'lmm-i18n-test-'))
  t.after(() => rmSync(cwd, { recursive: true, force: true }))
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim()
  const writeLocales = (values) => {
    const directory = join(cwd, 'apps/web/src/i18n/locales')
    mkdirSync(directory, { recursive: true })
    for (const locale of locales) {
      writeFileSync(join(directory, `${locale}.json`), JSON.stringify({ translation: values[locale] }))
    }
  }
  git('init', '--initial-branch=main')
  git('config', 'user.email', 'i18n-test@example.invalid')
  git('config', 'user.name', 'i18n test')
  const initial = translations()
  delete initial.fr.old
  writeLocales(initial)
  git('add', '.')
  git('commit', '-qm', 'Initial translations')
  const baseline = git('rev-parse', 'HEAD')
  git('branch', 'feature')
  // Main repaired a legacy gap after branching; the feature has not deleted that repair.
  writeLocales(translations({ old: 'Existing translation', unrelated: 'Main-only translation' }))
  git('add', '.')
  git('commit', '-qm', 'Main independently adds a key')
  git('checkout', '-q', 'feature')
  const feature = translations({ old: 'Existing translation', added: 'Added translation' })
  delete feature.fr.old
  delete feature.fr.added
  writeLocales(feature)
  git('add', '.')
  git('commit', '-qm', 'Feature adds an incomplete translation')
  const expected = ['fr.json: new key needs a translation: "added"']
  assert.deepEqual(checkRepository({ cwd, base: 'main', mergeBase: true }), expected)
  assert.deepEqual(checkRepository({ cwd, base: 'main' }), [
    'fr.json: existing translation removed or emptied: "old"',
    ...expected,
  ])
  assert.deepEqual(checkRepository({ cwd, base: baseline }), expected)
  feature.fr.added = 'Ajout'
  writeLocales(feature)
  assert.deepEqual(checkRepository({ cwd, base: baseline }), [])
  delete feature.vi.old
  writeLocales(feature)
  // Default HEAD comparison checks uncommitted edits, too.
  assert.deepEqual(checkRepository({ cwd }), ['vi.json: existing translation removed or emptied: "old"'])
  const script = fileURLToPath(new URL('./check-i18n.mjs', import.meta.url))
  const result = spawnSync(process.execPath, [script, '--base', baseline], { cwd, encoding: 'utf8' })
  assert.equal(result.status, 1)
  assert.match(result.stderr, /vi.json: existing translation removed or emptied/)
  const invalidRef = spawnSync(process.execPath, [script, '--base=--help'], { cwd, encoding: 'utf8' })
  assert.equal(invalidRef.status, 1)
  assert.match(invalidRef.stderr, /i18n check could not run/)
})
