/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

import { homeWorldCopy } from './home-world-copy.mjs'

// Translated by the actual gpt-6-luna CLI, including a second semantic review.
// Frozen source SHA256: 962dd0688525def9a4bdad9938bcbc923219b82a352a2ec55881f8a42f7a86a3
// Original final module SHA256: 4672e445c5ec35c9824e208b2d88a4b696948c96971e6c1c97f621934f6fce4c
const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
const keys = Object.keys(homeWorldCopy.en)
assert.equal(keys.length, 15)
assert.deepEqual(Object.keys(homeWorldCopy).sort(), [...locales].sort())
for (const locale of locales) {
  assert.deepEqual(Object.keys(homeWorldCopy[locale]), keys)
  for (const key of keys) {
    assert.equal(typeof homeWorldCopy[locale][key], 'string')
    assert.ok(homeWorldCopy[locale][key].trim())
    if (locale === 'en') assert.equal(homeWorldCopy[locale][key], key)
  }
}
const source = await readFile(
  new URL('../src/features/home/home-landing.tsx', import.meta.url),
  'utf8'
)
for (const key of keys) {
  assert.ok(source.includes(key), `Frozen source key missing: ${key}`)
}
const write = process.argv.includes('--write')
const report = []
for (const locale of locales) {
  const url = new URL(`../src/i18n/locales/${locale}.json`, import.meta.url)
  const document = JSON.parse(await readFile(url, 'utf8'))
  assert.ok(document.translation && typeof document.translation === 'object')
  const differences = keys.filter(
    (key) => document.translation[key] !== homeWorldCopy[locale][key]
  )
  if (write && differences.length) {
    Object.assign(document.translation, homeWorldCopy[locale])
    await writeFile(url, `${JSON.stringify(document, null, 2)}\n`)
  }
  report.push({
    locale,
    differences: differences.length,
    applied: write ? differences.length : 0,
    path: fileURLToPath(url),
  })
}
console.log(
  JSON.stringify(
    {
      sourceKeys: keys.length,
      actualLunaValues: keys.length * locales.length,
      write,
      locales: report,
    },
    null,
    2
  )
)
if (!write && report.some(({ differences }) => differences)) {
  process.exitCode = 1
}
