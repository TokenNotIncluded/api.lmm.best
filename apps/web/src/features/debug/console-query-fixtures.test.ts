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
    '/api/user/topup/currency/pay',
    '/api/user/topup/currency/stripe/pay',
    '/api/user/topup/currency/waffo/pay',
    '/api/user/topup/currency/waffo-pancake/pay',
    '/api/user/amount',
    '/api/subscription/balance/pay',
    '/api/subscription/root/reset-targets',
    'https://example.invalid/api/pricing/runtime',
  ]) {
    await assert.rejects(wrapped(config(url, 'post')), /unmocked/, url)
  }
})

test('four synthetic raw-credit quote routes return native pricing ISO without payment URLs', async () => {
  const wrapped = withConsoleQueryFixtures(async () => {
    throw new Error('blocked')
  })
  for (const example of [
    {
      path: '/api/user/topup/currency/amount',
      currency: 'CNY',
      nativeAmount: '14',
      payment_method: 'alipay',
    },
    {
      path: '/api/user/topup/currency/stripe/amount',
      currency: 'USD',
      nativeAmount: '2',
      payment_method: undefined,
    },
    {
      path: '/api/user/topup/currency/waffo/amount',
      currency: 'USD',
      nativeAmount: '2',
      payment_method: undefined,
    },
    {
      path: '/api/user/topup/currency/waffo-pancake/amount',
      currency: 'USD',
      nativeAmount: '2',
      payment_method: undefined,
    },
  ]) {
    const request = config(example.path, 'post')
    request.data = JSON.stringify({
      amount: 7000000,
      amount_unit: 'CREDIT',
      ...(example.payment_method
        ? { payment_method: example.payment_method }
        : {}),
    })
    const response = await wrapped(request)
    assert.equal(response.status, 200)
    assert.deepEqual(response.data, {
      success: true,
      data: example.nativeAmount,
      settlement_currency: example.currency,
      credited_quota: 7000000,
      credit_amount: 7000000,
      amount_unit: 'CREDIT',
      legacy_batch_units: '14',
    })
    assert.equal('url' in response.data, false)
    assert.equal('checkout_url' in response.data, false)
    assert.equal('pay_link' in response.data, false)
  }
  const smallest = config(
    '/api/user/topup/currency/waffo-pancake/amount',
    'post'
  )
  smallest.data = { amount: 1, amount_unit: 'CREDIT' }
  assert.deepEqual((await wrapped(smallest)).data, {
    success: true,
    data: '0.000000285714285714285714285714',
    settlement_currency: 'USD',
    credited_quota: 1,
    credit_amount: 1,
    amount_unit: 'CREDIT',
    legacy_batch_units: '0.000002',
  })
})

test('raw-credit preview body guards reject invalid, legacy, remote and mutation requests', async () => {
  const wrapped = withConsoleQueryFixtures(async () => {
    throw new Error('blocked')
  })
  for (const body of [
    { amount: 0, amount_unit: 'CREDIT' },
    { amount: -1, amount_unit: 'CREDIT' },
    { amount: 1.5, amount_unit: 'CREDIT' },
    { amount: Number.POSITIVE_INFINITY, amount_unit: 'CREDIT' },
    { amount: 9007199254740992, amount_unit: 'CREDIT' },
    { amount: 3499999, amount_unit: 'CREDIT' },
    { amount: 350000001, amount_unit: 'CREDIT' },
    { amount: '7000000', amount_unit: 'CREDIT' },
    { amount: 7000000 },
    { amount: 7000000, amount_unit: 'LEGACY' },
    { amount: 7000000, amount_unit: 'USD' },
    { amount: 7000000, amount_unit: 'CREDIT', payment_method: 'stripe' },
    {
      amount: 7000000,
      amount_unit: 'CREDIT',
      discount_code: 'NOT-A-PREVIEW-CODE',
    },
    { amount: 7000000, amount_unit: 'CREDIT', create_order: true },
    '{broken-json',
    null,
    [],
  ]) {
    const request = config('/api/user/topup/currency/amount', 'post')
    request.data = body
    await assert.rejects(wrapped(request), /blocked/)
  }
  for (const path of [
    'https://example.invalid/api/user/topup/currency/amount',
    'http://user:pass@127.0.0.1:4174/api/user/topup/currency/amount',
    '/api/user/amount',
    '/api/user/topup/currency/unknown/amount',
    '/api/user/topup/currency/pay',
  ]) {
    const request = config(path, 'post')
    request.data = {
      amount: 7000000,
      amount_unit: 'CREDIT',
      payment_method: 'alipay',
    }
    await assert.rejects(wrapped(request), /blocked/, path)
  }
  const stripeAboveMax = config(
    '/api/user/topup/currency/stripe/amount',
    'post'
  )
  stripeAboveMax.data = { amount: 5000000001, amount_unit: 'CREDIT' }
  await assert.rejects(wrapped(stripeAboveMax), /blocked/)
  await assert.rejects(
    wrapped(config('/api/user/topup/currency/amount', 'get')),
    /blocked/
  )
})
