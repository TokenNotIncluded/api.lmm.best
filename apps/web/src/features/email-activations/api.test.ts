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
  getCurrentHeroSmsActivation,
  getHeroSmsActivationDetail,
  listHeroSmsActivations,
  listHeroSmsProducts,
  reorderHeroSmsActivation,
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
                count: 4,
                available: true,
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
    assert.equal(response.items[0]?.count, 4)
    assert.equal(response.items[0]?.available, true)
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
              items: [
                {
                  id: 1,
                  order_id: 9,
                  email: 'pending@example.com',
                  status: 'active',
                  charge_quota: 15,
                  cost_usd: 1.5,
                  created_at: 1_776_988_800,
                  updated_at: 1_776_988_860,
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
            id: 1,
            order_id: 9,
            domain_id: 'opaque-domain-quote',
            email: 'pending@example.com',
            code: '4321',
            status: 'completed',
            charge_quota: 15,
            cost_usd: 1.5,
            currency: 'USD',
            currency_code: 840,
            cancel_reason: '',
            created_at: '2026-08-22T00:00:00Z',
            updated_at: '2026-08-22T00:02:00Z',
          },
        },
      }
    }) as typeof api.get

    const list = await listHeroSmsActivations({ page: 1, size: 10 })
    const detail = await getHeroSmsActivationDetail(1)

    assert.equal(list.items[0]?.status, 'active')
    assert.match(list.items[0]?.created_at ?? '', /^2026-/)
    assert.equal(detail.activation.code, '4321')
    assert.equal(detail.activation.currency_code, 840)
    assert.equal(detail.activation.domain_id, 'opaque-domain-quote')
  })

  test('loads the current activation independently from history filters', async () => {
    api.get = (async (url: string) => {
      assert.equal(url, '/api/hero-sms/email/activations/current')
      return {
        data: {
          success: true,
          data: {
            id: 'current-1',
            order_id: 'order-1',
            domain_id: 'quote-1',
            email: 'current@mail.test',
            status: 'active',
            charge_quota: 10,
            cost_usd: '0.1',
            currency: 'USD',
            currency_code: 840,
            created_at: 1_776_988_800,
            updated_at: 1_776_988_860,
          },
        },
      }
    }) as typeof api.get

    const current = await getCurrentHeroSmsActivation()
    assert.equal(current?.id, 'current-1')
    assert.equal(current?.currency_code, 840)
  })

  test('reorders with the confirmed quote token and stable idempotency key', async () => {
    let receivedBody: unknown
    let receivedHeaders: Record<string, string> | undefined

    api.post = (async (url: string, body: unknown, config?: unknown) => {
      assert.equal(url, '/api/hero-sms/email/activations/act-1/reorder')
      receivedBody = body
      receivedHeaders = (config as { headers?: Record<string, string> }).headers
      return {
        data: {
          success: true,
          data: { order: { id: 'order-2', status: 'completed' }, activations: [] },
        },
      }
    }) as typeof api.post

    await reorderHeroSmsActivation({
      activationId: 'act-1',
      domain_id: 'opaque-quote-token',
      idempotencyKey: 'reorder-idem',
    })

    assert.deepEqual(receivedBody, { domain_id: 'opaque-quote-token' })
    assert.equal(receivedHeaders?.['Idempotency-Key'], 'reorder-idem')
  })
})
