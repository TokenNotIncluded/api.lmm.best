/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  readableTopupCurrencies,
  readableTopupMethods,
  readableWalletRisk,
} from './user-data-display'

const risk = {
  score: 0.65,
  high_risk: false,
  version: 2,
  reasons: ['outbound_without_usage'],
  checkin_quota: 250000,
  checkin_count: 1,
  transferred_quota: 100000,
  pending_quota: 0,
  received_quota: 0,
  high_risk_senders: 0,
}

test('valid risk remains unchanged, including optional older counters', () => {
  const before = JSON.stringify(risk)
  assert.equal(readableWalletRisk(risk), true)
  assert.equal(
    readableWalletRisk({ ...risk, moderation_reviewed_count: 12 }),
    true
  )
  assert.equal(JSON.stringify(risk), before)
})

test('invalid risk remains unknown rather than a zero or low risk', () => {
  for (const patch of [
    { score: null },
    { score: undefined },
    { score: '0.65' },
    { score: Number.NaN },
    { score: Infinity },
    { score: -1 },
    { score: 1.1 },
    { reasons: null },
    { reasons: undefined },
    { reasons: { length: 1 } },
    { reasons: [{}] },
    { high_risk: 'false' },
    { high_risk_senders: {} },
    { moderation_flagged_count: null },
  ]) {
    assert.equal(readableWalletRisk({ ...risk, ...patch }), false)
  }
})

test('missing legacy methods are empty, invalid metadata is distinguishable', () => {
  assert.deepEqual(readableTopupMethods(null), [])
  assert.deepEqual(readableTopupMethods(undefined), [])
  for (const value of [
    'bad',
    { length: 1 },
    [null],
    [{ method: null }],
    [{ method: 'card', quota: 1, money_micros: 2, orders: {} }],
  ]) {
    assert.equal(readableTopupMethods(value), null)
  }
})

test('valid topup method amounts and references are preserved exactly', () => {
  const methods = [
    {
      method: ' card ',
      provider: ' source ',
      settlement_currency: ' usd ',
      quota: 672901,
      normalized_quota: 500000,
      quota_projection_available: true,
      money_micros: 1234567,
      orders: 2,
      payment_basis: 'settled',
      settled_orders: 2,
      settled_money_micros: 1234567,
    },
  ]
  const before = JSON.stringify(methods)
  assert.strictEqual(readableTopupMethods(methods), methods)
  assert.equal(JSON.stringify(methods), before)
})

test('currency-only metadata retains valid minimal records and unknown states', () => {
  assert.deepEqual(
    readableTopupCurrencies([{ settlement_currency: ' usd ' }]),
    ['USD']
  )
  assert.deepEqual(readableTopupCurrencies([{}]), ['UNKNOWN'])
  assert.deepEqual(readableTopupCurrencies(null), [])
  for (const value of [
    'bad',
    { length: 1 },
    [null],
    [{ settlement_currency: 42 }],
  ]) {
    assert.equal(readableTopupCurrencies(value), null)
  }
})
