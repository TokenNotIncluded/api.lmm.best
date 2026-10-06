/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { storeSellerId, storeSellerMailto } from './merchant-profile'

test('store merchant ids are bounded and canonical', () => {
  assert.equal(storeSellerId('27'), 27)
  assert.equal(storeSellerId(2147483647), 2147483647)
  for (const value of [
    0,
    -1,
    '01',
    '+1',
    '1 OR 1=1',
    '2147483648',
    {},
    undefined,
  ]) {
    assert.equal(storeSellerId(value), undefined)
  }
})

test('store contact mailto encodes address punctuation without adding headers', () => {
  assert.equal(
    storeSellerMailto('sales+shop@example.test'),
    'mailto:sales%2Bshop%40example.test'
  )
  const link = storeSellerMailto('sales?subject=x%0A@example.test')
  assert.equal(link, 'mailto:sales%3Fsubject%3Dx%250A%40example.test')
  assert.ok(!link?.includes('?'))
  for (const value of [
    'sales@example.test\r\nBcc: other@example.test',
    'a\x00@example.test',
    'a\u0085@example.test',
    '',
    'not-an-email',
    undefined,
  ]) {
    assert.equal(storeSellerMailto(value), undefined)
  }
})
