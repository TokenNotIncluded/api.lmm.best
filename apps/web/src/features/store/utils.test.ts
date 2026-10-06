/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  CREDITS_PER_USD,
  EXTERNAL_MINIMUM_QUOTA,
  MAX_IMPORT_BYTES,
  parseInventoryText,
  safeStoreUrl,
  storeTotal,
} from './utils'

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
