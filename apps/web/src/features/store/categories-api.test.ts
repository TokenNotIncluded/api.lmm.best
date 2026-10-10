/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'

import { storeApi } from './api'

const original = { get: api.get, put: api.put, post: api.post }
const result = (data: unknown) => ({ data: { success: true, data } })
afterEach(() => Object.assign(api, original))

test('category selection reads every real page and preserves unsupported capability', async () => {
  const requests: unknown[] = []
  api.get = (async (
    url: string,
    config: { params: { offset: number; limit: number } }
  ) => {
    requests.push({ url, ...config.params })
    return result({
      supported: true,
      items: [
        {
          id: String(config.params.offset),
          name: `Seller-defined ${config.params.offset}`,
        },
      ],
      offset: config.params.offset,
      limit: 100,
      has_more: config.params.offset === 0,
    })
  }) as typeof api.get
  const data = await storeApi.categories()
  assert.deepEqual(
    data.items.map((item) => item.id),
    ['0', '100']
  )
  assert.deepEqual(requests, [
    { url: '/api/store/categories', offset: 0, limit: 100 },
    { url: '/api/store/categories', offset: 100, limit: 100 },
  ])
  api.get = (async () =>
    result({
      supported: false,
      items: [],
      offset: 0,
      limit: 100,
      has_more: false,
    })) as typeof api.get
  assert.equal((await storeApi.categories()).supported, false)
})

test('category administration and explicit clearing use their own endpoints', async () => {
  const requests: unknown[] = []
  api.post = (async (url: string, body: unknown) => {
    requests.push({ url, body })
    return result(body)
  }) as typeof api.post
  api.put = (async (url: string, body: unknown) => {
    requests.push({ url, body })
    return result(body)
  }) as typeof api.put
  const body = { name: '管理员自建分类', sort_order: 4, active: true }
  await storeApi.createCategory(body)
  await storeApi.updateCategory('category/id', { ...body, active: false })
  await storeApi.productCategory('product/id', '')
  assert.deepEqual(requests, [
    { url: '/api/store/admin/categories', body },
    {
      url: '/api/store/admin/categories/category%2Fid',
      body: { ...body, active: false },
    },
    {
      url: '/api/store/products/product%2Fid/category',
      body: { category_id: '' },
    },
  ])
})

test('category pagination rejects a nonadvancing server response', async () => {
  api.get = (async () =>
    result({
      supported: true,
      items: [{ id: 'a' }],
      offset: 0,
      limit: 0,
      has_more: true,
    })) as typeof api.get
  await assert.rejects(storeApi.categories(), /Store request failed/)
})
