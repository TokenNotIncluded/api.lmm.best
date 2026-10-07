/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { storeProductXShare } from './product-x-share'

const product = {
  id: 'public-product',
  title: '中文 & café 👨‍👩‍👧‍👦',
  description: '<p>Useful <strong>product</strong> &amp; delivery</p>',
  status: 'published',
  visibility: 'public',
}

test('X composer pre-fills plain title and description and only the canonical product link', () => {
  const withPrivateFields = {
    ...product,
    description:
      '**Useful** [product](https://example.com/secret) ![image](https://example.com/image) <script>hidden-secret</script> https://shop.test/store/claim/pickup-secret?guest=guest-secret',
    contact: 'private-contact',
    review_note: 'private-review',
  }
  const share = storeProductXShare(
    withPrivateFields,
    'https://username:password@shop.test/store/claim/session?guest=session-secret#pickup'
  )
  assert.ok(share)
  const intent = new URL(share.intentUrl)
  assert.equal(intent.origin + intent.pathname, 'https://x.com/intent/tweet')
  assert.equal(
    intent.searchParams.get('text'),
    `${product.title}\nUseful product`
  )
  assert.equal(
    intent.searchParams.get('url'),
    'https://shop.test/store/products/public-product'
  )
  assert.deepEqual([...intent.searchParams.keys()], ['text', 'url'])
  for (const secret of [
    'pickup-secret',
    'guest-secret',
    'session-secret',
    'private-contact',
    'private-review',
    'hidden-secret',
    'username',
    'password',
  ]) {
    assert.ok(!share.intentUrl.includes(secret), secret)
  }
})

test('private, registered, test and unpublished items never yield public X metadata', () => {
  for (const variant of [
    { visibility: 'private' },
    { visibility: 'registered' },
    { test_mode: true },
    ...['draft', 'pending', 'paused', 'rejected', 'off_shelf', 'unlisted'].map(
      (status) => ({ status })
    ),
  ]) {
    assert.equal(
      storeProductXShare({ ...product, ...variant }, 'https://shop.test'),
      undefined
    )
  }
  assert.ok(
    storeProductXShare({ ...product, visibility: '' }, 'https://shop.test')
  )
})

test('long Unicode excerpts fit ordinary X posts without broken surrogate pairs or encoding injection', () => {
  const share = storeProductXShare(
    {
      ...product,
      title: '😀中'.repeat(200),
      description: '🛍️商品'.repeat(200),
    },
    'https://shop.test'
  )
  assert.ok(share)
  assert.ok(Array.from(share.text).length <= 120)
  assert.ok(Array.from(share.text).length * 2 + 24 <= 280)
  assert.ok(!/[\ud800-\udfff]/u.test(share.text))
  const encoded = storeProductXShare(
    {
      ...product,
      title: '&url=https://evil.test&text=override',
      description: 'Cafe\u0301 &amp; offers',
    },
    'https://shop.test'
  )
  assert.ok(encoded)
  assert.equal(new URL(encoded.intentUrl).searchParams.get('url'), encoded.url)
  assert.equal(encoded.description, 'Café & offers')
  const domains = storeProductXShare(
    { ...product, description: 'Useful 产品.cn example.com items' },
    'https://shop.test'
  )
  assert.equal(domains?.description, 'Useful items')
})
