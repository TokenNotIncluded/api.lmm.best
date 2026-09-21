/* Copyright (C) 2026 LIghtJUNction */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { parseSettlementQuote } from './settlement-quote'

test('keeps the server-owned same-currency savings breakdown', () => {
  assert.deepEqual(
    parseSettlementQuote({
      amount: '1.49',
      currency: 'USD',
      originalAmount: '2.00',
      savingsAmount: '0.51',
    }),
    {
      amount: '1.49',
      currency: 'USD',
      originalAmount: '2.00',
      savingsAmount: '0.51',
    }
  )
  assert.deepEqual(parseSettlementQuote({ amount: '10.00', currency: 'CNY' }), {
    amount: '10.00',
    currency: 'CNY',
  })
})

test('rejects malformed or impossible savings without inventing a discount', () => {
  assert.equal(
    parseSettlementQuote({
      amount: '1.49',
      currency: 'USD',
      originalAmount: '1.00',
      savingsAmount: '0.49',
    }),
    null
  )
  assert.deepEqual(
    parseSettlementQuote({
      amount: '1.49',
      currency: 'USD',
      originalAmount: 'not-a-number',
      savingsAmount: 'not-a-number',
    }),
    { amount: '1.49', currency: 'USD' }
  )
})
