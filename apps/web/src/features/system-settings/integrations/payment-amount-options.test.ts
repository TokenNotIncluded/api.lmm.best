/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createPaymentAmountDiscountSchema,
  createPaymentAmountOptionsSchema,
  isPaymentAmountInput,
  isPaymentAmountOptions,
  isPositiveSafeAmount,
  normalizePaymentAmount,
  normalizePaymentAmountDiscountJson,
  normalizePaymentAmountOptionsJson,
  parsePaymentAmountDiscounts,
  parsePaymentAmountOptions,
  paymentAmountCredits,
  paymentAmountOptionsSchema,
} from './payment-amount-options'

const productionDiscounts =
  '{"1":1,"2":0.99,"5":0.97,"10":0.96,"20":0.94,"50":0.92,"100":0.9,"3.5":0.98}'

test('USD presets retain exact decimals and convert to whole fixed-denomination credits', () => {
  assert.deepEqual(parsePaymentAmountOptions('[3.5, 2e-6, 1.0, 2e1]'), [
    '3.5',
    '0.000002',
    '1',
    '20',
  ])
  assert.equal(
    paymentAmountOptionsSchema.parse('[3.50, 2e-6, 1.0, 2e1]'),
    '[3.5,0.000002,1,20]'
  )
  assert.equal(paymentAmountCredits('3.5', 'USD'), 1750000n)
  assert.equal(paymentAmountCredits('0.000002', 'USD'), 1n)
  assert.equal(
    paymentAmountOptionsSchema.parse('[18014398509.481982]'),
    '[18014398509.481982]'
  )
  assert.equal(
    paymentAmountCredits('18014398509.481982', 'USD'),
    9007199254740991n
  )
  assert.ok(isPaymentAmountOptions([3.5, 10, 20]))
  assert.ok(isPaymentAmountInput('3.5'))
})

test('raw JSON cannot hide fractions of a credit or values outside the wallet domain', () => {
  for (const token of [
    '0.000001',
    '3.5000000000000001',
    '18014398509.481984',
    '9007199254740991.1',
    '0',
    '-1',
    '1e19',
    '2e-19',
  ]) {
    assert.equal(normalizePaymentAmount(token), null, token)
    assert.equal(
      paymentAmountOptionsSchema.safeParse(`[${token}]`).success,
      false,
      token
    )
  }
  // Binary parsing would silently turn this spelling into a valid 3.5.
  assert.equal(JSON.parse('[3.5000000000000001]')[0], 3.5)
  assert.equal(parsePaymentAmountOptions('[3.5000000000000001]'), null)
  for (const value of [
    0,
    -1,
    '35',
    true,
    null,
    Number.MAX_SAFE_INTEGER,
    Number.NaN,
    Infinity,
  ]) {
    assert.equal(isPositiveSafeAmount(value), false)
  }
})

test('preset JSON enforces syntax, bounds, and canonical uniqueness without substituting an empty list', () => {
  for (const value of [
    '',
    'null',
    '{}',
    '["3.5"]',
    '[true]',
    '[null]',
    '[NaN]',
    '[+1]',
    '[01]',
    '[1,]',
    '[,1]',
    '[[1]]',
    '[1 2]',
    '[1,1.0]',
    '[20,2e1]',
    '\u00a0[1]',
    '[1\u00a0]',
  ]) {
    assert.equal(parsePaymentAmountOptions(value), null, value)
  }
  assert.deepEqual(parsePaymentAmountOptions(' \r\n[\t1, 3.5\n] '), [
    '1',
    '3.5',
  ])
  assert.equal(paymentAmountOptionsSchema.parse(' [ ] '), '[]')
  const hundred = Array.from({ length: 100 }, (_, index) => index + 1).join(',')
  assert.equal(parsePaymentAmountOptions(`[${hundred}]`)?.length, 100)
  assert.equal(parsePaymentAmountOptions(`[${hundred},101]`), null)
  assert.equal(normalizePaymentAmount(`1.${'0'.repeat(62)}`), '1')
  assert.equal(normalizePaymentAmount(`1.${'0'.repeat(63)}`), null)
  assert.equal(normalizePaymentAmount('1000000000000000000e-18'), '1')
})

test('TOKENS configuration keeps integer CREDIT input instead of using the wallet display currency', () => {
  const schema = createPaymentAmountOptionsSchema('CREDIT')
  assert.equal(
    schema.parse('[1.0,2e1,9007199254740991]'),
    '[1,20,9007199254740991]'
  )
  for (const value of [
    '[3.5]',
    '[0.000002]',
    '[9007199254740991.1]',
    '[9007199254740992]',
  ]) {
    assert.equal(schema.safeParse(value).success, false, value)
  }
  assert.equal(isPaymentAmountInput('3.5', 'CREDIT'), false)
  assert.equal(paymentAmountCredits('20', 'CREDIT'), 20n)
})

test('settings dirty comparison preserves raw invalid tokens and distinguishes adjacent large credit amounts', () => {
  const previous = '[18014398509.481978]'
  const next = '[18014398509.48198]'
  assert.equal(
    JSON.parse(previous)[0],
    JSON.parse(next)[0],
    'binary Number comparison loses the one-credit edit'
  )
  assert.notEqual(
    normalizePaymentAmountOptionsJson(previous, 'USD'),
    normalizePaymentAmountOptionsJson(next, 'USD')
  )
  assert.equal(
    normalizePaymentAmountOptionsJson('[3.5000000000000001]', 'USD'),
    '[3.5000000000000001]'
  )
  assert.equal(normalizePaymentAmountOptionsJson('[3.50]', 'USD'), '[3.5]')
  const invalidDiscount = '{"3.5":0.98,"3.5":0.97}'
  assert.equal(
    normalizePaymentAmountDiscountJson(invalidDiscount, 'USD'),
    invalidDiscount
  )
})

test('production discount map preserves the 3.5 amount and every existing rate', () => {
  const parsed = parsePaymentAmountDiscounts(productionDiscounts, 'USD')
  assert.deepEqual(parsed, JSON.parse(productionDiscounts))
  assert.equal(parsed?.['3.5'], 0.98)
  assert.equal(parsed?.['3'], undefined)
  const serialized =
    createPaymentAmountDiscountSchema('USD').parse(productionDiscounts)
  assert.deepEqual(JSON.parse(serialized), JSON.parse(productionDiscounts))
})

test('discount decimal aliases merge only when rates agree and match backend legacy key grammar', () => {
  assert.deepEqual(
    parsePaymentAmountDiscounts(
      '{"+3.5":0.98,"03.50":0.98,"+2e1":0.94,"001":1}',
      'USD'
    ),
    { '3.5': 0.98, '20': 0.94, '1': 1 }
  )
  assert.deepEqual(
    parsePaymentAmountDiscounts('{"3\\u002e5":0.98,"3.50":0.98}', 'USD'),
    { '3.5': 0.98 }
  )
  assert.equal(
    parsePaymentAmountDiscounts('{"3.5":0.98,"3.50":0.97}', 'USD'),
    null
  )
  assert.equal(
    parsePaymentAmountDiscounts('{"3.5":0.98,"3.5":0.97}', 'USD'),
    null
  )
  assert.equal(parsePaymentAmountDiscounts('{"3.5":0.98}', 'CREDIT'), null)
  assert.deepEqual(
    parsePaymentAmountDiscounts('{"1.0":1,"2e1":0.9}', 'CREDIT'),
    { '1': 1, '20': 0.9 }
  )
})

test('invalid discount JSON, monetary keys, and rates stay invalid', () => {
  for (const value of [
    '',
    'null',
    '[]',
    '{"3.5":0.98,}',
    '{"3.5":"0.98"}',
    '{"3.5":null}',
    '{"3.5":true}',
    '{"3.5":0}',
    '{"3.5":1.01}',
    '{"3.5":1e309}',
    '{"0.000001":0.9}',
    '{"3.5000000000000001":0.98}',
    '{"1e19":0.9}',
    '{"-1":0.9}',
    '{"0":0.9}',
    '{" 3.5":0.98}',
    '{"3\n.5":0.98}',
    `{"${'0'.repeat(64)}1":0.9}`,
  ]) {
    assert.equal(parsePaymentAmountDiscounts(value, 'USD'), null, value)
  }
  assert.deepEqual(parsePaymentAmountDiscounts(' { } ', 'USD'), {})
})
