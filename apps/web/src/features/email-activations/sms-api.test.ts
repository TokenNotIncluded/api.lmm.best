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
import { afterEach, describe, test } from 'node:test'

import { api } from '@/lib/api'

import {
  cancelHeroSmsSmsOrder,
  createHeroSmsSmsOrder,
  getHeroSmsSmsOffer,
  listCurrentHeroSmsSmsOrders,
  listHeroSmsSmsCountries,
  listHeroSmsSmsOperators,
  listHeroSmsSmsOrders,
} from './sms-api'

const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
})

describe('phone activation api', () => {
  test('loads countries and a priced offer', async () => {
    api.get = (async (url: string, config?: { params?: unknown }) => {
      if (url.endsWith('/countries')) {
        assert.deepEqual(config?.params, { service: 'tg' })
        return {
          data: {
            success: true,
            data: [
              {
                id: 6,
                name: '俄罗斯',
                english_name: 'Russia',
                chinese_name: '俄罗斯',
                popularity: 12,
              },
            ],
          },
        }
      }
      if (url.endsWith('/operators')) {
        assert.deepEqual(config?.params, { country: 6 })
        return {
          data: { success: true, data: ['mts', 'tele2'] },
        }
      }
      assert.equal(url, '/api/hero-sms/sms/offer')
      assert.deepEqual(config?.params, {
        country: 6,
        service: 'tg',
        operator: 'any',
        max_price_usd: '2.5',
      })
      return {
        data: {
          success: true,
          data: {
            id: 'hssq_test',
            country_id: 6,
            service: 'tg',
            operator: 'any',
            inventory: 3,
            customer_price_usd: '2',
            charge_quota: 1_000_000,
            bid: true,
            tiers: [
              {
                id: 'hssq_tier',
                inventory: 3,
                customer_price_usd: '2',
                charge_quota: 1_000_000,
              },
            ],
          },
        },
      }
    }) as typeof api.get

    const countries = await listHeroSmsSmsCountries('tg')
    assert.equal(countries[0]?.english_name, 'Russia')
    assert.equal(countries[0]?.popularity, 12)
    const operators = await listHeroSmsSmsOperators(6)
    assert.deepEqual(operators, ['mts', 'tele2'])
    const offer = await getHeroSmsSmsOffer({
      country: 6,
      service: 'tg',
      operator: 'any',
      maxPriceUSD: '2.5',
    })
    assert.equal(offer.bid, true)
    assert.equal(offer.customer_price_usd, '2')
  })

  test('loads current orders separately from redacted history', async () => {
    const requests: Array<{ url: string; params: unknown }> = []
    api.get = (async (url: string, config?: { params?: unknown }) => {
      requests.push({ url, params: config?.params })
      if (url.endsWith('/current-list')) {
        return {
          data: {
            success: true,
            data: { items: [{ id: 'current-1', status: 'active' }] },
          },
        }
      }
      return {
        data: {
          success: true,
          data: { items: [], total: 0, page: 1, size: 50 },
        },
      }
    }) as typeof api.get

    const current = await listCurrentHeroSmsSmsOrders()
    const history = await listHeroSmsSmsOrders(1, 50)
    assert.equal(current[0]?.id, 'current-1')
    assert.equal(history.total, 0)
    assert.deepEqual(requests, [
      {
        url: '/api/hero-sms/sms/orders/current-list',
        params: undefined,
      },
      {
        url: '/api/hero-sms/sms/orders',
        params: { page: 1, size: 50, summary: true },
      },
    ])
  })

  test('purchases with idempotency and cancels by order id', async () => {
    const calls: Array<{ url: string; body: unknown; config: unknown }> = []
    api.post = (async (url: string, body: unknown, config?: unknown) => {
      calls.push({ url, body, config })
      return {
        data: {
          success: true,
          data: {
            order: { id: 'hssms_1', status: 'active' },
            quota: 500_000,
          },
        },
      }
    }) as typeof api.post

    await createHeroSmsSmsOrder('hssq_quote', 'sms-batch-1-item-1')
    await cancelHeroSmsSmsOrder('hssms_1')

    const purchaseCall = calls[0]
    assert.ok(purchaseCall)
    assert.equal(purchaseCall.url, '/api/hero-sms/sms/orders')
    assert.deepEqual(purchaseCall.body, { offer_id: 'hssq_quote' })
    const headers = (
      purchaseCall.config as {
        headers?: Record<string, string>
      }
    ).headers
    assert.equal(headers?.['Idempotency-Key'], 'sms-batch-1-item-1')
    assert.equal(calls[1]?.url, '/api/hero-sms/sms/orders/hssms_1/cancel')
  })
})
