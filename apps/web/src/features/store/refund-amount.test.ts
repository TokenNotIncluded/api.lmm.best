/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  storeRefundNativeAmountFromInput,
  storeRefundNativeAmountInput,
  storeRefundNativeAmountSupported,
  storeRefundNativeAmountText,
} from './refund-amount'

test('ordinary original-payment amounts enter and leave the existing store protocol', () => {
  for (const currency of ['USD', 'CNY', 'LDC']) {
    assert.equal(storeRefundNativeAmountFromInput('20', currency), 2000)
    assert.equal(storeRefundNativeAmountFromInput('20.00', currency), 2000)
    assert.equal(storeRefundNativeAmountFromInput('20.01', currency), 2001)
    assert.equal(storeRefundNativeAmountFromInput('0.01', currency), 1)
    assert.equal(
      storeRefundNativeAmountText(2000, currency),
      `20.00 ${currency}`
    )
    assert.equal(storeRefundNativeAmountInput(1, currency), '0.01')
    assert.equal(storeRefundNativeAmountInput(0, currency), '0.00')
  }
})

test('payment precision and large integral limits cannot be rounded into a different refund', () => {
  for (const input of [
    '0',
    '0.001',
    '20.001',
    '1e3',
    '-1',
    'NaN',
    '20,00',
    '.5',
    '20.',
    '',
  ]) {
    assert.equal(
      storeRefundNativeAmountFromInput(input, 'CNY'),
      undefined,
      input
    )
  }
  assert.equal(
    storeRefundNativeAmountFromInput('90071992547409.91', 'USD'),
    Number.MAX_SAFE_INTEGER
  )
  assert.equal(
    storeRefundNativeAmountInput(Number.MAX_SAFE_INTEGER, 'USD'),
    '90071992547409.91'
  )
  assert.equal(
    storeRefundNativeAmountFromInput('90071992547409.92', 'USD'),
    undefined
  )
  assert.equal(
    storeRefundNativeAmountInput(Number.MAX_SAFE_INTEGER + 1, 'USD'),
    undefined
  )
})

test('an undeclared payment currency has no guessed major-unit conversion', () => {
  for (const currency of ['JPY', 'BHD', '', 'usd', 'USD ']) {
    assert.equal(storeRefundNativeAmountSupported(currency), false)
    assert.equal(storeRefundNativeAmountFromInput('20', currency), undefined)
    assert.equal(storeRefundNativeAmountInput(2000, currency), undefined)
    assert.equal(storeRefundNativeAmountText(2000, currency), undefined)
  }
})
