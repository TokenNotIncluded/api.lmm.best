/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

import {
  GUEST_STORE_CART_KEY,
  STORE_CATALOGUE_VIEW_KEY,
  guestCartClear,
  guestCartRemove,
  guestCartUpsert,
  readGuestStoreCart,
  readStoreCatalogueView,
  sanitizeGuestStoreCart,
  writeStoreCatalogueView,
} from './collection-storage'

const dom = new Window({ url: 'https://shop.example.test/store/cart' })
const previousWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom })

beforeEach(() => {
  dom.localStorage.clear()
  guestCartClear()
})

after(() => {
  if (previousWindow) {
    Object.defineProperty(globalThis, 'window', previousWindow)
  } else {
    Reflect.deleteProperty(globalThis, 'window')
  }
  dom.happyDOM.abort()
})

test('guest storage sanitizes identifiers, quantities, duplicates and extra private fields', () => {
  const valid = { product_id: 'product-1', variant_id: 'sku-1', quantity: 2 }
  assert.deepEqual(
    sanitizeGuestStoreCart([
      {
        ...valid,
        quantity: 1,
        account_id: 27,
        price_quota: 999,
        guest_token: 'private',
      },
      valid,
      { ...valid, variant_id: 'sku-2', quantity: '3' },
      { ...valid, product_id: '../private-product' },
      { ...valid, quantity: 0 },
      { ...valid, quantity: 0.5 },
      { ...valid, quantity: Number.MAX_SAFE_INTEGER + 1 },
      null,
    ]),
    [valid]
  )
})

test('guest cart keeps separate SKUs and writes only reference fields with absolute quantities', () => {
  const first = { product_id: 'product-1', variant_id: 'sku-1', quantity: 2 }
  const second = { ...first, variant_id: 'sku-2', quantity: 1 }
  guestCartUpsert(first)
  guestCartUpsert(second)
  guestCartUpsert({ ...first, quantity: 5 })
  assert.equal(
    readGuestStoreCart().find((item) => item.variant_id === 'sku-1')?.quantity,
    5
  )
  assert.equal(readGuestStoreCart().length, 2)
  const saved = dom.localStorage.getItem(GUEST_STORE_CART_KEY)
  assert.ok(saved)
  assert.deepEqual(JSON.parse(saved), [second, { ...first, quantity: 5 }])
  guestCartRemove(first.product_id, first.variant_id)
  assert.deepEqual(readGuestStoreCart(), [second])
  guestCartClear()
  assert.deepEqual(readGuestStoreCart(), [])
  assert.equal(dom.localStorage.getItem(GUEST_STORE_CART_KEY), null)
})

test('malformed persisted JSON is unavailable rather than rendered as saved cart data', () => {
  dom.localStorage.setItem(GUEST_STORE_CART_KEY, '{broken')
  assert.deepEqual(readGuestStoreCart(), [])
  dom.localStorage.setItem(
    GUEST_STORE_CART_KEY,
    JSON.stringify([
      {
        product_id: 'product-2',
        variant_id: 'sku-2',
        quantity: 1,
        title: 'private',
      },
    ])
  )
  assert.deepEqual(readGuestStoreCart(), [
    { product_id: 'product-2', variant_id: 'sku-2', quantity: 1 },
  ])
  guestCartUpsert({ product_id: 'product-3', variant_id: 'sku-3', quantity: 2 })
  const saved = dom.localStorage.getItem(GUEST_STORE_CART_KEY)
  assert.ok(saved)
  assert.equal(saved.includes('private'), false)
})

test('invalid upserts leave a saved cart intact', () => {
  const reference = {
    product_id: 'product-1',
    variant_id: 'sku-1',
    quantity: 2,
  }
  guestCartUpsert(reference)
  assert.equal(guestCartUpsert({ ...reference, quantity: -1 }), false)
  assert.deepEqual(readGuestStoreCart(), [reference])
})

test('catalogue view preferences accept only the two non-sensitive display modes', () => {
  assert.equal(readStoreCatalogueView(), 'cards')
  dom.localStorage.setItem(STORE_CATALOGUE_VIEW_KEY, 'private-viewer:27')
  assert.equal(readStoreCatalogueView(), 'cards')
  writeStoreCatalogueView('list')
  assert.equal(readStoreCatalogueView(), 'list')
  assert.equal(dom.localStorage.getItem(GUEST_STORE_CART_KEY), null)
  writeStoreCatalogueView('cards')
  assert.equal(readStoreCatalogueView(), 'cards')
})
