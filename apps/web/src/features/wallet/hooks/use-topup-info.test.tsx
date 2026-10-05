/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import {
  calculateSettlementAmount,
  getPaymentMaxTopup,
  getPaymentMaxTopupAmount,
  getPaymentMinTopupAmount,
  getPaymentSettlementMetadata,
  getPaymentTopupRatio,
} from '../lib/payment-unit'

const domWindow = new Window({ url: 'https://console.example.test/wallet' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { api } = await import('@/lib/api')
const { useTopupInfo } = await import('./use-topup-info')

const originalAPIAdapter = api.defaults.adapter
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

afterEach(() => {
  api.defaults.adapter = originalAPIAdapter
  domWindow.localStorage.clear()
})

after(() => domWindow.close())

async function loadTopupInfo(
  payMethods: unknown,
  extra: Record<string, unknown> = {}
) {
  api.defaults.adapter = async (config) => {
    assert.equal(config.url, '/api/user/topup/info')
    return {
      config,
      headers: {},
      status: 200,
      statusText: 'OK',
      data: {
        success: true,
        data: {
          credit_metadata_version: 1,
          credit_amount_options: [5000000, 10000000, 25000000],
          credit_discount: {},
          credit_min_topup: 500000,
          stripe_credit_min_topup: 5000000,
          waffo_credit_min_topup: 0,
          pancake_credit_min_topup: 0,
          enable_online_topup: true,
          enable_stripe_topup: true,
          min_topup: 1,
          stripe_min_topup: 10,
          amount_options: [10, 20, 50],
          discount: {},
          pay_methods: payMethods,
          ...extra,
        },
      },
    }
  }

  const state: { current?: ReturnType<typeof useTopupInfo> } = {}
  function Harness() {
    state.current = useTopupInfo()
    return null
  }

  const root = createRoot(document.createElement('div'))
  try {
    await act(async () => root.render(<Harness />))
    assert.ok(state.current)
    assert.equal(state.current.loading, false)
    assert.equal(state.current.error, null)
    assert.ok(state.current.topupInfo)
    return state.current.topupInfo
  } finally {
    await act(async () => root.unmount())
  }
}

test('preserves server currency, USD bridge rates and payment limits through the topup API', async () => {
  const topupInfo = await loadTopupInfo([
    {
      name: 'USD card',
      type: 'stripe',
      settlement_currency: 'USD',
      platform_units_per_usd: '6.8',
      settlement_units_per_usd: '1',
      max_topup: '2.5',
      max_topup_amount: '17',
    },
    {
      name: 'CNY payment',
      type: 'alipay',
      settlement_currency: 'CNY',
      platform_units_per_usd: 6.8,
      settlement_units_per_usd: 6.8,
      max_topup: 34,
      max_topup_amount: '231.2',
    },
  ])

  const [usd, cny] = topupInfo.pay_methods
  const usdMetadata = getPaymentSettlementMetadata(usd, true)
  const cnyMetadata = getPaymentSettlementMetadata(cny, true)
  assert.ok(usdMetadata)
  assert.ok(cnyMetadata)
  assert.equal(usdMetadata.currencyCode, 'USD')
  assert.equal(cnyMetadata.currencyCode, 'CNY')
  assert.equal(calculateSettlementAmount(6.8, usdMetadata), 1)
  assert.equal(calculateSettlementAmount(6.8, cnyMetadata), 6.8)
  assert.equal(getPaymentMaxTopup(usd), 2.5)
  assert.equal(getPaymentMaxTopupAmount(usd), 17)
  assert.equal(usd.max_topup_amount, '17')
  assert.equal(getPaymentMaxTopupAmount(cny), 231.2)
  assert.equal(getPaymentMaxTopup(cny), 34)
  assert.equal(usd.min_topup, 10)
})

test('preserves direct credit pricing and decimal strings from legacy JSON catalogs', async () => {
  const topupInfo = await loadTopupInfo(
    JSON.stringify([
      {
        name: 'LINUX DO Credit',
        type: 'epay',
        settlement_currency: 'LDC',
        settlement_units_per_platform_unit: '10.000000000000000001',
        topup_ratio: '0.5',
        max_topup: '20.5',
        private_gateway_key: 'must-not-reach-wallet',
      },
    ])
  )

  const [method] = topupInfo.pay_methods
  const metadata = getPaymentSettlementMetadata(method)
  assert.ok(metadata)
  assert.equal(metadata.currencyCode, 'LDC')
  assert.equal(calculateSettlementAmount(2, metadata), 20)
  assert.equal(
    method.settlement_units_per_platform_unit,
    '10.000000000000000001'
  )
  assert.equal(getPaymentTopupRatio(method), 0.5)
  assert.equal(getPaymentMaxTopup(method), 20.5)
  assert.equal(getPaymentMaxTopupAmount(method), null)
  assert.equal('private_gateway_key' in method, false)
})

test('keeps incomplete preferred rates visible to settlement validation instead of using legacy pricing', async () => {
  const topupInfo = await loadTopupInfo([
    null,
    { type: 'epay' },
    { name: 'Waffo', type: 'waffo' },
    {
      name: 'Incomplete contract',
      type: 'epay',
      settlement_currency: 'CNY',
      platform_units_per_usd: '6.8',
      settlement_unit: 'LDC',
      unit_price: '10',
    },
  ])

  assert.equal(topupInfo.pay_methods.length, 1)
  assert.equal(getPaymentSettlementMetadata(topupInfo.pay_methods[0]), null)
})

test('keeps raw catalogs exact even when legacy aliases cannot round-trip', async () => {
  const quotas = [1, 4503599627370497, 9007199254740987]
  const info = await loadTopupInfo([], {
    credit_amount_options: quotas,
    credit_discount: { 1: 0.9, 9007199254740987: 0.8 },
    amount_unit: 'LEGACY',
    legacy_amount_options: [0.0000033333333333333333, 9007199254.740993],
    legacy_discount: { 1: 0.5 },
  })
  assert.deepEqual(info.amount_options, quotas)
  assert.deepEqual(info.discount, { 1: 0.9, 9007199254740987: 0.8 })
  assert.equal(info.amount_unit, 'CREDIT')
})

test('missing or invalid raw metadata disables editable money without disabling fixed products', async () => {
  for (const extra of [
    { credit_metadata_version: undefined },
    { credit_metadata_available: false },
    { credit_amount_options: undefined },
    { credit_min_topup: Number.MAX_SAFE_INTEGER + 1 },
  ]) {
    const info = await loadTopupInfo([{ name: 'Card', type: 'card' }], {
      ...extra,
      enable_waffo_topup: true,
      enable_waffo_pancake_topup: true,
      enable_creem_topup: true,
      enable_redemption: true,
      creem_products: [
        {
          name: 'Fixed',
          productId: 'fixed',
          quota: 1,
          price: 1,
          currency: 'USD',
        },
      ],
    })
    assert.equal(info.enable_online_topup, false)
    assert.equal(info.enable_stripe_topup, false)
    assert.equal(info.enable_waffo_topup, false)
    assert.equal(info.enable_waffo_pancake_topup, false)
    assert.deepEqual(info.pay_methods, [])
    assert.deepEqual(info.amount_options, [])
    assert.equal(info.enable_creem_topup, true)
    assert.equal(info.creem_products?.[0].quota, 1)
    assert.equal(info.enable_redemption, true)
  }
})

test('uses authoritative legacy method limits when the compatibility catalog is in raw Credits', async () => {
  const info = await loadTopupInfo([
    {
      name: 'Card',
      type: 'card',
      min_topup: 1,
      min_topup_unit: 'USD',
      legacy_min_topup: '6.8',
      max_topup_amount: '8500000',
      max_topup_amount_unit: 'CREDIT',
      legacy_max_topup_amount: '17',
    },
  ])
  assert.equal(getPaymentMinTopupAmount(info.pay_methods[0]), 6.8)
  assert.equal(getPaymentMaxTopupAmount(info.pay_methods[0]), 17)
})
