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

import {
  calculateCreditAmount,
  validateCreditDiscountCode,
} from '@/features/wallet/api'
import { parsePaymentDiscount } from '@/features/wallet/lib/payment-discount'
import { hasCompleteCreditGrant } from '@/features/wallet/lib/topup-credit-metadata'
import { api } from '@/lib/api'

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
    '/api/user/topup/currency/v2/pay',
    '/api/user/topup/currency/pay',
    '/api/user/topup/currency/v2/stripe/pay',
    '/api/user/topup/currency/v2/waffo/pay',
    '/api/user/topup/currency/v2/waffo-pancake/pay',
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
      path: '/api/user/topup/currency/v2/amount',
      currency: 'CNY',
      nativeAmount: '98.00',
      payment_method: 'alipay',
    },
    {
      path: '/api/user/topup/currency/v2/stripe/amount',
      currency: 'USD',
      nativeAmount: '14.00',
      payment_method: undefined,
    },
    {
      path: '/api/user/topup/currency/v2/waffo/amount',
      currency: 'USD',
      nativeAmount: '14.00',
      payment_method: undefined,
    },
    {
      path: '/api/user/topup/currency/v2/waffo-pancake/amount',
      currency: 'USD',
      nativeAmount: '14.00',
      payment_method: undefined,
    },
  ]) {
    const request = config(example.path, 'post')
    request.data = JSON.stringify({
      amount: 7000000,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
      ...(example.payment_method
        ? { payment_method: example.payment_method }
        : {}),
    })
    const response = await wrapped(request)
    assert.equal(response.status, 200)
    assert.ok(hasCompleteCreditGrant(response.data, 7000000))
    assert.equal(response.data.public_credit_amount, '7000000')
    const {
      success,
      data,
      settlement_currency,
      credited_quota,
      credit_amount,
      amount_unit,
      legacy_batch_units,
    } = response.data
    assert.deepEqual(
      {
        success,
        data,
        settlement_currency,
        credited_quota,
        credit_amount,
        amount_unit,
        legacy_batch_units,
      },
      {
        success: true,
        data: example.nativeAmount,
        settlement_currency: example.currency,
        credited_quota: 7000000,
        credit_amount: 7000000,
        amount_unit: 'LEDGER_QUOTA',
        legacy_batch_units: '14',
      }
    )
    assert.equal('url' in response.data, false)
    assert.equal('checkout_url' in response.data, false)
    assert.equal('pay_link' in response.data, false)
  }
  const smallest = config(
    '/api/user/topup/currency/v2/waffo-pancake/amount',
    'post'
  )
  smallest.data = {
    amount: 1,
    amount_unit: 'LEDGER_QUOTA',
    credit_metadata_version: 2,
  }
  const minimum = (await wrapped(smallest)).data
  assert.ok(hasCompleteCreditGrant(minimum, 1))
  assert.equal(minimum.data, '0.000002')
  assert.equal(minimum.settlement_currency, 'USD')
  assert.equal(minimum.credited_quota, 1)
  assert.equal(minimum.amount_unit, 'LEDGER_QUOTA')
  assert.equal(minimum.legacy_batch_units, '0.000002')
})

test('raw-credit preview body guards reject invalid, legacy, remote and mutation requests', async () => {
  const wrapped = withConsoleQueryFixtures(async () => {
    throw new Error('blocked')
  })
  for (const body of [
    { amount: 0, amount_unit: 'LEDGER_QUOTA', credit_metadata_version: 2 },
    { amount: -1, amount_unit: 'LEDGER_QUOTA', credit_metadata_version: 2 },
    { amount: 1.5, amount_unit: 'LEDGER_QUOTA', credit_metadata_version: 2 },
    {
      amount: Number.POSITIVE_INFINITY,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
    },
    {
      amount: 9007199254740992,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
    },
    {
      amount: 3499999,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
    },
    {
      amount: 350000001,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
    },
    {
      amount: '7000000',
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
    },
    { amount: 7000000 },
    { amount: 7000000, amount_unit: 'LEGACY' },
    { amount: 7000000, amount_unit: 'USD' },
    { amount: 7000000, amount_unit: 'CREDIT', credit_metadata_version: 2 },
    { amount: 7000000, amount_unit: 'LEDGER_QUOTA' },
    {
      amount: 7000000,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
      payment_method: 'stripe',
    },
    {
      amount: 7000000,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
      discount_code: 'NOT-A-PREVIEW-CODE',
    },
    {
      amount: 7000000,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
      create_order: true,
    },
    '{broken-json',
    null,
    [],
  ]) {
    const request = config('/api/user/topup/currency/v2/amount', 'post')
    request.data = body
    await assert.rejects(wrapped(request), /blocked/)
  }
  for (const path of [
    'https://example.invalid/api/user/topup/currency/v2/amount',
    'http://user:pass@127.0.0.1:4174/api/user/topup/currency/v2/amount',
    '/api/user/amount',
    '/api/user/topup/currency/v2/unknown/amount',
    '/api/user/topup/currency/v2/pay',
    '/api/user/topup/currency/pay',
  ]) {
    const request = config(path, 'post')
    request.data = {
      amount: 7000000,
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
      payment_method: 'alipay',
    }
    await assert.rejects(wrapped(request), /blocked/, path)
  }
  const stripeAboveMax = config(
    '/api/user/topup/currency/v2/stripe/amount',
    'post'
  )
  stripeAboveMax.data = {
    amount: 5000000001,
    amount_unit: 'LEDGER_QUOTA',
    credit_metadata_version: 2,
  }
  await assert.rejects(wrapped(stripeAboveMax), /blocked/)
  await assert.rejects(
    wrapped(config('/api/user/topup/currency/v2/amount', 'get')),
    /blocked/
  )
})

test('normal preview preset and coupon controls expose only synthetic same-ISO 700→630→504 quotes', async () => {
  const wrapped = withConsoleQueryFixtures(async () => {
    throw new Error('blocked')
  })
  const baseBody = {
    amount: 50000000,
    amount_unit: 'LEDGER_QUOTA',
    credit_metadata_version: 2,
    payment_method: 'alipay',
  }
  const request = config('/api/user/topup/currency/v2/amount', 'post')
  request.data = baseBody
  const preset = (await wrapped(request)).data
  assert.equal(preset.data, '630.00')
  assert.deepEqual(preset.settlement_quote, {
    schema_version: 1,
    currency: 'CNY',
    original_amount: '700.00',
    paid_amount: '630.00',
    savings_amount: '70.00',
    discount_percent: '10.00',
    basis: 'amount_preset_and_code',
  })
  assert.ok(hasCompleteCreditGrant(preset, 50000000))
  assert.ok(
    parsePaymentDiscount(
      preset.settlement_quote,
      preset.data,
      preset.settlement_currency
    )
  )
  const validation = config(
    '/api/user/topup/currency/v2/discount-code/validate',
    'post'
  )
  validation.data = {
    amount: 50000000,
    amount_unit: 'LEDGER_QUOTA',
    credit_metadata_version: 2,
    code: 'PREVIEW20',
    payment_method: 'alipay',
  }
  assert.deepEqual((await wrapped(validation)).data, {
    success: true,
    data: { code: 'PREVIEW20', discount_percent: 20, min_amount: 0 },
  })
  request.data = { ...baseBody, discount_code: 'PREVIEW20' }
  const combined = (await wrapped(request)).data
  assert.equal(combined.data, '504.00')
  assert.deepEqual(combined.settlement_quote, {
    schema_version: 1,
    currency: 'CNY',
    original_amount: '700.00',
    paid_amount: '504.00',
    savings_amount: '196.00',
    discount_percent: '28.00',
    basis: 'amount_preset_and_code',
  })
  assert.ok(hasCompleteCreditGrant(combined, 50000000))
  assert.ok(
    parsePaymentDiscount(
      combined.settlement_quote,
      combined.data,
      combined.settlement_currency
    )
  )
  for (const suffix of [
    'pay',
    'stripe/pay',
    'waffo/pay',
    'waffo-pancake/pay',
    'unknown/amount',
  ]) {
    const payment = config(`/api/user/topup/currency/v2/${suffix}`, 'post')
    payment.data = baseBody
    await assert.rejects(wrapped(payment), /blocked/)
  }
  validation.data = { ...validation.data, create_order: true }
  await assert.rejects(wrapped(validation), /blocked/)
})

test('the real coupon API accepts the local Alipay method and returns the matching combined quote', async () => {
  const originalAdapter = api.defaults.adapter
  api.defaults.adapter = withConsoleQueryFixtures(async () => {
    throw new Error('blocked')
  })
  try {
    const validated = await validateCreditDiscountCode({
      code: 'PREVIEW20',
      amount: 50000000,
      payment_method: 'alipay',
    })
    assert.deepEqual(validated, {
      success: true,
      data: { code: 'PREVIEW20', discount_percent: 20, min_amount: 0 },
    })
    assert.ok(validated.data)
    const quote = await calculateCreditAmount({
      amount: 50000000,
      payment_method: 'alipay',
      discount_code: validated.data.code,
    })
    assert.equal(quote.data, '504.00')
    assert.deepEqual(quote.settlement_quote, {
      schema_version: 1,
      currency: 'CNY',
      original_amount: '700.00',
      paid_amount: '504.00',
      savings_amount: '196.00',
      discount_percent: '28.00',
      basis: 'amount_preset_and_code',
    })
    for (const payment_method of ['stripe', 'wxpay', 'unknown']) {
      await assert.rejects(
        validateCreditDiscountCode({
          code: 'PREVIEW20',
          amount: 50000000,
          payment_method,
        }),
        /blocked/
      )
    }
  } finally {
    api.defaults.adapter = originalAdapter
  }
})
