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
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { HeroSmsSmsOffer, HeroSmsSmsOrder } from './sms-api.js'
import {
  getSmsPurchaseBalance,
  isSmsMinimumBalanceError,
  SMS_MINIMUM_BALANCE_CODE,
} from './sms-balance.js'
import { purchaseHeroSmsBatch } from './sms-purchase.js'

describe('new temporary SMS purchase balance', () => {
  test('allows exactly USD 10, but not one quota unit less', () => {
    assert.equal(getSmsPurchaseBalance(5_000_000, 500_000).status, 'allowed')
    assert.equal(
      getSmsPurchaseBalance(4_999_999, 500_000).status,
      'below-minimum'
    )
    assert.equal(getSmsPurchaseBalance(0, 500_000).status, 'below-minimum')
  })

  test('uses configured quota per USD without a display currency conversion', () => {
    assert.deepEqual(getSmsPurchaseBalance(10_000, 1_000), {
      status: 'allowed',
      balanceUSD: 10,
    })
    assert.equal(getSmsPurchaseBalance(9_999, 1_000).status, 'below-minimum')
  })

  test('unknown or failed balance/configuration is not treated as zero', () => {
    for (const value of [undefined, null, Number.NaN, Infinity, '5000000']) {
      assert.deepEqual(getSmsPurchaseBalance(value, 500_000), {
        status: 'unknown',
        balanceUSD: undefined,
      })
    }
    for (const ratio of [0, -1, Number.NaN, Infinity]) {
      assert.equal(getSmsPurchaseBalance(5_000_000, ratio).status, 'unknown')
    }
  })

  test('a batch stops at the next starting-balance check without losing paid orders', async () => {
    const offer: HeroSmsSmsOffer = {
      id: 'quote',
      country_id: 6,
      service: 'tg',
      operator: '',
      inventory: 3,
      customer_price_usd: '1',
      charge_quota: 500_000,
    }
    const order: HeroSmsSmsOrder = {
      id: 'order',
      country_id: 6,
      service: 'tg',
      operator: '',
      status: 'active',
      customer_price_usd: '1',
      charge_quota: 500_000,
      refunded_quota: 0,
      provider_id: null,
      phone_number: '',
      code: '',
      message: '',
      last_error_code: '',
      last_error_message: '',
      created_at: 0,
      updated_at: 0,
    }
    let quota = 5_500_000
    const chargedKeys: string[] = []
    const result = await purchaseHeroSmsBatch({
      initialOffer: offer,
      quantity: 3,
      idempotencyKey: 'batch',
      getFreshOffer: async () => offer,
      createOrder: async (_offerId, key) => {
        if (getSmsPurchaseBalance(quota, 500_000).status !== 'allowed') {
          throw Object.assign(new Error('minimum balance'), {
            code: SMS_MINIMUM_BALANCE_CODE,
          })
        }
        quota -= offer.charge_quota
        chargedKeys.push(key)
        return { order: { ...order, id: key }, quota }
      },
      isAmbiguousNetworkError: () => false,
    })
    assert.deepEqual(chargedKeys, ['batch-1', 'batch-2'])
    assert.equal(result.orders.length, 2)
    assert.equal(result.failure?.item, 3)
    assert.equal(isSmsMinimumBalanceError(result.failure?.error), true)
    assert.equal(quota, 4_500_000)
  })

  test('recognizes both HTTP and business-envelope minimum balance denials', () => {
    assert.equal(
      isSmsMinimumBalanceError({
        response: { status: 402, data: { code: SMS_MINIMUM_BALANCE_CODE } },
      }),
      true
    )
    assert.equal(
      isSmsMinimumBalanceError(
        Object.assign(new Error('denied'), { code: SMS_MINIMUM_BALANCE_CODE })
      ),
      true
    )
    for (const error of [
      undefined,
      null,
      new Error('offline'),
      { code: 'INSUFFICIENT_BALANCE' },
      { response: {} },
    ]) {
      assert.equal(isSmsMinimumBalanceError(error), false)
    }
  })
})
