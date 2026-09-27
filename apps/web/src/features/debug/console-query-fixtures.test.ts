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

import { withConsoleQueryFixtures } from './console-query-fixtures'

const dom = new Window({ url: 'http://127.0.0.1:4174/' })
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom })
const config = (url: string, method: string) =>
  ({ url, method, headers: new AxiosHeaders() }) as InternalAxiosRequestConfig

test('only the named local read queries are handled; every mutation stays blocked', async () => {
  const wrapped = withConsoleQueryFixtures(async () => {
    throw new Error('unmocked')
  })
  const runtime = await wrapped(config('/api/pricing/runtime', 'post'))
  assert.deepEqual(runtime.data, { success: true, data: {} })
  const targets = await wrapped(
    config('/api/subscription/root/reset-targets', 'get')
  )
  assert.deepEqual(targets.data.data.items, [])
  for (const url of [
    '/api/subscription/root/reset',
    '/api/subscription/root/reset/preview',
    '/api/hero-sms/sms/orders',
    '/api/user/topup',
    '/api/user/pay',
    '/api/user/stripe/pay',
    '/api/user/waffo/pay',
    '/api/user/waffo-pancake/pay',
    '/api/subscription/balance/pay',
    '/api/subscription/root/reset-targets',
    'https://example.invalid/api/pricing/runtime',
  ]) {
    await assert.rejects(wrapped(config(url, 'post')), /unmocked/, url)
  }
})

test('the local quote reads the entered amount and refuses invalid or remote quote requests', async () => {
  const wrapped = withConsoleQueryFixtures(async () => {
    throw new Error('blocked')
  })
  for (const body of [
    { amount: 25, payment_method: 'alipay' },
    JSON.stringify({
      amount: 12.5,
      payment_method: 'alipay',
      discount_code: '',
    }),
  ]) {
    const request = config('/api/user/amount', 'post')
    request.data = body
    const response = await wrapped(request)
    assert.equal(response.status, 200)
    assert.deepEqual(response.data, {
      success: true,
      data: typeof body === 'string' ? '12.50' : '25.00',
      settlement_currency: 'USD',
    })
  }
  for (const body of [
    { amount: 0 },
    { amount: -1 },
    { amount: Number.POSITIVE_INFINITY },
    { amount: 10, payment_method: 'stripe' },
    { amount: 10, discount_code: 'NOT-A-PREVIEW-CODE' },
    { amount: 10, create_order: true },
    '{broken-json',
  ]) {
    const request = config('/api/user/amount', 'post')
    request.data = body
    await assert.rejects(wrapped(request), /blocked/)
  }
  const remote = config('https://example.invalid/api/user/amount', 'post')
  remote.data = { amount: 10, payment_method: 'alipay' }
  await assert.rejects(wrapped(remote), /blocked/)
  await assert.rejects(wrapped(config('/api/user/amount', 'get')), /blocked/)
})
