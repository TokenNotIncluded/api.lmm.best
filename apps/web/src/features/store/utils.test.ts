/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { analyzeInventoryItems } from './inventory-import-analysis'
import {
  CREDITS_PER_USD,
  EXTERNAL_MINIMUM_QUOTA,
  isStoreEmail,
  MAX_IMPORT_BYTES,
  parseInventoryText,
  safeStoreUrl,
  storeDate,
  storeTotal,
} from './utils'

test('pickup email accepts plain addresses and rejects malformed or oversized input', () => {
  assert.equal(isStoreEmail('buyer+store@mail.example.test'), true)
  for (const value of [
    '',
    'buyer',
    'buyer@',
    '@example.test',
    'buyer@@example.test',
    'buyer@example',
    'buyer@example.test extra',
    'buyer@example.test\nBcc: another@example.test',
    `${'b'.repeat(250)}@example.test`,
  ]) {
    assert.equal(isStoreEmail(value), false, value)
  }
})

test('store integer credits retain the immutable USD anchor and strict external threshold', () => {
  assert.equal(CREDITS_PER_USD, 500000)
  assert.equal(EXTERNAL_MINIMUM_QUOTA, 5000000)
  assert.equal(storeTotal(5000000, 3), 15000000)
  for (const [unit, quantity] of [
    [0, 1],
    [1, 0],
    [1.5, 1],
    [1, 2.1],
    [Number.MAX_SAFE_INTEGER, 2],
  ]) {
    assert.throws(() => storeTotal(unit, quantity), /Invalid amount/)
  }
})
test('order dates accept both Chinese interface language codes without crashing', () => {
  const timestamp = 1791270000
  for (const [language, locale] of [
    ['zhCN', 'zh-CN'],
    ['zhTW', 'zh-TW'],
    ['en', 'en'],
  ]) {
    assert.equal(
      storeDate(timestamp, language),
      new Date(timestamp * 1000).toLocaleString(locale)
    )
    assert.equal(storeDate(0, language), '—')
  }
})
test('inventory handles BOM, CRLF, empty lines and preserves duplicate units', () => {
  assert.deepEqual(
    parseInventoryText('\uFEFFkey-a\r\n\r\n key-b \r\nkey-a\n'),
    ['key-a', ' key-b ', 'key-a']
  )
  assert.deepEqual(parseInventoryText(' \n\t'), [])
  assert.throws(
    () => parseInventoryText('x'.repeat(MAX_IMPORT_BYTES + 1)),
    /too large/
  )
  assert.throws(
    () => parseInventoryText(Array(10001).fill('key').join('\n')),
    /too large/
  )
  assert.equal(parseInventoryText('密'.repeat(10000)).length, 1)
  assert.throws(() => parseInventoryText('密'.repeat(11000)), /too large/)
})
test('inventory import duplicate preview preserves reusable items and exact card text', () => {
  const original = parseInventoryText(
    '\uFEFFkey-A\r\nkey-A\r\n key-A \r\nkey-a\r\n密钥\r\n密钥\r\n\r\n'
  )
  const snapshot = [...original]
  const preview = analyzeInventoryItems(original)
  assert.equal(preview.count, 6)
  assert.equal(preview.repeated, 2)
  assert.deepEqual(preview.unique, ['key-A', ' key-A ', 'key-a', '密钥'])
  assert.deepEqual(original, snapshot)
  assert.deepEqual(analyzeInventoryItems([]), {
    count: 0,
    repeated: 0,
    unique: [],
  })
  assert.equal(
    analyzeInventoryItems(Array(1000).fill('same delivery text')).count,
    1000
  )
})
test('seller links reject executable protocols, embedded credentials and malformed URLs', () => {
  for (const input of [
    'javascript:alert(1)',
    'data:text/html,secret',
    'https://user:secret@example.test',
    '/relative',
    'not a link',
  ]) {
    assert.equal(safeStoreUrl(input), undefined)
  }
  assert.equal(
    safeStoreUrl('https://example.test/help?x=1'),
    'https://example.test/help?x=1'
  )
})
