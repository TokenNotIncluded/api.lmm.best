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
  listHeroSmsSmsCountries,
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
        return { data: { success: true, data: [{ id: 6, name: '俄罗斯' }] } }
      }
      assert.equal(url, '/api/hero-sms/sms/offer')
      assert.deepEqual(config?.params, {
        country: 6,
        service: 'tg',
        operator: 'any',
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
          },
        },
      }
    }) as typeof api.get

    const countries = await listHeroSmsSmsCountries()
    assert.equal(countries[0]?.name, '俄罗斯')
    const offer = await getHeroSmsSmsOffer({
      country: 6,
      service: 'tg',
      operator: 'any',
    })
    assert.equal(offer.customer_price_usd, '2')
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

    await createHeroSmsSmsOrder('hssq_quote')
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
    assert.ok(headers?.['Idempotency-Key'])
    assert.equal(calls[1]?.url, '/api/hero-sms/sms/orders/hssms_1/cancel')
  })
})
