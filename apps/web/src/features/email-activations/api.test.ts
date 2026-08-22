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
import { afterEach, describe, test } from 'node:test'

import { api } from '@/lib/api'

import {
  createHeroSmsActivations,
  getHeroSmsActivationDetail,
  listHeroSmsActivations,
  listHeroSmsProducts,
} from './api'

const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
})

describe('email activation api', () => {
  test('lists products with requested filters', async () => {
    let receivedConfig: unknown

    api.get = (async (url: string, config?: unknown) => {
      receivedConfig = config
      assert.equal(url, '/api/hero-sms/email/products')
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                id: 7,
                domain: 'mail.example',
                site: 'Example',
                cost_usd: 0.2,
                customer_price_usd: 1,
                charge_quota: 10,
                available: 4,
              },
            ],
            page: 2,
            size: 5,
            total: 9,
            price_multiplier: 10,
            currency: 'USD',
            currency_code: 840,
          },
        },
      }
    }) as typeof api.get

    const response = await listHeroSmsProducts({ page: 2, size: 5, site: 'Example' })

    assert.deepEqual((receivedConfig as { params: unknown }).params, {
      page: 2,
      size: 5,
      site: 'Example',
    })
    assert.equal(response.items[0]?.domain, 'mail.example')
    assert.equal(response.price_multiplier, 10)
  })

  test('creates activations with idempotency header', async () => {
    let receivedHeaders: Record<string, string> | undefined

    api.post = (async (_url: string, _data: unknown, config?: unknown) => {
      receivedHeaders = (config as { headers?: Record<string, string> }).headers
      return {
        data: {
          success: true,
          data: {
            order: { id: 'order-1', status: 'paid' },
            activations: [
              {
                id: 'act-1',
                order_id: 'order-1',
                email: 'hero@example.com',
                code: '123456',
                status: 'completed',
                charge_quota: 10,
                cost_usd: 1,
                created_at: '2026-08-22T00:00:00Z',
                updated_at: '2026-08-22T00:00:00Z',
              },
            ],
          },
        },
      }
    }) as typeof api.post

    const response = await createHeroSmsActivations({
      domain_id: 1,
      quantity: 2,
      idempotencyKey: 'idem-123',
    })

    assert.equal(receivedHeaders?.['Idempotency-Key'], 'idem-123')
    assert.equal(response.activations[0]?.email, 'hero@example.com')
    assert.equal(response.order?.id, 'order-1')
  })

  test('normalizes activation list and detail payloads', async () => {
    api.get = (async (url: string) => {
      if (url.endsWith('/api/hero-sms/email/activations')) {
        return {
          data: {
            success: true,
            data: {
              activations: [
                {
                  id: 1,
                  order_id: 9,
                  email: 'pending@example.com',
                  status: 'waiting_code',
                  charge_quota: 15,
                  cost_usd: 1.5,
                  created_at: '2026-08-22T00:00:00Z',
                  updated_at: '2026-08-22T00:01:00Z',
                  expires_at: '2026-08-22T00:10:00Z',
                },
              ],
              page: 1,
              size: 10,
              total: 1,
            },
          },
        }
      }

      return {
        data: {
          success: true,
          data: {
            activation: {
              id: 1,
              order_id: 9,
              email: 'pending@example.com',
              code: '4321',
              status: 'completed',
              charge_quota: 15,
              cost_usd: 1.5,
              created_at: '2026-08-22T00:00:00Z',
              updated_at: '2026-08-22T00:02:00Z',
            },
            order: { status: 'paid' },
          },
        },
      }
    }) as typeof api.get

    const list = await listHeroSmsActivations({ page: 1, size: 10 })
    const detail = await getHeroSmsActivationDetail(1)

    assert.equal(list.items[0]?.status, 'waiting_code')
    assert.equal(detail.activation.code, '4321')
    assert.equal(detail.order?.status, 'paid')
  })
})
