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

import { hasCompletePublicCreditCatalog } from '@/features/wallet/lib/topup-credit-metadata'

const domWindow = new Window({ url: 'http://127.0.0.1:4174/' })
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: domWindow,
})
const { consolePageFixture, withConsolePageFixtures } =
  await import('./console-page-fixtures')
const { useAuthStore } = await import('@/stores/auth-store')
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

test('store and security consumers receive the real controller success shapes', async () => {
  const { storeApi } = await import('@/features/store/api')
  const { marketAIReviewAPI } = await import('@/features/market-ai-review/api')
  const { listModerationAppeals } =
    await import('@/features/system-settings/security/security-audit-api')
  const { api } = await import('@/lib/api')
  const previousUser = useAuthStore.getState().auth.user
  const previousAdapter = api.defaults.adapter
  useAuthStore
    .getState()
    .auth.setUser({ id: 9001, username: 'review', role: 10 })
  api.defaults.adapter = withConsolePageFixtures(async () => {
    throw new Error('unmocked')
  })
  try {
    const store = await storeApi.config()
    const basis = store as typeof store & {
      credits_per_usd: number
      external_minimum_quota: number
    }
    assert.equal(store.fee_bps, 100)
    assert.equal(store.promotion_quota, 500000)
    assert.equal(store.minimum_unit_price_quota, 500000)
    assert.equal(basis.credits_per_usd, 500000)
    assert.equal(basis.external_minimum_quota, 5000000)
    assert.equal(store.linuxdo_units_per_usd, '')
    assert.equal(store.disclaimer_version, 'merchant-store-v1')
    assert.ok(store.disclaimer_text.length > 0)
    assert.deepEqual(store.product_link_presets, [])
    assert.deepEqual(store.platform_payment_methods, [
      {
        provider: 'balance',
        payment_type: 'balance',
        name: 'Platform balance',
        supported: true,
        configured: true,
      },
    ])
    assert.deepEqual(
      store.platform_payment_catalog,
      store.platform_payment_methods
    )
    const settings = await marketAIReviewAPI.settings()
    assert.equal(settings.tool_mode, 'off')
    assert.equal(settings.store_mode, 'off')
    assert.equal(settings.review_group, 'default')
    assert.equal(settings.review_model, 'omni-moderation-latest')
    assert.equal(settings.engine, 'openai_moderation')
    assert.deepEqual(settings.supported_inputs, ['text'])
    assert.equal(settings.categories.length, 13)
    assert.ok(settings.categories.includes('violence/graphic'))
    const appeals = await listModerationAppeals()
    assert.deepEqual(appeals, { success: true, message: '', data: [] })
    settings.categories.push('mutation')
    assert.equal((await marketAIReviewAPI.settings()).categories.length, 13)
  } finally {
    api.defaults.adapter = previousAdapter
    useAuthStore.getState().auth.setUser(previousUser)
  }
})

test('the three new reads keep exact paths, GET methods and admin permissions', async () => {
  const previousUser = useAuthStore.getState().auth.user
  const wrapped = withConsolePageFixtures(async () => {
    throw new Error('unmocked')
  })
  const paths = [
    '/api/store/config',
    '/api/security/market-ai-review/settings',
    '/api/security/admin/violation-fee-appeals',
  ]
  try {
    for (const role of [0, 1, 9]) {
      useAuthStore
        .getState()
        .auth.setUser({ id: 9002, username: 'review', role })
      assert.equal((await wrapped(config(paths[0]))).data.success, true)
      for (const path of paths.slice(1)) {
        await assert.rejects(wrapped(config(path)), /unmocked/, path)
      }
    }
    useAuthStore.getState().auth.setUser(null)
    for (const path of paths.slice(1)) {
      await assert.rejects(wrapped(config(path)), /unmocked/, path)
    }
    for (const role of [10, 100]) {
      useAuthStore
        .getState()
        .auth.setUser({ id: 9001, username: 'review', role })
      for (const path of paths) {
        assert.equal((await wrapped(config(path))).data.success, true)
        for (const method of ['post', 'put', 'patch', 'delete']) {
          await assert.rejects(wrapped(config(path, method)), /unmocked/, path)
        }
        for (const url of [
          `${path}/unlisted`,
          `https://example.invalid${path}`,
          `http://credential@127.0.0.1:4174${path}`,
        ]) {
          await assert.rejects(wrapped(config(url)), /unmocked/, url)
        }
      }
    }
    for (const path of [
      '/api/store/orders',
      '/api/store/guest/orders',
      '/api/store/orders/order-preview/pay',
      '/api/store/orders/order-preview/email',
      '/api/security/admin/violation-fee-appeals/1/approve',
      '/api/security/not-in-the-explicit-catalog',
    ]) {
      await assert.rejects(wrapped(config(path, 'post')), /unmocked/, path)
    }
  } finally {
    useAuthStore.getState().auth.setUser(previousUser)
  }
})

test('admin list fixtures keep the API array contract rather than a paginated envelope', () => {
  for (const url of [
    '/api/red-packet/admin',
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
  assert.deepEqual(response.data.credit_discount, { 50000000: 0.9 })
  assert.ok(hasCompletePublicCreditCatalog(response.data))
  assert.equal(response.data.credit_min_topup, 500000)
  assert.equal(response.data.stripe_credit_min_topup, 500000)
  assert.equal(response.data.waffo_credit_min_topup, 0)
  assert.equal(response.data.pancake_credit_min_topup, 0)
  assert.equal(response.data.stripe_credit_max_topup, 5000000000)
  assert.equal(response.data.waffo_credit_max_topup, null)
  assert.equal(response.data.pancake_credit_max_topup, null)
  assert.equal(response.data.pay_methods[0]?.min_topup_credit, '3500000')
  assert.equal(response.data.pay_methods[0]?.max_topup_credit, '350000000')
  assert.equal(response.data.pay_methods[0]?.min_topup_unit, 'USD')
  assert.equal(response.data.pay_methods[0]?.min_topup, 7)
  assert.equal(response.data.pay_methods[0]?.max_topup, '700')
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
    '/api/user/topup/currency/v2/pay',
    '/api/user/topup/currency/v2/stripe/pay',
    '/api/user/topup/currency/v2/waffo/pay',
    '/api/user/topup/currency/v2/waffo-pancake/pay',
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
