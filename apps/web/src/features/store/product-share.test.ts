/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { shareStoreProduct, storeProductShareUrl } from './product-share'

const product = { id: 'product-fixture', title: 'Merchant-defined product' }
test('product shares use the actual origin and exclude search credentials and fragments', () => {
  assert.equal(
    storeProductShareUrl(
      product.id,
      'https://shop.test/store/claim/token?guest=secret#pickup'
    ),
    'https://shop.test/store/products/product-fixture'
  )
  for (const id of ['../claim/secret', 'id?token=secret', '', 'a'.repeat(65)]) {
    assert.throws(() => storeProductShareUrl(id, 'https://shop.test'))
  }
  assert.throws(() => storeProductShareUrl(product.id, 'javascript:alert(1)'))
})
test('native share success passes the exact product and does not touch the clipboard', async () => {
  let copied = 0
  const result = await shareStoreProduct(product, 'https://shop.test', {
    share: async (data) =>
      assert.deepEqual(data, {
        title: product.title,
        url: 'https://shop.test/store/products/product-fixture',
      }),
    copy: async () => {
      copied++
      return true
    },
  })
  assert.equal(result, 'shared')
  assert.equal(copied, 0)
})
test('native cancellation does not copy while unsupported native sharing falls back truthfully', async () => {
  let copied = 0
  const copy = async () => {
    copied++
    return true
  }
  assert.equal(
    await shareStoreProduct(product, 'https://shop.test', {
      share: async () => {
        const error = new Error('cancelled')
        error.name = 'AbortError'
        throw error
      },
      copy,
    }),
    'cancelled'
  )
  assert.equal(copied, 0)
  assert.equal(
    await shareStoreProduct(product, 'https://shop.test', {
      share: async () => {
        throw new Error('unsupported')
      },
      copy,
    }),
    'copied'
  )
  assert.equal(copied, 1)
  await assert.rejects(
    shareStoreProduct(product, 'https://shop.test', {
      copy: async () => false,
    }),
    /Copy failed/
  )
})
