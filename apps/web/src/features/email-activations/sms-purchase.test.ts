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

import type { HeroSmsSmsOffer, HeroSmsSmsOrder } from './sms-api'
import { purchaseHeroSmsBatch, selectHeroSmsPriceTier } from './sms-purchase'

function offer(id: string, chargeQuota = 1_000): HeroSmsSmsOffer {
  return {
    id,
    country_id: 6,
    service: 'tg',
    operator: 'any',
    inventory: 10,
    customer_price_usd: '2',
    charge_quota: chargeQuota,
    bid: false,
    tiers: [
      {
        id,
        inventory: 10,
        customer_price_usd: '2',
        charge_quota: chargeQuota,
      },
    ],
  }
}

function order(id: string): HeroSmsSmsOrder {
  return {
    id,
    country_id: 6,
    service: 'tg',
    operator: 'any',
    status: 'active',
    customer_price_usd: '2',
    charge_quota: 1_000,
    refunded_quota: 0,
    provider_id: id,
    phone_number: `7900${id}`,
    code: '',
    message: '',
    last_error_code: '',
    last_error_message: '',
    created_at: 1,
    updated_at: 1,
  }
}

describe('phone activation quantity purchases', () => {
  test('selects a displayed price tier without silently falling back', () => {
    const base = offer('quote-1')
    base.tiers?.push({
      id: 'quote-2',
      inventory: 4,
      customer_price_usd: '3',
      charge_quota: 1_500,
    })

    const selected = selectHeroSmsPriceTier(base, '3')
    assert.equal(selected?.id, 'quote-2')
    assert.equal(selected?.inventory, 4)
    assert.equal(selectHeroSmsPriceTier(base, '4'), undefined)

    const legacy = { ...offer('legacy'), tiers: undefined, bid: undefined }
    assert.equal(selectHeroSmsPriceTier(legacy, '')?.id, 'legacy')
  })

  test('creates independent orders with deterministic item keys', async () => {
    let nextOffer = 2
    const calls: Array<{ offerId: string; key: string }> = []
    const progress: string[] = []
    const result = await purchaseHeroSmsBatch({
      initialOffer: offer('quote-1'),
      quantity: 3,
      idempotencyKey: 'batch-a',
      getFreshOffer: async () => offer(`quote-${nextOffer++}`),
      createOrder: async (offerId, key) => {
        calls.push({ offerId, key })
        return { order: order(`order-${calls.length}`), quota: 50_000 }
      },
      isAmbiguousNetworkError: () => false,
      onProgress: (completed, total) => progress.push(`${completed}/${total}`),
    })

    assert.equal(result.failure, undefined)
    assert.equal(result.orders.length, 3)
    assert.deepEqual(calls, [
      { offerId: 'quote-1', key: 'batch-a-1' },
      { offerId: 'quote-2', key: 'batch-a-2' },
      { offerId: 'quote-3', key: 'batch-a-3' },
    ])
    assert.deepEqual(progress, ['1/3', '2/3', '3/3'])
  })

  test('stops truthfully when a later quote price changes', async () => {
    let freshCalls = 0
    const result = await purchaseHeroSmsBatch({
      initialOffer: offer('quote-1'),
      quantity: 3,
      idempotencyKey: 'batch-b',
      getFreshOffer: async () => {
        freshCalls += 1
        return offer(`quote-${freshCalls + 1}`, 2_000)
      },
      createOrder: async () => ({ order: order('order-1'), quota: 50_000 }),
      isAmbiguousNetworkError: () => false,
    })

    assert.equal(result.orders.length, 1)
    assert.deepEqual(result.failure, { code: 'PRICE_CHANGED', item: 2 })
  })

  test('retries an ambiguous transport failure with the same item key', async () => {
    const keys: string[] = []
    let attempt = 0
    const result = await purchaseHeroSmsBatch({
      initialOffer: { ...offer('quote-1'), inventory: 1 },
      quantity: 4,
      idempotencyKey: 'batch-c',
      getFreshOffer: async () => offer('unused'),
      createOrder: async (_offerId, key) => {
        keys.push(key)
        attempt += 1
        if (attempt === 1) throw new Error('network')
        return { order: order('order-1'), quota: 50_000 }
      },
      isAmbiguousNetworkError: (error) =>
        error instanceof Error && error.message === 'network',
    })

    assert.equal(result.requested, 1)
    assert.equal(result.orders.length, 1)
    assert.deepEqual(keys, ['batch-c-1', 'batch-c-1'])
  })

  test('keeps the outcome ambiguous when the retry returns a business error', async () => {
    const keys: string[] = []
    let attempt = 0
    const result = await purchaseHeroSmsBatch({
      initialOffer: { ...offer('quote-1'), inventory: 1 },
      quantity: 1,
      idempotencyKey: 'batch-d',
      getFreshOffer: async () => offer('unused'),
      createOrder: async (_offerId, key) => {
        keys.push(key)
        attempt += 1
        throw new Error(attempt === 1 ? 'network' : 'business')
      },
      isAmbiguousNetworkError: (error) =>
        error instanceof Error && error.message === 'network',
    })

    assert.equal(result.orders.length, 0)
    assert.equal(result.failure?.ambiguous, true)
    assert.equal(
      result.failure?.error instanceof Error && result.failure.error.message,
      'business'
    )
    assert.equal(result.failure?.offerId, 'quote-1')
    assert.equal(result.failure?.idempotencyKey, 'batch-d-1')
    assert.deepEqual(keys, ['batch-d-1', 'batch-d-1'])
  })
})
