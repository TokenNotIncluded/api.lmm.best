/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

import { useAuthStore } from '@/stores/auth-store'

import { StoreCatalogueProductCard } from './catalogue-product-card'
import { StoreNativeAmount, StoreNativeProduct } from './native-projection'
import { nativePriceText } from './native-projection-values'
import { StoreOrderRow } from './orders-page'
import { StoreCheckout } from './product-page'
import { storeCheckoutCapacity } from './quantity'
import type { StoreOrder, StorePrice, StoreProduct } from './types'
import { storeDate } from './utils'
import { StoreProductPrice } from './variant-summary'

const product = {
  id: 'native',
  record_kind: 'native',
  title: 'Native product',
  price_quota: null,
  created_at: null,
  updated_at: null,
  trading_paused: true,
  payment_methods: [],
  variants: [
    {
      id: 'zero',
      name: 'Zero',
      price: { unit: 'credits', amount_credits: 0 },
      price_quota: 0,
    },
    {
      id: 'usd',
      name: 'USD variant',
      price: { amount_minor: 123, currency: 'USD' },
      price_quota: null,
    },
    {
      id: 'jpy',
      name: 'JPY variant',
      price: { amount_minor: 500, currency: 'JPY' },
      price_quota: null,
    },
  ],
} as unknown as StoreProduct // Deliberately partial native DTO: legacy metadata is absent.

function render(node: ReactNode) {
  return renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>{node}</QueryClientProvider>
  )
}

test('native exact units preserve zero, safe integer and currency minor amounts', () => {
  assert.equal(
    nativePriceText({ unit: 'credits', amount_credits: 0 }),
    '0 credits'
  )
  assert.equal(
    nativePriceText({
      unit: 'credits',
      amount_credits: Number.MAX_SAFE_INTEGER,
    }),
    '9007199254740991 credits'
  )
  for (const currency of ['USD', 'JPY', 'KWD', 'ZZZ']) {
    const text = render(
      <StoreNativeAmount price={{ amount_minor: 123, currency }} />
    )
    assert.match(text, new RegExp(`123 ${currency}.*minor units`))
    assert.doesNotMatch(text, /credits/)
  }
})

test('invalid or missing money stays unknown, never free or rounded', () => {
  for (const price of [
    null,
    undefined,
    {},
    { unit: 'credits', amount_credits: Number.NaN },
    { unit: 'credits', amount_credits: Number.MAX_SAFE_INTEGER + 1 },
    { amount_minor: -1, currency: 'USD' },
    { amount_minor: 1.5, currency: 'USD' },
    { amount_minor: 10, currency: 'usd' },
    { unit: 'other', amount_minor: 10, currency: 'USD' },
  ] as (StorePrice | null | undefined)[]) {
    assert.equal(nativePriceText(price), undefined)
    assert.match(render(<StoreNativeAmount price={price} />), /Amount unknown/)
  }
})

test('mixed native variants remain visible with their own units, not a credits range', () => {
  const markup = render(<StoreProductPrice product={product} />)
  for (const text of [
    'Zero',
    '0 credits',
    'USD variant',
    '123 USD',
    'JPY variant',
    '500 JPY',
  ]) {
    assert.ok(markup.includes(text), text)
  }
  assert.doesNotMatch(markup, /No variants/)
  const panel = render(<StoreNativeProduct product={product} />)
  assert.match(panel, /read-only/)
  assert.match(panel, /Time unknown/)
  assert.match(panel, /disabled/)
})

test('mixed native and legacy catalogue cards both remain visible', () => {
  const native = {
    ...product,
    seller_id: 2,
    image_urls: [],
    description: '',
    links: [],
  }
  const legacy = {
    ...native,
    id: 'legacy',
    title: 'Legacy product',
    record_kind: 'legacy' as const,
    variants: undefined,
    price_quota: 0,
  }
  const markup = render(
    <>
      <StoreCatalogueProductCard product={native} featured={false} list />
      <StoreCatalogueProductCard product={legacy} featured={false} list />
    </>
  )
  assert.match(markup, /Native product/)
  assert.match(markup, /Legacy product/)
  assert.match(markup, /123 USD.*minor units/)
  assert.match(markup, /read-only/)
  assert.match(markup, /0 credits/)
})

test('legacy zero quote is distinct from unknown; legacy capacity unchanged', () => {
  assert.match(
    render(
      <StoreProductPrice
        product={{
          ...product,
          record_kind: 'legacy',
          price_min_quota: 0,
          price_max_quota: 0,
        }}
      />
    ),
    /0/
  )
  assert.equal(
    storeCheckoutCapacity({ available_stock: 4, price_quota: 100 }, 'balance'),
    4
  )
  assert.equal(
    storeCheckoutCapacity(
      { record_kind: 'native', available_stock: 4, price_quota: 100 },
      'balance'
    ),
    0
  )
  assert.equal(
    storeCheckoutCapacity({ available_stock: 4, price_quota: null }, 'balance'),
    0
  )
})

for (const buyer of [true, false]) {
  for (const status of [
    'pending',
    'paid',
    'refund_pending',
    'refunded',
  ] as const) {
    test(`native ${buyer ? 'buyer' : 'seller'} ${status} order is read-only and retains money/time`, () => {
      useAuthStore.getState().auth.setBootstrapState('complete')
      const order = {
        id: 'native-order',
        trade_no: 'native-order',
        record_kind: 'native',
        buyer_id: 1,
        seller_id: 2,
        product_title: 'Real record',
        quantity: 2,
        status,
        price_quota: null,
        unit_price_quota: null,
        unit_price: { amount_minor: 123, currency: 'USD' },
        total: { amount_minor: 246, currency: 'USD' },
        created_at: null,
        payment_method: '',
      } as StoreOrder
      const markup = render(
        <StoreOrderRow order={order} buyer={buyer} locale='en' />
      )
      assert.match(markup, /Real record/)
      assert.match(markup, /246 USD.*minor units/)
      assert.match(markup, /123 USD.*minor units/)
      assert.match(markup, /Time unknown/)
      assert.doesNotMatch(markup, /1970|Seller fee|credits|href=/)
      for (const button of markup.matchAll(/<button\b([^>]*)>/g)) {
        assert.match(button[1], /disabled/)
      }
    })
  }
}

test('native checkout stays read-only even with legacy-looking capabilities', () => {
  useAuthStore.getState().auth.setBootstrapState('complete')
  const markup = render(
    <StoreCheckout
      product={{
        ...product,
        trading_paused: false,
        payment_methods: ['balance'],
        price_quota: 100,
      }}
    />
  )
  assert.match(markup, /read-only/)
  assert.doesNotMatch(
    markup,
    /store-payment|store-quantity|promotion-code|Pay with balance/
  )
  for (const button of markup.matchAll(/<button\b([^>]*)>/g)) {
    assert.match(button[1], /disabled/)
  }
})

test('native order zero total and missing total remain distinct', () => {
  const order = {
    id: 'zero',
    trade_no: 'zero',
    record_kind: 'native',
    product_title: 'Zero order',
    status: 'paid',
    quantity: 1,
    total: { unit: 'credits', amount_credits: 0 },
    unit_price: { unit: 'credits', amount_credits: 0 },
    created_at: null,
  } as StoreOrder
  assert.match(
    render(<StoreOrderRow order={order} buyer locale='en' />),
    /0 credits/
  )
  assert.match(
    render(
      <StoreOrderRow
        order={{ ...order, total: undefined }}
        buyer={false}
        locale='en'
      />
    ),
    /Amount unknown/
  )
})

test('null, invalid and out-of-range dates never become epoch or Invalid Date', () => {
  for (const value of [
    null,
    undefined,
    0,
    Number.NaN,
    Infinity,
    Number.MAX_SAFE_INTEGER,
  ]) {
    assert.equal(storeDate(value, 'en'), '—')
  }
  assert.notEqual(storeDate(1700000000, 'en'), '—')
})
