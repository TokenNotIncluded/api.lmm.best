/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  isPaymentAmountInput,
  isPaymentAmountOptions,
  isPositiveSafeAmount,
  paymentAmountOptionsSchema,
} from './payment-amount-options'

test('payment JSON and visual editor share positive safe integer rules', () => {
  const valid = [10, 20, 35, 50, 100, 200, 500, 1000]
  assert.ok(isPaymentAmountOptions(valid))
  assert.equal(
    paymentAmountOptionsSchema.parse(JSON.stringify(valid)),
    JSON.stringify(valid)
  )
  for (const value of [
    3.5,
    0,
    -1,
    '35',
    true,
    null,
    Number.MAX_SAFE_INTEGER + 1,
    Number.NaN,
    Infinity,
  ]) {
    assert.equal(isPositiveSafeAmount(value), false)
    assert.equal(isPaymentAmountOptions([10, value, 35]), false)
    assert.equal(
      paymentAmountOptionsSchema.safeParse(JSON.stringify([10, value, 35]))
        .success,
      false
    )
  }
  for (const text of [
    '',
    'null',
    '{}',
    '[1,2,',
    '[NaN]',
    '[1.0]',
    '[2e1]',
    '[9007199254740991.1]',
  ]) {
    assert.equal(paymentAmountOptionsSchema.safeParse(text).success, false)
  }
  assert.equal(paymentAmountOptionsSchema.parse('[]'), '[]')
  assert.equal(
    paymentAmountOptionsSchema.parse('[ 1, 20, 9007199254740991 ]'),
    '[1,20,9007199254740991]'
  )
  for (const text of [
    '3.5',
    '1.0',
    '2e1',
    '9007199254740991.1',
    '0',
    '-1',
    '35bad',
    '9007199254740992',
  ]) {
    assert.equal(isPaymentAmountInput(text), false)
  }
  assert.equal(isPaymentAmountInput('35'), true)
})
