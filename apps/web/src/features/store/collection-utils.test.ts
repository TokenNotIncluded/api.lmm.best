/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { StoreCatalogueProduct } from './catalogue-types'
import { storeCartCapacity } from './collection-utils'

const product = {
  id: 'mixed-supply',
  status: 'published',
  trading_paused: false,
  unlimited_supply: true,
  sale_available: 0,
  sale_limit: null,
  variants: [
    {
      id: 'fixed',
      product_id: 'mixed-supply',
      enabled: true,
      price_quota: 500000,
      template: 'fixed-content',
      unlimited_supply: true,
      trading_paused: false,
      inventory_available: 0,
      sale_available: 0,
    },
    {
      id: 'card',
      product_id: 'mixed-supply',
      enabled: true,
      price_quota: 500000,
      template: 'card-key',
      unlimited_supply: false,
      trading_paused: false,
      inventory_available: 2,
      sale_available: 2,
    },
  ],
} as StoreCatalogueProduct

test('a mixed cart uses exact variant supply and real purchase limits without zero aggregate stock blocking fixed content', () => {
  assert.equal(
    storeCartCapacity({ ...product, max_quantity_per_order: 8 }, 'fixed'),
    8
  )
  assert.equal(
    storeCartCapacity(
      { ...product, max_quantity_per_order: 8, buyer_purchase_remaining: 3 },
      'fixed'
    ),
    3
  )
  assert.equal(storeCartCapacity(product, 'card'), 2)
  assert.equal(storeCartCapacity(product, 'unknown'), 0)
  assert.equal(product.variants?.[0].inventory_available, 0)
})

test('cart capacity honors shared sales ceilings and paused fixed variants', () => {
  assert.equal(
    storeCartCapacity(
      {
        ...product,
        sale_limit: 4,
        sale_available: 1,
        variants: [{ ...product.variants![0], sale_available: 1 }],
      },
      'fixed'
    ),
    1
  )
  assert.equal(
    storeCartCapacity({ ...product, trading_paused: true }, 'fixed'),
    0
  )
  assert.equal(
    storeCartCapacity(
      {
        ...product,
        variants: [{ ...product.variants![0], trading_paused: true }],
      },
      'fixed'
    ),
    0
  )
})
