/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { formatFiatAmountInput } from './payment-amount-input'

test('read-only fiat drafts are short and mark rounding without hiding a positive minimum', () => {
  assert.equal(
    formatFiatAmountInput('1.488208625419079548918012800976', 'en'),
    '≈1.49'
  )
  assert.equal(
    formatFiatAmountInput('14.882086254190795489180128009754', 'zh'),
    '≈14.88'
  )
  assert.equal(
    formatFiatAmountInput('14.882086254190795489180128009754', 'zhCN'),
    '≈14.88'
  )
  assert.equal(
    formatFiatAmountInput('14.882086254190795489180128009754', 'zhTW'),
    '≈14.88'
  )
  assert.equal(formatFiatAmountInput('100'), '100')
  assert.equal(
    formatFiatAmountInput('0.000000297641725083815909783603', 'en'),
    '≈0.0000003'
  )
  assert.notEqual(
    formatFiatAmountInput('0.000000000000000000000000000001', 'en'),
    '0'
  )
  for (const invalid of ['', 'abc', '-1', '1..2']) {
    assert.equal(formatFiatAmountInput(invalid), invalid)
  }
})
