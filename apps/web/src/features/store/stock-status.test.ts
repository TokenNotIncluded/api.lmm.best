/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  storeProductDisplayTags,
  type StoreStockTagProduct,
} from './stock-status'
import type { StoreVariant } from './types'

const stock = (patch: StoreStockTagProduct): StoreStockTagProduct => ({
  display_tags: ['out_of_stock', 'guest_purchase'],
  available_stock: 0,
  sale_available: 0,
  sale_limit: null,
  unlimited_supply: false,
  trading_paused: false,
  inventory_available: 0,
  ...patch,
})
const variant = (patch: Partial<StoreVariant> = {}): StoreVariant => ({
  id: 'default',
  product_id: 'product',
  name: 'Default',
  price_quota: 500000,
  template: 'card-key',
  enabled: true,
  is_default: true,
  created_at: 1,
  updated_at: 1,
  inventory_total: 9,
  inventory_available: 9,
  reserved_stock: 0,
  sale_available: 0,
  trading_paused: true,
  ...patch,
})

test('unbounded fixed content corrects an older out-of-stock projection without inventing inventory', () => {
  const product = stock({ unlimited_supply: true })
  const before = structuredClone(product)
  assert.deepEqual(storeProductDisplayTags(product), [
    'in_stock',
    'guest_purchase',
  ])
  assert.deepEqual(product, before)
  assert.equal(product.available_stock, 0)
  assert.equal(product.sale_available, 0)
})

test('finite fixed-content and inventory quotas remain in stock only while purchase availability is positive', () => {
  for (const unlimited_supply of [true, false]) {
    assert.deepEqual(
      storeProductDisplayTags(
        stock({ unlimited_supply, sale_limit: 5, sale_available: 5 })
      ),
      ['in_stock', 'guest_purchase']
    )
  }
  assert.deepEqual(
    storeProductDisplayTags(
      stock({ unlimited_supply: true, sale_limit: 5, sale_available: 0 })
    ),
    ['out_of_stock', 'guest_purchase']
  )
})

test('paused inventory is not empty inventory and cannot be relabeled purchasable', () => {
  const product = stock({
    trading_paused: true,
    inventory_available: 9,
    variants: [variant()],
  })
  assert.deepEqual(storeProductDisplayTags(product), [
    'trading_paused',
    'guest_purchase',
  ])
  assert.equal(product.sale_available, 0)
  assert.equal(product.trading_paused, true)
  assert.deepEqual(
    storeProductDisplayTags(
      stock({ trading_paused: true, inventory_available: 9 })
    ),
    ['trading_paused', 'guest_purchase']
  )
})

test('paused fixed content and zero sales quotas retain supply without bypassing trading restrictions', () => {
  assert.deepEqual(
    storeProductDisplayTags(
      stock({
        trading_paused: true,
        variants: [
          variant({
            template: 'fixed-content',
            unlimited_supply: true,
            inventory_available: 0,
          }),
        ],
      })
    ),
    ['trading_paused', 'guest_purchase']
  )
  assert.deepEqual(
    storeProductDisplayTags(
      stock({ unlimited_supply: true, trading_paused: true, sale_limit: 0 })
    ),
    ['trading_paused', 'guest_purchase']
  )
})

test('unlimited sales never create card inventory and disabled variants never prove supply', () => {
  for (const sale_limit of [null, 10]) {
    assert.deepEqual(
      storeProductDisplayTags(stock({ sale_limit, trading_paused: true })),
      ['out_of_stock', 'guest_purchase']
    )
  }
  assert.deepEqual(
    storeProductDisplayTags(
      stock({
        trading_paused: true,
        inventory_available: 9,
        variants: [variant({ enabled: false })],
      })
    ),
    ['out_of_stock', 'guest_purchase']
  )
})

test('tags-only legacy responses and absent catalogue metadata retain the server projection', () => {
  assert.deepEqual(storeProductDisplayTags({ display_tags: ['in_stock'] }), [
    'in_stock',
  ])
  assert.deepEqual(
    storeProductDisplayTags({ display_tags: ['out_of_stock'] }),
    ['out_of_stock']
  )
  assert.deepEqual(storeProductDisplayTags({ unlimited_supply: true }), [])
  assert.deepEqual(storeProductDisplayTags({}), [])
})

test('duplicate stock tags normalize once without replacing unrelated derived tags', () => {
  assert.deepEqual(
    storeProductDisplayTags(
      stock({
        unlimited_supply: true,
        display_tags: [
          'out_of_stock',
          'auto_delivery',
          'in_stock',
          'guest_purchase',
        ],
      })
    ),
    ['in_stock', 'auto_delivery', 'guest_purchase']
  )
})

test('negative, fractional and non-finite counts do not establish availability', () => {
  for (const sale_available of [
    -1,
    0.5,
    Number.NaN,
    Number.POSITIVE_INFINITY,
  ]) {
    assert.deepEqual(storeProductDisplayTags(stock({ sale_available })), [
      'out_of_stock',
      'guest_purchase',
    ])
  }
})
