/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { api } from '@/lib/api'

import { PAYMENT_TYPES } from '../constants'
import {
  isPositivePaymentAmount,
  requestPaymentAmount,
  requestPaymentQuote,
} from './use-payment'

describe('payment amount routing', () => {
  test('rejects missing, non-finite, and zero checkout amounts', () => {
    assert.equal(isPositivePaymentAmount(undefined), false)
    assert.equal(isPositivePaymentAmount(Number.NaN), false)
    assert.equal(isPositivePaymentAmount(Number.POSITIVE_INFINITY), false)
    assert.equal(isPositivePaymentAmount(Number.NEGATIVE_INFINITY), false)
    assert.equal(isPositivePaymentAmount(0), false)
    assert.equal(isPositivePaymentAmount(-0.01), false)
    assert.equal(isPositivePaymentAmount(0.01), true)
  })

  test('sends the selected regular gateway to the amount endpoint', async () => {
    const requests: Array<{ amount: number; payment_method?: string }> = []

    await requestPaymentAmount(10, 'epay', {
      regular: async (request) => {
        requests.push(request)
        return { success: true, data: '100', settlement_currency: 'USD' }
      },
      stripe: async () => ({
        success: true,
        data: '0',
        settlement_currency: 'USD',
      }),
      waffo: async () => ({
        success: true,
        data: '0',
        settlement_currency: 'USD',
      }),
      waffoPancake: async () => ({
        success: true,
        data: '0',
        settlement_currency: 'USD',
      }),
    })

    assert.deepEqual(requests, [
      { amount: 10, amount_unit: 'CREDIT', payment_method: 'epay' },
    ])
  })

  test('does not add a regular gateway field when calculators share a function', async () => {
    const requests: Array<{ amount: number; payment_method?: string }> = []
    const sharedCalculator = async (request: {
      amount: number
      payment_method?: string
    }) => {
      requests.push(request)
      return { success: true, data: '1', settlement_currency: 'USD' }
    }

    await requestPaymentAmount(10, PAYMENT_TYPES.STRIPE, {
      regular: sharedCalculator,
      stripe: sharedCalculator,
      waffo: sharedCalculator,
      waffoPancake: sharedCalculator,
    })

    assert.deepEqual(requests, [{ amount: 10, amount_unit: 'CREDIT' }])
  })

  test('uses the dedicated Waffo amount calculator', async () => {
    const calls: string[] = []
    const amount = await requestPaymentAmount(120, PAYMENT_TYPES.WAFFO, {
      regular: async () => {
        calls.push('regular')
        return { success: true, data: '1', settlement_currency: 'USD' }
      },
      stripe: async () => {
        calls.push('stripe')
        return { success: true, data: '2', settlement_currency: 'USD' }
      },
      waffo: async (request) => {
        calls.push(`waffo:${request.amount}`)
        return { success: true, data: '18.75', settlement_currency: 'USD' }
      },
      waffoPancake: async () => {
        calls.push('pancake')
        return { success: true, data: '4', settlement_currency: 'USD' }
      },
    })

    assert.equal(amount, 18.75)
    assert.deepEqual(calls, ['waffo:120'])
  })
})

test('uses authoritative credited quota without treating it as the payment amount', async () => {
  const quote = await requestPaymentQuote(1, 'card', {
    regular: async (request) => {
      assert.equal(request.amount, 1)
      assert.equal(request.amount_unit, 'CREDIT')
      return {
        success: true,
        data: '0.01',
        settlement_currency: 'USD',
        credited_quota: 1,
      }
    },
    stripe: async () => ({ success: false }),
    waffo: async () => ({ success: false }),
    waffoPancake: async () => ({ success: false }),
  })
  assert.equal(quote.amount, 0.01)
  assert.equal(quote.creditedQuota, 1)
  assert.equal(quote.paymentCurrency, 'USD')
})

test('raw CREDIT quote validation rejects invalid values without calling any gateway calculator', async () => {
  let calls = 0
  const calculator = async () => {
    calls++
    return { success: true, data: '0.01', settlement_currency: 'USD' }
  }
  const calculators = {
    regular: calculator,
    stripe: calculator,
    waffo: calculator,
    waffoPancake: calculator,
  }
  for (const paymentType of [
    'card',
    PAYMENT_TYPES.STRIPE,
    PAYMENT_TYPES.WAFFO,
    PAYMENT_TYPES.WAFFO_PANCAKE,
  ]) {
    for (const amount of [
      0,
      -1,
      0.000002,
      1.5,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.MAX_SAFE_INTEGER + 1,
    ]) {
      assert.deepEqual(
        await requestPaymentQuote(amount, paymentType, calculators),
        {
          amount: 0,
          settlementQuote: null,
          errorReason: 'Invalid top-up amount',
        }
      )
    }
  }
  assert.equal(calls, 0)
})

test('gateway routing preserves large raw integers and the discount code without treating fiat as CREDIT', async () => {
  for (const rawAmount of [
    1,
    4503599627370497,
    9007199254740987,
    Number.MAX_SAFE_INTEGER,
  ]) {
    const requests: unknown[] = []
    const calculator = async (request: {
      amount: number
      amount_unit?: 'CREDIT'
      discount_code?: string
    }) => {
      requests.push(request)
      return {
        success: true,
        data: '0.0100',
        settlement_currency: 'USD',
        credited_quota: rawAmount,
      }
    }
    const quote = await requestPaymentQuote(
      rawAmount,
      PAYMENT_TYPES.STRIPE,
      'SAVE',
      {
        regular: calculator,
        stripe: calculator,
        waffo: calculator,
        waffoPancake: calculator,
      }
    )
    assert.deepEqual(requests, [
      { amount: rawAmount, amount_unit: 'CREDIT', discount_code: 'SAVE' },
    ])
    assert.equal(quote.amount, 0.01)
    assert.equal(quote.creditedQuota, rawAmount)
    assert.equal(quote.paymentCurrency, 'USD')
  }
})

test('default quote calculators fail closed on missing CREDIT routes without legacy fallback', async () => {
  const originalPost = api.post
  const captured: Array<{ url: string; body: unknown }> = []
  api.post = (async (url: string, body: unknown) => {
    captured.push({ url, body })
    throw {
      isAxiosError: true,
      response: { status: 404, data: { message: 'CREDIT unavailable' } },
    }
  }) as typeof api.post
  try {
    for (const paymentType of [
      'card',
      PAYMENT_TYPES.STRIPE,
      PAYMENT_TYPES.WAFFO,
      PAYMENT_TYPES.WAFFO_PANCAKE,
    ]) {
      assert.deepEqual(await requestPaymentQuote(1, paymentType), {
        amount: 0,
        settlementQuote: null,
        errorReason: 'CREDIT unavailable',
      })
    }
    assert.deepEqual(captured, [
      {
        url: '/api/user/topup/currency/v2/amount',
        body: {
          amount: 1,
          amount_unit: 'LEDGER_QUOTA',
          credit_metadata_version: 2,
          payment_method: 'card',
        },
      },
      {
        url: '/api/user/topup/currency/v2/stripe/amount',
        body: {
          amount: 1,
          amount_unit: 'LEDGER_QUOTA',
          credit_metadata_version: 2,
        },
      },
      {
        url: '/api/user/topup/currency/v2/waffo/amount',
        body: {
          amount: 1,
          amount_unit: 'LEDGER_QUOTA',
          credit_metadata_version: 2,
        },
      },
      {
        url: '/api/user/topup/currency/v2/waffo-pancake/amount',
        body: {
          amount: 1,
          amount_unit: 'LEDGER_QUOTA',
          credit_metadata_version: 2,
        },
      },
    ])
  } finally {
    api.post = originalPost
  }
})

test('all quote calculators preserve same-quote CNY discount metadata and keep malformed promotions independent of a valid charge', async () => {
  const valid = {
    schema_version: 1,
    currency: 'CNY',
    original_amount: '100.00',
    paid_amount: '72.00',
    savings_amount: '28.00',
    discount_percent: '28.00',
    basis: 'amount_preset_and_code',
  }
  for (const type of [
    'alipay',
    PAYMENT_TYPES.STRIPE,
    PAYMENT_TYPES.WAFFO,
    PAYMENT_TYPES.WAFFO_PANCAKE,
  ]) {
    for (const metadata of [
      valid,
      undefined,
      { ...valid, currency: 'USD' },
      { ...valid, paid_amount: '71.00' },
      { ...valid, savings_amount: '10.00' },
    ]) {
      const calculator = async () => ({
        success: true,
        data: '72.00',
        amount: '72.00',
        settlement_currency: 'CNY',
        settlement_quote: metadata,
        credited_quota: 50000000,
      })
      const quote = await requestPaymentQuote(50000000, type, 'SAVE10', {
        regular: calculator,
        stripe: calculator,
        waffo: calculator,
        waffoPancake: calculator,
      })
      assert.equal(quote.amount, 72)
      assert.equal(quote.paymentCurrency, 'CNY')
      assert.equal(quote.creditedQuota, 50000000)
      assert.deepEqual(
        quote.paymentDiscount,
        metadata === valid
          ? {
              currency: 'CNY',
              original: 100,
              paid: 72,
              savings: 28,
              percent: 28.000000000000004,
            }
          : null
      )
    }
  }
})

test('a missing or non-fiat quote currency cannot be relabelled as a payable USD or CNY amount', async () => {
  for (const settlement_currency of [
    undefined,
    '',
    'CREDIT',
    'LDC',
    'AAA',
    '????',
    'cny',
    ' CNY ',
  ]) {
    const calculator = async () => ({
      success: true,
      data: '90.00',
      settlement_currency,
    })
    const quote = await requestPaymentQuote(50000000, 'alipay', {
      regular: calculator,
      stripe: calculator,
      waffo: calculator,
      waffoPancake: calculator,
    })
    assert.equal(quote.amount, 0)
    assert.equal(quote.paymentCurrency, undefined)
    assert.equal(quote.paymentDiscount, undefined)
    assert.equal(quote.errorReason, 'Payment unavailable')
  }
})
