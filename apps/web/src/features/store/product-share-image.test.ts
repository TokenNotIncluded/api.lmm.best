/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  prepareStoreProductShareImage,
  storeProductShareImageUrl,
} from './product-share-image'

const product = {
  id: 'public-product',
  title: 'Public product',
  description: 'Public description',
  status: 'published',
  image_urls: ['https://images.example.test/cover.png'],
}

test('private, test, draft and non-HTTPS images cannot trigger an image download', async () => {
  let calls = 0
  const fetchImage: typeof fetch = async () => {
    calls++
    return new Response('should not fetch')
  }
  for (const variant of [
    { visibility: 'private' },
    { visibility: 'registered' },
    { test_mode: true },
    { status: 'draft' },
    { id: '../private-order' },
    { image_urls: ['http://images.example.test/image.png'] },
    { image_urls: ['https://user:secret@images.example.test/image.png'] },
    { image_urls: ['javascript:alert(1)'] },
  ]) {
    const restricted = { ...product, ...variant }
    assert.equal(storeProductShareImageUrl(restricted), undefined)
    await assert.rejects(
      prepareStoreProductShareImage(restricted, undefined, fetchImage),
      /Image download unavailable/
    )
  }
  assert.equal(calls, 0)
})

test('actual image response becomes a downloadable file with no cookies or referrer', async () => {
  const controller = new AbortController()
  const fetchImage: typeof fetch = async (source, options) => {
    assert.equal(source, product.image_urls[0])
    assert.equal(options?.credentials, 'omit')
    assert.equal(options?.referrerPolicy, 'no-referrer')
    assert.equal(options?.signal, controller.signal)
    return new Response(new Uint8Array([137, 80, 78, 71]), {
      headers: { 'content-type': 'image/png' },
    })
  }
  const file = await prepareStoreProductShareImage(
    product,
    controller.signal,
    fetchImage
  )
  assert.equal(file.filename, 'product-public-product.png')
  assert.equal(file.blob.type, 'image/png')
  assert.deepEqual(
    new Uint8Array(await file.blob.arrayBuffer()),
    new Uint8Array([137, 80, 78, 71])
  )
})

test('unknown, HTML, remote SVG, empty and failed responses never masquerade as images', async () => {
  for (const [body, type, status] of [
    ['<script>not an image</script>', 'text/html', 200],
    ['<svg><script/></svg>', 'image/svg+xml', 200],
    ['image', 'application/octet-stream', 200],
    ['', 'image/png', 200],
    ['error', 'image/png', 403],
  ] as const) {
    await assert.rejects(
      prepareStoreProductShareImage(
        product,
        undefined,
        async () =>
          new Response(body, { status, headers: { 'content-type': type } })
      ),
      /Image download unavailable/
    )
  }
  await assert.rejects(
    prepareStoreProductShareImage(product, undefined, async () => {
      throw new TypeError('Failed to fetch')
    }),
    /Failed to fetch/
  )
})

test('oversized declared and streaming image bodies are rejected and the reader is cancelled', async () => {
  await assert.rejects(
    prepareStoreProductShareImage(
      product,
      undefined,
      async () =>
        new Response('x', {
          headers: {
            'content-type': 'image/png',
            'content-length': String(10 * 1024 * 1024 + 1),
          },
        })
    ),
    /Image download unavailable/
  )
  let cancelled = false
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new Uint8Array(10 * 1024 * 1024 + 1))
    },
    cancel() {
      cancelled = true
    },
  })
  await assert.rejects(
    prepareStoreProductShareImage(
      product,
      undefined,
      async () =>
        new Response(body, { headers: { 'content-type': 'image/png' } })
    ),
    /Image download unavailable/
  )
  assert.equal(cancelled, true)
})
