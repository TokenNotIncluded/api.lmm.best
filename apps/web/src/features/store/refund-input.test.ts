/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import {
  storeRefundAmountMax,
  storeRefundInput,
  storeRefundInteger,
  storeRefundModes,
  storeRefundQuantityMax,
} from './refund-input'
import type { StoreRefundMode, StoreRefundView } from './refund-types'

const view: StoreRefundView = {
  order_id: 'order-refund-fixture',
  product_title: 'Original product',
  variant_name: 'Original variant',
  payment_method: 'balance',
  currency: 'USD',
  principal_quota: 1500000,
  refunded_quota: 500000,
  reserved_quota: 250000,
  remaining_quota: 750000,
  quantity: 3,
  refunded_quantity: 1,
  max_quantity: 1,
  eligible_items: [
    { stock_id: '00000000-0000-0000-0000-000000000002', position: 2 },
    { stock_id: '00000000-0000-0000-0000-000000000003', position: 3 },
  ],
  supports_quantity: true,
  supports_amount: true,
  native_basis_verified: false,
  refunds: [],
}
const draft = (mode: StoreRefundMode) => ({
  mode,
  reason: '  Wrong specification  ',
  quantity: '1',
  amount: '750000',
  stockIds: [] as string[],
})

test('balance amount uses integer Credits and respects completed plus reserved refunds', () => {
  assert.equal(storeRefundAmountMax(view), 750000)
  assert.deepEqual(storeRefundInput(view, draft('amount'), 'same-key'), {
    request_key: 'same-key',
    mode: 'amount',
    reason: 'Wrong specification',
    amount_quota: 750000,
  })
  for (const amount of [
    '750001',
    '0',
    '1.5',
    '1e3',
    '-1',
    '9007199254740992',
  ]) {
    assert.equal(
      storeRefundInput(view, { ...draft('amount'), amount }, 'same-key'),
      undefined,
      amount
    )
  }
})

test('quantity refund accepts exact eligible card identities and rejects already refunded or duplicate items', () => {
  const quantityView = {
    ...view,
    reserved_quota: 0,
    remaining_quota: 1000000,
    max_quantity: 2,
  }
  const ids = view.eligible_items.map((item) => item.stock_id)
  assert.equal(storeRefundQuantityMax(quantityView), 2)
  assert.deepEqual(
    storeRefundInput(
      quantityView,
      { ...draft('quantity'), quantity: '2', stockIds: ids },
      'same-key'
    ),
    {
      request_key: 'same-key',
      mode: 'quantity',
      reason: 'Wrong specification',
      quantity: 2,
      stock_ids: ids,
    }
  )
  for (const input of [
    { quantity: '3', stockIds: [] },
    { quantity: '2', stockIds: [ids[0], ids[0]] },
    { quantity: '1', stockIds: ['already-refunded-item'] },
    { quantity: '1', stockIds: ids },
  ]) {
    assert.equal(
      storeRefundInput(
        quantityView,
        { ...draft('quantity'), ...input },
        'same-key'
      ),
      undefined
    )
  }
  assert.equal(storeRefundQuantityMax({ ...view, eligible_items: [] }), 0)
  assert.equal(storeRefundQuantityMax(view), 1)
  assert.equal(
    storeRefundQuantityMax({
      ...view,
      remaining_quota: 499999,
      max_quantity: 0,
    }),
    0
  )
  assert.equal(storeRefundQuantityMax({ ...view, max_quantity: Number.NaN }), 0)
})

test('external quote and provider flags alone cannot expose partial or guessed native refund amount', () => {
  const unverified: StoreRefundView = {
    ...view,
    payment_method: 'external:epay',
    amount_minor: 999999,
    remaining_amount_minor: 999999,
  }
  assert.deepEqual(storeRefundModes(unverified), ['full'])
  assert.equal(storeRefundAmountMax(unverified), 0)
  assert.equal(
    storeRefundInput(unverified, draft('amount'), 'same-key'),
    undefined
  )
  assert.equal(
    storeRefundInput(unverified, draft('quantity'), 'same-key'),
    undefined
  )
  assert.deepEqual(storeRefundInput(unverified, draft('full'), 'same-key'), {
    request_key: 'same-key',
    mode: 'full',
    reason: 'Wrong specification',
  })
})

test('verified native payment accepts ordinary original-currency money without current FX', () => {
  const native: StoreRefundView = {
    ...view,
    payment_method: 'platform:waffo_pancake',
    currency: 'CNY',
    native_basis_verified: true,
    amount_minor: 3000,
    refunded_amount_minor: 1000,
    remaining_amount_minor: 1500,
  }
  assert.deepEqual(storeRefundModes(native), ['full', 'quantity', 'amount'])
  assert.deepEqual(
    storeRefundInput(
      native,
      { ...draft('amount'), amount: '15.00' },
      'same-key'
    ),
    {
      request_key: 'same-key',
      mode: 'amount',
      reason: 'Wrong specification',
      amount_minor: 1500,
    }
  )
  assert.equal(
    storeRefundInput(
      native,
      { ...draft('amount'), amount: '15.01' },
      'same-key'
    ),
    undefined
  )
  assert.deepEqual(storeRefundModes({ ...native, currency: 'JPY' }), [
    'full',
    'quantity',
  ])
  assert.equal(
    storeRefundInput(
      { ...native, currency: 'JPY' },
      draft('amount'),
      'same-key'
    ),
    undefined
  )
  assert.deepEqual(
    storeRefundModes({
      ...native,
      supports_quantity: false,
      supports_amount: false,
    }),
    ['full']
  )
})

test('balance ordinary amounts reuse the wallet denomination and still submit only integer quota', () => {
  const config = {
    ...DEFAULT_CURRENCY_CONFIG,
    currencyUnit: 'credit' as const,
    creditsPerUsd: 500000,
    creditsPerUsdExact: '500000',
    cnyPerUsd: 6.8,
    cnyPerUsdExact: '6.8',
  }
  for (const [currency, amount] of [
    ['USD', '1.25'],
    ['CNY', '8.50'],
  ] as const) {
    const input = storeRefundInput(
      view,
      { ...draft('amount'), amount },
      'same-key',
      { currency, config }
    )
    assert.equal(input?.amount_quota, 625000)
    assert.equal(input?.amount_minor, undefined)
  }
  assert.equal(
    storeRefundInput(
      view,
      { ...draft('amount'), amount: '1.500002' },
      'same-key',
      { currency: 'USD', config }
    ),
    undefined
  )
  assert.equal(
    storeRefundInput(view, { ...draft('amount'), amount: '1e0' }, 'same-key', {
      currency: 'USD',
      config,
    }),
    undefined
  )
  assert.equal(
    storeRefundInput(view, { ...draft('amount'), amount: '1.5' }, 'same-key', {
      currency: 'CREDIT',
      config,
    }),
    undefined
  )
  assert.equal(
    storeRefundInput(
      view,
      { ...draft('amount'), amount: '500000' },
      'same-key',
      { currency: 'CREDIT', config }
    )?.amount_quota,
    500000
  )
  assert.equal(
    storeRefundInput(view, { ...draft('amount'), amount: '8.50' }, 'same-key', {
      currency: 'CNY',
      config: { ...config, cnyPerUsd: 0, cnyPerUsdExact: '' },
    }),
    undefined
  )
  assert.equal(
    storeRefundInput(
      view,
      { ...draft('amount'), amount: '0.000002' },
      'same-key',
      { currency: 'USD', config }
    )?.amount_quota,
    1
  )
})

test('exhausted or invalid cumulative totals cannot create another refund', () => {
  for (const remaining_quota of [
    0,
    -1,
    Number.NaN,
    1.5,
    Number.MAX_SAFE_INTEGER + 1,
  ]) {
    const exhausted = { ...view, remaining_quota }
    assert.deepEqual(storeRefundModes(exhausted), [])
    assert.equal(
      storeRefundInput(exhausted, draft('full'), 'same-key'),
      undefined
    )
  }
  assert.equal(
    storeRefundInput(view, { ...draft('full'), reason: '  ' }, 'same-key'),
    undefined
  )
  assert.equal(storeRefundInteger('01'), undefined)
})
