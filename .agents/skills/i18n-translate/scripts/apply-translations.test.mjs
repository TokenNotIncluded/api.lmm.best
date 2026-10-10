import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { applyTranslations } from './apply-translations.mjs'

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
const makePatch = () => Object.fromEntries(locales.map((locale) => [locale, { 'Hello {{name}}': `${locale}: {{name}}` }]))

async function fixture(t) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'lmm-i18n-test-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  for (const locale of locales) await writeFile(path.join(directory, `${locale}.json`), JSON.stringify({ translation: { Existing: locale }, extra: 'keep' }) + '\n')
  const patchFile = path.join(directory, 'patch.input')
  const snapshot = () => Promise.all(locales.map((locale) => readFile(path.join(directory, `${locale}.json`), 'utf8')))
  const run = async (updates, options = {}) => {
    await writeFile(patchFile, JSON.stringify(updates))
    return applyTranslations(patchFile, { localesDir: directory, ...options })
  }
  return { directory, snapshot, run }
}

test('complete patch updates all locales and preserves unrelated content', async (t) => {
  const { directory, run } = await fixture(t)
  assert.equal(await run(makePatch()), 7)
  for (const locale of locales) {
    const result = JSON.parse(await readFile(path.join(directory, `${locale}.json`), 'utf8'))
    assert.equal(result.translation.Existing, locale)
    assert.equal(result.translation['Hello {{name}}'], `${locale}: {{name}}`)
    assert.equal(result.extra, 'keep')
  }
})

test('check mode leaves every file byte-identical', async (t) => {
  const { snapshot, run } = await fixture(t)
  const before = await snapshot()
  assert.equal(await run(makePatch(), { check: true }), 7)
  assert.deepEqual(await snapshot(), before)
})

test('repeating the same patch does not rewrite files', async (t) => {
  const { run } = await fixture(t)
  await run(makePatch())
  assert.equal(await run(makePatch()), 0)
})

for (const [name, mutate] of [
  ['missing locale', (p) => { delete p.vi }],
  ['unexpected locale', (p) => { p.unknown = p.en }],
  ['missing translation key', (p) => { p.fr = {} }],
  ['non-string translation', (p) => { p.ja['Hello {{name}}'] = 42 }],
  ['blank translation', (p) => { p.ru['Hello {{name}}'] = ' ' }],
  ['changed placeholder', (p) => { p.zh['Hello {{name}}'] = 'Hello {{other}}' }],
  ['placeholder removed in all locales', (p) => { for (const locale of locales) p[locale]['Hello {{name}}'] = 'Hello' }],
]) {
  test(`${name} fails before any write`, async (t) => {
    const { snapshot, run } = await fixture(t)
    const before = await snapshot()
    const updates = makePatch()
    mutate(updates)
    await assert.rejects(run(updates))
    assert.deepEqual(await snapshot(), before)
  })
}

test('a corrupt last locale fails before earlier locales are written', async (t) => {
  const { directory, run, snapshot } = await fixture(t)
  await writeFile(path.join(directory, 'zh-TW.json'), '{invalid')
  const before = await snapshot()
  await assert.rejects(run(makePatch()))
  assert.deepEqual(await snapshot(), before)
})

test('semantic keys can use placeholders in the English value', async (t) => {
  const { run } = await fixture(t)
  const updates = Object.fromEntries(locales.map((locale) => [locale, { 'welcome.title': `${locale}: {{name}}` }]))
  assert.equal(await run(updates), 7)
})
