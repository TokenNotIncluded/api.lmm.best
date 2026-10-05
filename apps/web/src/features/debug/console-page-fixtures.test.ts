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
import { test } from 'node:test'

import { AxiosHeaders, type InternalAxiosRequestConfig } from 'axios'
import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'http://127.0.0.1:4174/' })
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: domWindow,
})
const { consolePageFixture, withConsolePageFixtures } =
  await import('./console-page-fixtures')
const config = (url: string, method = 'get') =>
  ({ url, method, headers: new AxiosHeaders() }) as InternalAxiosRequestConfig

test('review fixtures never handle writes, unknown paths, or remote requests', async () => {
  assert.equal(consolePageFixture(config('/api/token/', 'post')), undefined)
  assert.equal(
    consolePageFixture(config('https://example.invalid/api/token/')),
    undefined
  )
  assert.equal(
    consolePageFixture(config('/api/not-in-the-explicit-catalog')),
    undefined
  )
  let passed = 0
  const wrapped = withConsolePageFixtures(async (request) => {
    passed += 1
    throw new Error(`blocked:${request.url}`)
  })
  await assert.rejects(
    wrapped(config('/api/hero-sms/sms/orders', 'post')),
    /blocked/
  )
  assert.equal(passed, 1)
  const response = await wrapped(config('/api/token/'))
  assert.equal(response.status, 200)
  assert.equal(passed, 1)
})

test('empty read responses are cloned so one page cannot mutate another fixture', () => {
  const first = consolePageFixture(config('/api/user/oauth/bindings')) as {
    data: unknown[]
  }
  first.data.push('local mutation')
  const second = consolePageFixture(config('/api/user/oauth/bindings')) as {
    data: unknown[]
  }
  assert.deepEqual(second.data, [])
})

test('admin list fixtures keep the API array contract rather than a paginated envelope', () => {
  for (const url of [
    '/api/red-packet/admin',
    '/api/security/admin/review-runs',
    '/api/assistant/admin/registration-events',
  ]) {
    const response = consolePageFixture(config(url)) as { data: unknown }
    assert.ok(Array.isArray(response.data), url)
  }
})

test('assistant model reads are explicit, cloned fixtures and never authorize writes', () => {
  const first = consolePageFixture(
    config('/api/assistant/models?group=default')
  ) as { data: string[] }
  assert.ok(Array.isArray(first.data))
  assert.ok(first.data.length > 0)
  assert.ok(first.data.every((model) => typeof model === 'string'))
  first.data.push('mutated-preview')
  const second = consolePageFixture(
    config('/api/assistant/models?group=default')
  ) as { data: string[] }
  assert.ok(!second.data.includes('mutated-preview'))
  assert.equal(
    consolePageFixture(config('/api/assistant/models', 'post')),
    undefined
  )
})

test('wallet review exposes versioned raw Credits and correct Alipay CNY limits without payment writes', async () => {
  const response = consolePageFixture(config('/api/user/topup/info')) as {
    data: {
      enable_online_topup: boolean
      payment_available: boolean
      amount_unit: string
      credit_metadata_available: boolean
      credit_metadata_version: number
      credit_amount_options: number[]
      credit_discount: Record<string, number>
      credit_min_topup: number
      stripe_credit_min_topup: number
      waffo_credit_min_topup: number
      pancake_credit_min_topup: number
      stripe_credit_max_topup: number | null
      waffo_credit_max_topup: number | null
      pancake_credit_max_topup: number | null
      pay_methods: Array<Record<string, unknown>>
    }
  }
  assert.equal(response.data.enable_online_topup, true)
  assert.equal(response.data.payment_available, true)
  assert.equal(response.data.pay_methods[0]?.type, 'alipay')
  assert.equal(response.data.amount_unit, 'LEGACY')
  assert.equal(response.data.credit_metadata_available, true)
  assert.equal(response.data.credit_metadata_version, 1)
  assert.deepEqual(
    response.data.credit_amount_options,
    [5000000, 25000000, 50000000, 100000000]
  )
  assert.deepEqual(response.data.credit_discount, {})
  assert.equal(response.data.credit_min_topup, 500000)
  assert.equal(response.data.stripe_credit_min_topup, 500000)
  assert.equal(response.data.waffo_credit_min_topup, 0)
  assert.equal(response.data.pancake_credit_min_topup, 0)
  assert.equal(response.data.stripe_credit_max_topup, 5000000000)
  assert.equal(response.data.waffo_credit_max_topup, null)
  assert.equal(response.data.pancake_credit_max_topup, null)
  assert.equal(response.data.pay_methods[0]?.min_topup_credit, 3500000)
  assert.equal(response.data.pay_methods[0]?.max_topup_credit, 350000000)
  assert.equal(response.data.pay_methods[0]?.min_topup_unit, 'USD')
  assert.equal(response.data.pay_methods[0]?.min_topup, 1)
  assert.equal(response.data.pay_methods[0]?.max_topup, '100')
  assert.equal(response.data.pay_methods[0]?.settlement_currency, 'CNY')
  assert.equal(response.data.pay_methods[0]?.platform_units_per_usd, 7)
  assert.equal(response.data.pay_methods[0]?.settlement_units_per_usd, 7)
  assert.equal(
    response.data.pay_methods[0]?.settlement_units_per_platform_unit,
    1
  )
  const wrapped = withConsolePageFixtures(async () => {
    throw new Error('blocked')
  })
  for (const path of [
    '/api/user/topup',
    '/api/user/pay',
    '/api/user/stripe/pay',
    '/api/user/topup/currency/pay',
    '/api/user/topup/currency/stripe/pay',
    '/api/user/topup/currency/waffo/pay',
    '/api/user/topup/currency/waffo-pancake/pay',
    '/api/subscription/balance/pay',
  ]) {
    await assert.rejects(wrapped(config(path, 'post')), /blocked/, path)
  }
})

test('usage review windows filter timestamps without duplicating model totals', () => {
  const stamp = 1790035200
  const first = config(
    `/api/data/self?start_timestamp=${stamp - 30 * 86400}&end_timestamp=${stamp - 3 * 86400}`
  )
  const second = config('/api/data/self')
  second.params = {
    start_timestamp: stamp - 3 * 86400 + 1,
    end_timestamp: stamp,
  }
  type UsageRow = {
    model_name: string
    token_used: number
    count: number
    quota: number
    created_at: number
  }
  const rows = [first, second].flatMap(
    (request) => (consolePageFixture(request) as { data: UsageRow[] }).data
  )
  assert.equal(rows.length, 7)
  assert.equal(new Set(rows.map((row) => row.created_at)).size, 7)
  assert.equal(
    rows.reduce((total, row) => total + row.token_used, 0),
    24780
  )
  assert.equal(
    rows.reduce((total, row) => total + row.count, 0),
    98
  )
  assert.ok(rows.every((row) => row.model_name && row.quota > 0))
})
