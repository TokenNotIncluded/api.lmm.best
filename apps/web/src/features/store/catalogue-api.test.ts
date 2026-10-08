/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'

import { catalogueApi } from './catalogue-api'

const original = {
  get: api.get,
  put: api.put,
  post: api.post,
  delete: api.delete,
}
const result = (data: unknown) => ({ data: { success: true, data } })

afterEach(() => {
  Object.assign(api, original)
})

test('catalogue search sends server pagination, sort and independent exact filters', async () => {
  const requests: { url: string; config: unknown }[] = []
  api.get = (async (url: string, config: unknown) => {
    requests.push({ url, config })
    return result({ items: [], offset: 48, limit: 24, has_more: false })
  }) as typeof api.get
  const signal = new AbortController().signal
  await catalogueApi.products(
    {
      search: '工具',
      page: 3,
      sellerId: 27,
      categoryId: '14558c81-9e12-4d68-bc9f-a3d9d9422a50',
      sort: 'sales',
      tag: 'AI / 工具',
      stock: 'out_of_stock',
      autoDelivery: false,
      aiProcessing: true,
      guestPurchase: false,
    },
    signal
  )
  assert.deepEqual(requests, [
    {
      url: '/api/store/products',
      config: {
        skipErrorHandler: true,
        skipBusinessError: true,
        signal,
        params: {
          q: '工具',
          offset: 48,
          limit: 24,
          sort: 'sales',
          seller_id: 27,
          category_id: '14558c81-9e12-4d68-bc9f-a3d9d9422a50',
          tag: 'AI / 工具',
          stock: 'out_of_stock',
          auto_delivery: false,
          ai_processing: true,
          guest_purchase: false,
        },
      },
    },
  ])
  await catalogueApi.products()
  assert.deepEqual((requests[1].config as { params: unknown }).params, {
    q: '',
    offset: 0,
    limit: 24,
    sort: 'comprehensive',
  })
})

test('account cart reads every page and retains unavailable rows for recovery', async () => {
  const requests: unknown[] = []
  const unavailable = {
    id: 'cart-1',
    product_id: 'product-1',
    variant_id: 'sku-1',
    quantity: 3,
    created_at: 1,
    updated_at: 1,
    valid: false,
    unavailable_reason: 'not_visible',
    product: null,
  }
  api.get = (async (_url: string, config: { params: { offset: number } }) => {
    requests.push(config.params)
    return config.params.offset === 0
      ? result({ items: [unavailable], offset: 0, limit: 1, has_more: true })
      : result({
          items: [{ ...unavailable, id: 'cart-2', variant_id: 'sku-2' }],
          offset: 1,
          limit: 1,
          has_more: false,
        })
  }) as typeof api.get
  const items = await catalogueApi.allCart()
  assert.deepEqual(requests, [
    { offset: 0, limit: 100 },
    { offset: 1, limit: 100 },
  ])
  assert.equal(items.length, 2)
  assert.deepEqual(items[0], unavailable)
  assert.equal(items[1].variant_id, 'sku-2')
})

test('malformed collection pagination fails instead of repeatedly reading one page', async () => {
  let requests = 0
  api.get = (async () => {
    requests += 1
    return result({ items: [], offset: 0, limit: 100, has_more: true })
  }) as typeof api.get
  await assert.rejects(catalogueApi.allFavorites(), /Store request failed/)
  assert.equal(requests, 1)
})

test('cart writes absolute SKU quantities and clear endpoints are account collections', async () => {
  const requests: { method: string; url: string; body?: unknown }[] = []
  api.put = (async (url: string, body: unknown) => {
    requests.push({ method: 'PUT', url, body })
    return result({
      id: 'cart-row',
      ...(body as object),
      created_at: 1,
      updated_at: 2,
    })
  }) as typeof api.put
  api.delete = (async (url: string) => {
    requests.push({ method: 'DELETE', url })
    return result(null)
  }) as typeof api.delete
  const input = { product_id: 'product-1', variant_id: 'sku-2', quantity: 4 }
  const stored = await catalogueApi.putCart(input)
  assert.equal(stored.quantity, 4)
  assert.equal('product' in stored, false)
  await catalogueApi.removeCart('cart/row')
  await catalogueApi.clearCart()
  await catalogueApi.addFavorite('product-1')
  await catalogueApi.removeFavorite('product/1')
  await catalogueApi.clearFavorites()
  assert.deepEqual(requests, [
    { method: 'PUT', url: '/api/store/cart', body: input },
    { method: 'DELETE', url: '/api/store/cart/cart%2Frow' },
    { method: 'DELETE', url: '/api/store/cart' },
    {
      method: 'PUT',
      url: '/api/store/favorites',
      body: { product_id: 'product-1' },
    },
    { method: 'DELETE', url: '/api/store/favorites/product%2F1' },
    { method: 'DELETE', url: '/api/store/favorites' },
  ])
})

test('catalogue metadata response does not fabricate an updated product snapshot', async () => {
  const metadata = {
    custom_tags: ['vendor tag'],
    auto_delivery: true,
    ai_processing: false,
  }
  api.put = (async () => result(metadata)) as typeof api.put
  assert.deepEqual(
    await catalogueApi.saveCatalogue('product-1', metadata),
    metadata
  )
})

test('cancellation remains cancellation rather than a storefront error', async () => {
  const cancellation = Object.assign(new Error('Canceled'), {
    code: 'ERR_CANCELED',
  })
  api.get = (async () => {
    throw cancellation
  }) as typeof api.get
  await assert.rejects(catalogueApi.cart(), (error) => error === cancellation)
})
