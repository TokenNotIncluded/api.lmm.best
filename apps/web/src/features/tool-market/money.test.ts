/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { marketQuota } from './money'

test('market prices preserve the exact integer billing unit', () => {
  assert.equal(marketQuota('0', 500000), 0)
  assert.equal(marketQuota('0.05', 500000), 25000)
  assert.equal(marketQuota('0.000002', 500000), 1)
  assert.equal(
    marketQuota('18014398509.481982', 500000),
    Number.MAX_SAFE_INTEGER
  )
  for (const amount of [
    '0.000001',
    '0.000003',
    '-1',
    '1e3',
    'Infinity',
    'NaN',
    '',
    '0x10',
    '18014398509.481984',
  ]) {
    assert.throws(
      () => marketQuota(amount, 500000),
      /Invalid amount/,
      amount || 'empty'
    )
  }
  assert.throws(() => marketQuota('1', 0.5))
})
