/*
Copyright (C) 2026 LIghtJUNction

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
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  validateDiscountCode,
  calculateAmount,
  calculateStripeAmount,
  calculateWaffoAmount,
  calculateWaffoPancakeAmount,
  calculateCreditAmount,
  calculateCreditStripeAmount,
  calculateCreditWaffoAmount,
  calculateCreditPancakeAmount,
  validateCreditDiscountCode,
  requestCreditPayment,
  requestCreditStripePayment,
  requestCreditWaffoPayment,
  requestCreditPancakePayment,
  requestPayment,
  requestStripePayment,
  requestCreemPayment,
  requestWaffoPayment,
  requestWaffoPancakePayment,
  getAllBillingHistory,
  getUserBillingHistory,
  sendAffiliateInvitation,
} from './api'
import { prepareTopup, readPendingTopups } from './lib/topup-cloud-storage'

const dom = new Window({ url: 'https://example.test/wallet' })
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom })
after(() => dom.close())

const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  dom.localStorage.clear()
  useAuthStore.getState().auth.reset()
})

test('billing history APIs send the global sort contract to user and admin routes', async () => {
  const capturedUrls: string[] = []
  api.get = (async (url: string) => {
    capturedUrls.push(url)
    return { data: { success: true, data: { items: [], total: 0 } } }
  }) as typeof api.get

  await getUserBillingHistory(2, 25, 'order 42', 'money', 'asc')
  await getAllBillingHistory(3, 50, '', 'payment_method', 'desc')

  assert.equal(
    capturedUrls[0],
    '/api/user/topup/self?p=2&page_size=25&sort_by=money&sort_order=asc&keyword=order+42'
  )
  assert.equal(
    capturedUrls[1],
    '/api/user/topup?p=3&page_size=50&sort_by=payment_method&sort_order=desc'
  )
})

test('sendAffiliateInvitation posts only the recipient to the SMTP-backed route', async () => {
  let capturedUrl = ''
  let capturedBody: unknown
  let capturedConfig: Record<string, unknown> | undefined

  api.post = (async (
    url: string,
    body: unknown,
    config?: Record<string, unknown>
  ) => {
    capturedUrl = url
    capturedBody = body
    capturedConfig = config
    return { data: { success: true, message: 'sent' } }
  }) as typeof api.post

  const response = await sendAffiliateInvitation({
    email: 'friend@example.com',
  })

  assert.deepEqual(response, { success: true, message: 'sent' })
  assert.equal(capturedUrl, '/api/user/aff/invite')
  assert.deepEqual(capturedBody, { email: 'friend@example.com' })
  assert.equal(capturedConfig?.skipBusinessError, true)
})

test('canonical money gateways and Creem bind the server order before returning to redirect hooks', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'test', role: 1 })
  const calls = [
    () => requestCreditPayment({ amount: 1, payment_method: 'alipay' }),
    () => requestCreditStripePayment({ amount: 1, payment_method: 'stripe' }),
    () =>
      requestCreemPayment({
        product_id: 'test-product',
        payment_method: 'creem',
      }),
    () => requestCreditWaffoPayment({ amount: 1 }),
    () => requestCreditPancakePayment({ amount: 1 }),
  ]
  for (const [index, invoke] of calls.entries()) {
    const intent = prepareTopup(7, 0, 1)
    const order = `gateway-order-${index}`
    api.post = (async () => ({
      data: { success: true, data: { trade_no: order, ...grantFields(1) } },
    })) as typeof api.post
    await invoke()
    assert.equal(
      readPendingTopups(7).find((x) => x.attemptId === intent.attemptId)
        ?.tradeNo,
      order
    )
  }
})

test('a late gateway response cannot bind a receipt after account switch', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'first', role: 1 })
  prepareTopup(7, 0, 1)
  let resolve!: (response: {
    data: { success: boolean; trade_no: string } & ReturnType<
      typeof grantFields
    >
  }) => void
  const delayed = new Promise<{
    data: { success: boolean; trade_no: string } & ReturnType<
      typeof grantFields
    >
  }>((accept) => {
    resolve = accept
  })
  api.post = (() => delayed) as typeof api.post
  const response = requestCreditPayment({ amount: 1, payment_method: 'alipay' })
  useAuthStore.getState().auth.setUser({ id: 8, username: 'second', role: 1 })
  resolve({
    data: { success: true, trade_no: 'first-users-order', ...grantFields(1) },
  })
  await response
  assert.equal(readPendingTopups(7)[0]?.tradeNo, undefined)
  assert.deepEqual(readPendingTopups(8), [])
})

test('quote and checkout keep fractional legacy batches explicit, with the payment ISO unchanged', async () => {
  const captured: Array<{ url: string; body: Record<string, unknown> }> = []
  api.post = (async (url: string, body: Record<string, unknown>) => {
    captured.push({ url, body })
    return {
      data: {
        success: true,
        data: '0.01',
        ...grantFields(Number(body.amount)),
      },
    }
  }) as typeof api.post
  const amount = 0.000002 // Exactly one raw Credit at QPU=500000.
  await calculateAmount({ amount, payment_method: 'alipay' })
  await calculateStripeAmount({ amount })
  await calculateWaffoAmount({ amount })
  await calculateWaffoPancakeAmount({ amount })
  await requestPayment({ amount, payment_method: 'alipay' })
  await requestStripePayment({ amount, payment_method: 'stripe' })
  await requestWaffoPayment({ amount })
  await requestWaffoPancakePayment({
    amount,
    settlement_amount: '0.01',
    settlement_currency: 'USD',
  })
  assert.equal(captured.length, 8)
  for (const call of captured) {
    assert.equal(call.body.amount, 0.000002, call.url)
    assert.equal(call.body.amount_unit, 'LEGACY', call.url)
  }
  assert.equal(captured.at(-1)?.body.settlement_amount, '0.01')
  assert.equal(captured.at(-1)?.body.settlement_currency, 'USD')
})

test('discount validation uses the same fractional batch unit as quote and checkout', async () => {
  let body: unknown
  api.post = (async (_url: string, request: unknown) => {
    body = request
    return {
      data: {
        success: true,
        data: { code: 'SAVE', discount_percent: 10, min_amount: 3500000 },
      },
    }
  }) as typeof api.post
  await validateDiscountCode({
    code: 'SAVE',
    amount: 7.3,
    payment_method: 'card',
  })
  assert.deepEqual(body, {
    code: 'SAVE',
    amount: 7.3,
    payment_method: 'card',
    amount_unit: 'LEGACY',
  })
})

function grantFields(amount: number) {
  return {
    credit_unit_schema_version: 2,
    quota_unit: 'LEDGER_QUOTA',
    legacy_credit_unit: 'LEDGER_QUOTA',
    public_credit_unit: 'CREDIT',
    ledger_quota_per_usd: 5,
    ledger_quota_per_usd_exact: '5',
    public_credits_per_usd: 5,
    public_credits_per_usd_exact: '5',
    credited_quota: amount,
    credit_amount: amount,
    credit_amount_unit: 'LEDGER_QUOTA',
    public_credit_metadata_version: 2,
    public_credit_amount_unit: 'CREDIT',
    public_credit_amount: String(amount),
  }
}

const creditRoutes = [
  {
    path: '/api/user/topup/currency/v2/amount',
    invoke: (amount: number) =>
      calculateCreditAmount({ amount, payment_method: 'alipay' }),
  },
  {
    path: '/api/user/topup/currency/v2/stripe/amount',
    invoke: (amount: number) => calculateCreditStripeAmount({ amount }),
  },
  {
    path: '/api/user/topup/currency/v2/waffo/amount',
    invoke: (amount: number) => calculateCreditWaffoAmount({ amount }),
  },
  {
    path: '/api/user/topup/currency/v2/waffo-pancake/amount',
    invoke: (amount: number) => calculateCreditPancakeAmount({ amount }),
  },
  {
    path: '/api/user/topup/currency/v2/discount-code/validate',
    invoke: (amount: number) =>
      validateCreditDiscountCode({
        amount,
        code: 'SAVE',
        payment_method: 'card',
      }),
  },
  {
    path: '/api/user/topup/currency/v2/pay',
    invoke: (amount: number) =>
      requestCreditPayment({ amount, payment_method: 'alipay' }),
  },
  {
    path: '/api/user/topup/currency/v2/stripe/pay',
    invoke: (amount: number) =>
      requestCreditStripePayment({ amount, payment_method: 'stripe' }),
  },
  {
    path: '/api/user/topup/currency/v2/waffo/pay',
    invoke: (amount: number) => requestCreditWaffoPayment({ amount }),
  },
  {
    path: '/api/user/topup/currency/v2/waffo-pancake/pay',
    invoke: (amount: number) => requestCreditPancakePayment({ amount }),
  },
] as const

for (const amount of [
  1,
  4503599627370497,
  9007199254740987,
  Number.MAX_SAFE_INTEGER,
]) {
  test(`all nine CREDIT routes preserve raw ${amount} as an exact JSON integer`, async () => {
    const captured: Array<{
      url: string
      body: Record<string, unknown>
      wire: string
    }> = []
    api.post = (async (url: string, body: Record<string, unknown>) => {
      const wire = JSON.stringify(body)
      captured.push({ url, body: JSON.parse(wire), wire })
      return {
        data: {
          success: true,
          data: '0.01',
          ...grantFields(Number(body.amount)),
        },
      }
    }) as typeof api.post
    for (const route of creditRoutes) await route.invoke(amount)

    assert.deepEqual(
      captured.map((call) => call.url),
      creditRoutes.map((route) => route.path)
    )
    for (const call of captured) {
      assert.equal(typeof call.body.amount, 'number', call.url)
      assert.equal(call.body.amount, amount, call.url)
      assert.ok(Number.isSafeInteger(call.body.amount), call.url)
      assert.equal(call.body.amount_unit, 'LEDGER_QUOTA', call.url)
      assert.equal(call.body.credit_metadata_version, 2, call.url)
      assert.match(call.wire, new RegExp(`"amount":${amount}(?:,|})`), call.url)
    }
    assert.equal(captured[0].body.payment_method, 'alipay')
    assert.deepEqual(captured[4].body, {
      amount,
      code: 'SAVE',
      payment_method: 'card',
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
    })
    assert.equal(captured[5].body.payment_method, 'alipay')
    assert.equal(captured[6].body.payment_method, 'stripe')
  })
}

test('all nine CREDIT routes reject nonpositive, fractional and unsafe amounts before HTTP', async () => {
  let calls = 0
  api.post = (async () => {
    calls++
    throw new Error('Invalid amount reached HTTP')
  }) as typeof api.post
  for (const route of creditRoutes) {
    for (const amount of [
      0,
      -1,
      0.000002,
      1.5,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.NEGATIVE_INFINITY,
      Number.MAX_SAFE_INTEGER + 1,
      Number.MAX_VALUE,
    ]) {
      await assert.rejects(
        () => route.invoke(amount),
        /Invalid top-up amount/,
        route.path
      )
    }
  }
  assert.equal(calls, 0)
})

test('a missing CREDIT route surfaces HTTP 404 without falling back to a legacy money route', async () => {
  const captured: string[] = []
  const notFound = { isAxiosError: true, response: { status: 404 } }
  api.post = (async (url: string) => {
    captured.push(url)
    throw notFound
  }) as typeof api.post
  for (const route of creditRoutes) {
    await assert.rejects(
      () => route.invoke(1),
      (error) => error === notFound,
      route.path
    )
  }
  assert.deepEqual(
    captured,
    creditRoutes.map((route) => route.path)
  )
})

test('only explicit v2 CREDIT requests use the public denomination and captured basis', async () => {
  const captured: unknown[] = []
  api.post = (async (_url: string, body: unknown) => {
    captured.push(body)
    return {
      data: {
        success: true,
        data: '7.00',
        ...grantFields(10),
        public_credits_per_usd: 2,
        public_credits_per_usd_exact: '2',
        public_credit_amount: '4',
      },
    }
  }) as typeof api.post
  await calculateCreditAmount({
    amount: 4,
    amount_unit: 'CREDIT',
    credit_metadata_version: 2,
    expected_public_credits_per_usd_exact: '2',
    payment_method: 'alipay',
  })
  assert.deepEqual(
    [...captured],
    [
      {
        amount: 4,
        amount_unit: 'CREDIT',
        credit_metadata_version: 2,
        expected_public_credits_per_usd_exact: '2',
        payment_method: 'alipay',
      },
    ]
  )

  api.post = (async (_url: string, body: unknown) => {
    captured.push(body)
    return { data: { success: true, data: '7.00', ...grantFields(4) } }
  }) as typeof api.post
  await calculateCreditAmount({ amount: 4, amount_unit: 'CREDIT' })
  assert.deepEqual(captured[1], {
    amount: 4,
    amount_unit: 'LEDGER_QUOTA',
    credit_metadata_version: 2,
  })
})

test('unknown and partial request denomination metadata fails before HTTP', async () => {
  let calls = 0
  api.post = (async () => {
    calls++
    throw new Error('Invalid metadata reached HTTP')
  }) as typeof api.post
  for (const metadata of [
    { amount_unit: 'LEDGER_QUOTA' },
    { credit_metadata_version: 1, amount_unit: 'CREDIT' },
    { credit_metadata_version: 3, amount_unit: 'CREDIT' },
    { credit_metadata_version: 2 },
    { credit_metadata_version: 2, amount_unit: 'UNKNOWN' },
    { credit_metadata_version: 2, amount_unit: 'CREDIT' },
    {
      credit_metadata_version: 2,
      amount_unit: 'CREDIT',
      expected_public_credits_per_usd_exact: 2,
    },
    {
      credit_metadata_version: 2,
      amount_unit: 'CREDIT',
      expected_public_credits_per_usd_exact: '02',
    },
    {
      credit_metadata_version: 2,
      amount_unit: 'CREDIT',
      expected_public_credits_per_usd_exact: '9007199254740992',
    },
    { amount_unit: 'CREDIT', expected_public_credits_per_usd_exact: '2' },
    {
      credit_metadata_version: 2,
      amount_unit: 'LEDGER_QUOTA',
      expected_public_credits_per_usd_exact: '2',
    },
  ]) {
    await assert.rejects(
      () => calculateCreditAmount({ amount: 1, ...metadata } as never),
      /Invalid top-up credit metadata/
    )
  }
  assert.equal(calls, 0)
})

test('partial or inconsistent successful grant metadata cannot bind a payment receipt', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'test', role: 1 })
  for (const changed of [
    { public_credit_metadata_version: undefined },
    { ledger_quota_per_usd_exact: undefined },
    { public_credit_amount: '2' },
    { credit_amount_unit: 'CREDIT' },
    { credited_quota: 2, credit_amount: 2, public_credit_amount: '2' },
  ]) {
    const intent = prepareTopup(7, 0, 1)
    api.post = (async () => ({
      data: {
        success: true,
        data: { trade_no: 'invalid-grant', ...grantFields(1), ...changed },
      },
    })) as typeof api.post
    await assert.rejects(
      () => requestCreditStripePayment({ amount: 1, payment_method: 'stripe' }),
      /Top-up credit metadata unavailable/
    )
    assert.equal(
      readPendingTopups(7).find((entry) => entry.attemptId === intent.attemptId)
        ?.tradeNo,
      undefined
    )
  }
})

test('explicit public input rejects a changed basis or unrelated ledger grant', async () => {
  for (const changed of [
    {},
    {
      public_credits_per_usd: 2,
      public_credits_per_usd_exact: '2',
      public_credit_amount: '0.4',
    },
  ]) {
    api.post = (async () => ({
      data: { success: true, data: '7.00', ...grantFields(1), ...changed },
    })) as typeof api.post
    await assert.rejects(
      () =>
        calculateCreditAmount({
          amount: 4,
          amount_unit: 'CREDIT',
          credit_metadata_version: 2,
          expected_public_credits_per_usd_exact: '2',
        }),
      /Top-up credit metadata unavailable/
    )
  }
})

for (const currency of ['CNY', 'USD'] as const) {
  test(`canonical Pancake checkout preserves the quoted ${currency} decimal and binds its order`, async () => {
    useAuthStore.getState().auth.setUser({ id: 7, username: 'test', role: 1 })
    const intent = prepareTopup(7, 0, 1)
    const captured: Array<{ url: string; body: unknown }> = []
    const quoteResponse = {
      success: true,
      data: '0.0100',
      settlement_currency: currency,
      original_settlement_amount: '0.0200',
      savings_settlement_amount: '0.0100',
      settlement_quote: {
        schema_version: 1,
        currency,
        original_amount: '0.0200',
        paid_amount: '0.0100',
        savings_amount: '0.0100',
        discount_percent: '50',
        basis: 'amount_preset_and_code',
      },
      ...grantFields(1),
    }
    api.post = (async (url: string, body: unknown) => {
      captured.push({ url, body })
      return {
        data: url.endsWith('/amount')
          ? quoteResponse
          : {
              success: true,
              data: { trade_no: `credit-${currency}-order`, ...grantFields(1) },
            },
      }
    }) as typeof api.post
    const quote = await calculateCreditPancakeAmount({
      amount: 1,
      discount_code: 'SAVE',
    })
    assert.deepEqual(quote, quoteResponse)
    await requestCreditPancakePayment({
      amount: 1,
      discount_code: 'SAVE',
      settlement_amount: quote.data,
      settlement_currency: currency,
    })
    assert.deepEqual(captured, [
      {
        url: '/api/user/topup/currency/v2/waffo-pancake/amount',
        body: {
          amount: 1,
          discount_code: 'SAVE',
          amount_unit: 'LEDGER_QUOTA',
          credit_metadata_version: 2,
        },
      },
      {
        url: '/api/user/topup/currency/v2/waffo-pancake/pay',
        body: {
          amount: 1,
          discount_code: 'SAVE',
          settlement_amount: '0.0100',
          settlement_currency: currency,
          amount_unit: 'LEDGER_QUOTA',
          credit_metadata_version: 2,
        },
      },
    ])
    assert.equal(
      readPendingTopups(7).find((entry) => entry.attemptId === intent.attemptId)
        ?.tradeNo,
      `credit-${currency}-order`
    )
  })
}
