/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { currentTopupCredits, topupPaymentAmounts } from './topup-display'

const historical = {
  quota: 105_000_000,
  money_micros: 154_000_000,
  orders: 2,
}

test('legacy raw point units and recorded order money do not imply current credits or actual settlement', () => {
  assert.equal(currentTopupCredits(historical), null)
  assert.equal(
    currentTopupCredits({ ...historical, normalized_quota: 15_647_439 }),
    null
  )
  assert.deepEqual(topupPaymentAmounts(historical), {
    settled: null,
    historical: 154_000_000,
  })
})

test('only a verified current-unit projection can label the historical two-order sample as credits', () => {
  const sample = {
    ...historical,
    normalized_quota: 15_647_439,
    quota_projection_available: true,
    settled_money_micros: 154_000_000,
    historical_money_micros: 0,
    settled_orders: 2,
    historical_orders: 0,
    payment_basis: 'settled' as const,
  }
  assert.equal(currentTopupCredits(sample), 15_647_439)
  assert.deepEqual(topupPaymentAmounts(sample), {
    settled: 154_000_000,
    historical: null,
  })
  assert.equal(
    currentTopupCredits({ ...sample, quota_projection_available: false }),
    null
  )
  for (const value of [-1, 1.5, Number.MAX_SAFE_INTEGER + 1, Number.NaN]) {
    assert.equal(
      currentTopupCredits({ ...sample, normalized_quota: value }),
      null
    )
  }
})

test('mixed evidence preserves separate settled money and historical quotation without summing their meanings', () => {
  assert.deepEqual(
    topupPaymentAmounts({
      quota: 1_500_000,
      money_micros: 30_000_000,
      orders: 2,
      settled_money_micros: 10_000_000,
      historical_money_micros: 20_000_000,
      settled_orders: 1,
      historical_orders: 1,
      payment_basis: 'mixed',
    }),
    { settled: 10_000_000, historical: 20_000_000 }
  )
})

test('settlement labels require the explicit basis and nonempty supporting order count', () => {
  assert.deepEqual(
    topupPaymentAmounts({
      ...historical,
      settled_orders: 2,
      payment_basis: 'settled',
    }),
    { settled: null, historical: null }
  )
  assert.deepEqual(
    topupPaymentAmounts({
      ...historical,
      settled_money_micros: 154_000_000,
      settled_orders: 0,
      payment_basis: 'settled',
    }),
    { settled: null, historical: null }
  )
  assert.deepEqual(topupPaymentAmounts({ ...historical, orders: 0 }), {
    settled: null,
    historical: null,
  })
})
