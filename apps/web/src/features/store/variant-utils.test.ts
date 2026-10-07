/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { StoreProduct, StoreVariant } from './types'
import { storeTotal } from './utils'
import {
  initialStoreVariant,
  legacyVariantProduct,
  selectedStoreVariant,
  storeVariantCapacity,
  storeVariantPrice,
} from './variant-utils'

const product = {
  id: 'product-a',
  price_quota: 500000,
  available_stock: 90,
  sale_available: 2,
} as StoreProduct
function variant(
  id: string,
  changes: Partial<StoreVariant> = {}
): StoreVariant {
  return {
    id,
    product_id: product.id,
    name: id,
    price_quota: 1500000,
    template: 'card-key',
    enabled: true,
    is_default: id === 'default',
    created_at: 1,
    updated_at: 1,
    inventory_total: 45,
    inventory_available: 40,
    reserved_stock: 5,
    sale_available: 2,
    trading_paused: false,
    ...changes,
  }
}

test('multiple active specifications require an exact choice, including when a default exists', () => {
  const current = {
    ...product,
    default_variant_id: 'default',
    variants: [variant('default'), variant('plus')],
  }
  assert.equal(initialStoreVariant(current), '')
  for (const id of ['', 'unknown', 'another-product']) {
    assert.equal(selectedStoreVariant(current, id), undefined)
    assert.equal(storeVariantPrice(current, id), undefined)
    assert.equal(storeVariantCapacity(current, id), 0)
  }
  assert.equal(
    initialStoreVariant({ ...current, variants: [variant('plus')] }),
    'plus'
  )
})

test('wrong-product and disabled specifications never fall back to the default price or stock', () => {
  const current = {
    ...product,
    variants: [
      variant('default'),
      variant('disabled', { enabled: false }),
      variant('foreign', { product_id: 'product-b' }),
    ],
  }
  for (const id of ['disabled', 'foreign']) {
    assert.equal(storeVariantPrice(current, id), undefined)
    assert.equal(storeVariantCapacity(current, id), 0)
  }
  assert.equal(storeVariantPrice(current, 'default'), 1500000)
})

test('variant checkout uses its exact whole-credit price and individual shared-cap capacity', () => {
  const current = {
    ...product,
    inventory_total: 90,
    variants: [variant('default'), variant('plus', { price_quota: 5000000 })],
  }
  assert.equal(storeVariantCapacity(current, 'plus'), 2)
  assert.equal(storeTotal(storeVariantPrice(current, 'plus')!, 2), 10000000)
  assert.equal(
    storeVariantCapacity(
      { ...current, variants: [variant('plus', { trading_paused: true })] },
      'plus'
    ),
    0
  )
  assert.equal(
    storeVariantCapacity(
      { ...current, variants: [variant('plus', { sale_available: 0 })] },
      'plus'
    ),
    0
  )
  assert.equal(
    storeVariantPrice(
      { ...current, variants: [variant('plus', { price_quota: 1.5 })] },
      'plus'
    ),
    undefined
  )
})

test('a partial new projection fails closed instead of reverting to legacy default terms', () => {
  for (const current of [
    { ...product, default_variant_id: 'default' },
    { ...product, variants: [] },
  ]) {
    assert.equal(legacyVariantProduct(current), false)
    assert.equal(storeVariantPrice(current, ''), undefined)
    assert.equal(storeVariantCapacity(current, ''), 0)
  }
})

test('legacy default-only projections retain their existing integer price and clipped sale limit', () => {
  assert.equal(legacyVariantProduct(product), true)
  assert.equal(storeVariantPrice(product, ''), 500000)
  assert.equal(storeVariantCapacity(product, ''), 2)
})

test('fixed content has unbounded supply only for its exact eligible variant and absent sales cap', () => {
  const fixed = variant('fixed', {
    template: 'fixed-content',
    unlimited_supply: true,
    inventory_total: 0,
    inventory_available: 0,
    reserved_stock: 0,
    sale_available: 0,
  })
  const current = {
    ...product,
    unlimited_supply: true,
    sale_limit: null,
    sale_available: 0,
    variants: [fixed, variant('card')],
  }
  assert.equal(storeVariantCapacity(current, 'fixed'), Infinity)
  assert.equal(storeVariantCapacity(current, 'card'), 2)
  assert.equal(
    storeVariantCapacity(
      {
        ...current,
        sale_limit: 4,
        variants: [{ ...fixed, sale_available: 3 }],
      },
      'fixed'
    ),
    3
  )
  assert.equal(
    storeVariantCapacity(
      { ...current, variants: [{ ...fixed, trading_paused: true }] },
      'fixed'
    ),
    0
  )
  assert.equal(
    storeVariantCapacity(
      { ...current, variants: [{ ...fixed, enabled: false }] },
      'fixed'
    ),
    0
  )
})
