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

import { PAYMENT_TYPES } from '../constants'
import { isPositivePaymentAmount, requestPaymentAmount } from './use-payment'

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
        return { success: true, data: '100' }
      },
      stripe: async () => ({ success: true, data: '0' }),
      waffo: async () => ({ success: true, data: '0' }),
      waffoPancake: async () => ({ success: true, data: '0' }),
    })

    assert.deepEqual(requests, [{ amount: 10, payment_method: 'epay' }])
  })

  test('does not add a regular gateway field when calculators share a function', async () => {
    const requests: Array<{ amount: number; payment_method?: string }> = []
    const sharedCalculator = async (request: {
      amount: number
      payment_method?: string
    }) => {
      requests.push(request)
      return { success: true, data: '1' }
    }

    await requestPaymentAmount(10, PAYMENT_TYPES.STRIPE, {
      regular: sharedCalculator,
      stripe: sharedCalculator,
      waffo: sharedCalculator,
      waffoPancake: sharedCalculator,
    })

    assert.deepEqual(requests, [{ amount: 10 }])
  })

  test('uses the dedicated Waffo amount calculator', async () => {
    const calls: string[] = []
    const amount = await requestPaymentAmount(120, PAYMENT_TYPES.WAFFO, {
      regular: async () => {
        calls.push('regular')
        return { success: true, data: '1' }
      },
      stripe: async () => {
        calls.push('stripe')
        return { success: true, data: '2' }
      },
      waffo: async (request) => {
        calls.push(`waffo:${request.amount}`)
        return { success: true, data: '18.75' }
      },
      waffoPancake: async () => {
        calls.push('pancake')
        return { success: true, data: '4' }
      },
    })

    assert.equal(amount, 18.75)
    assert.deepEqual(calls, ['waffo:120'])
  })
})
