/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  currentPaymentDiscount,
  formatDiscountPercent,
  parsePaymentDiscount,
} from './payment-discount'

const quote = {
  schema_version: 1,
  currency: 'CNY',
  original_amount: '100.00',
  paid_amount: '90.00',
  savings_amount: '10.00',
  discount_percent: '10.00',
  basis: 'amount_preset_and_code',
}
test('real same-ISO server money determines original price, savings and percent', () => {
  assert.deepEqual(parsePaymentDiscount(quote, '90.00', 'CNY'), {
    currency: 'CNY',
    original: 100,
    paid: 90,
    savings: 10,
    percent: 10,
  })
  assert.deepEqual(
    parsePaymentDiscount(
      {
        ...quote,
        original_amount: '2.00',
        paid_amount: '1.49',
        savings_amount: '0.51',
        discount_percent: '25.50',
      },
      '1.49',
      'CNY'
    ),
    { currency: 'CNY', original: 2, paid: 1.49, savings: 0.51, percent: 25.5 }
  )
  assert.equal(formatDiscountPercent(4.5, 'en'), '4.5')
})
test('missing, stale, inconsistent, zero-discount, extra-fee or currency-mismatched breakdown cannot advertise savings', () => {
  for (const bad of [
    undefined,
    { ...quote, currency: 'USD' },
    { ...quote, original_amount: '90.00', savings_amount: '0.00' },
    { ...quote, original_amount: '85.00' },
    { ...quote, savings_amount: '11.00' },
    { ...quote, paid_amount: '80.00' },
    { ...quote, discount_percent: '20.00' },
    { ...quote, basis: 'pre_coupon' },
    { ...quote, schema_version: 2 },
    { ...quote, original_amount: '100x' },
  ]) {
    assert.equal(parsePaymentDiscount(bad, '90.00', 'CNY'), null)
  }
  const parsed = parsePaymentDiscount(quote, '90.00', 'CNY')
  assert.equal(currentPaymentDiscount(parsed, 90, 'CNY', true), null)
  assert.equal(currentPaymentDiscount(parsed, 90, 'USD', false), null)
  assert.equal(currentPaymentDiscount(parsed, 80, 'CNY', false), null)
})
test('decimal money equality is exact, and a tiny real discount is never called zero percent', () => {
  assert.equal(
    parsePaymentDiscount(
      {
        ...quote,
        original_amount: '0.00000010000000001',
        paid_amount: '0.00000010000000000',
        savings_amount: '0.00000000000000001',
        discount_percent: '0.00',
      },
      '0.00000010000000000',
      'CNY'
    )?.savings,
    1e-17
  )
  assert.equal(formatDiscountPercent(0.000001, 'en'), '<0.01')
  assert.equal(
    parsePaymentDiscount(
      {
        ...quote,
        original_amount: '100.0000000000000001',
        savings_amount: '10.0000000000000002',
      },
      '90.00',
      'CNY'
    ),
    null
  )
})
