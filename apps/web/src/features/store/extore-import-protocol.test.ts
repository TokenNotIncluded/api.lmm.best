/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  extoreOrigin,
  extoreProductDraft,
  parseExtoreCatalog,
} from './extore-import-protocol'

function fixture() {
  return {
    schema: 'extore.commerce-catalog.v1',
    issuer: 'https://extore.example',
    shop: { id: 'shop', name: 'Shop' },
    grant_id: 'grant',
    products: [
      {
        schema: 'extore.product-listing.v1',
        id: 'document',
        shop_id: 'shop',
        revision: 'a'.repeat(64),
        redemption_url: 'https://extore.example/',
        semantics: {
          price: 'reference',
          inventory: 'not_exported',
          payment: 'external_sales_platform',
          redemption: 'extore',
        },
        product: {
          name: { 'zh-CN': '文档整理', en: 'Document' },
          description: 'Original **Markdown**',
          public: false,
          parameters: [{ key: 'instructions', type: 'textarea' }],
          outputs: [{ type: 'file' }],
        },
        variants: [
          {
            id: 'basic',
            name: 'Basic',
            price: '00025.001234',
            currency: 'USDT',
            attributes: { revisions: 3, enabled: true, optional: null },
            enabled: true,
          },
          {
            id: 'disabled',
            name: 'Disabled',
            price: null,
            currency: 'CNY',
            attributes: {},
            enabled: false,
          },
        ],
      },
    ],
  }
}

test('normalizes default and custom Extore origins; rejects unsafe or ambiguous origins', () => {
  assert.equal(extoreOrigin(''), 'https://extore.lmm.best')
  assert.equal(extoreOrigin('EXTORE.LMM.BEST/'), 'https://extore.lmm.best')
  assert.equal(
    extoreOrigin('https://custom.example:443'),
    'https://custom.example'
  )
  for (const value of [
    'http://extore.example',
    'https://user:pass@extore.example',
    'https://extore.example/api',
    'https://127.0.0.1',
    'https://[::1]',
    'https://host.local',
    'https://extore.example?',
    'https://extore.example#',
  ]) {
    assert.throws(() => extoreOrigin(value), value)
  }
})

test('imports an enabled variant without turning reference price or redemption inventory into sales inventory', () => {
  const catalog = parseExtoreCatalog(fixture())
  const product = catalog.products[0]
  const draft = extoreProductDraft(catalog, product, product.variants[0], 'zh')
  assert.equal(draft.fields.title, '文档整理 · Basic')
  assert.equal(draft.fields.visibility, 'private')
  assert.equal(draft.fields.template, 'card-key')
  assert.equal(draft.referencePrice, '00025.001234')
  assert.equal(draft.referenceCurrency, 'USDT')
  for (const key of [
    'price_quota',
    'inventory_available',
    'unlimited_supply',
    'status',
  ]) {
    assert.equal(key in draft.fields, false)
  }
  assert.equal(draft.fields.image_urls?.some(Boolean), false)
  assert.equal(draft.fields.links?.[0].url, 'https://extore.example/')
  assert.deepEqual(product.variants[0].attributes, {
    revisions: 3,
    enabled: true,
    optional: null,
  })
  assert.deepEqual(product.product.parameters, [
    { key: 'instructions', type: 'textarea' },
  ])
  assert.deepEqual(draft.source, {
    issuer: catalog.issuer,
    shopId: 'shop',
    productId: 'document',
    variantId: 'basic',
    revision: 'a'.repeat(64),
  })
  assert.throws(() =>
    extoreProductDraft(catalog, product, product.variants[1], 'en')
  )
})

test('rejects mismatched shops, duplicate products or variants, numeric prices and foreign redemption URLs', () => {
  const bad: unknown[] = []
  const shop = fixture()
  shop.products[0].shop_id = 'other'
  bad.push(shop)
  const duplicate = fixture()
  duplicate.products.push(duplicate.products[0])
  bad.push(duplicate)
  const variants = fixture()
  variants.products[0].variants.push(variants.products[0].variants[0])
  bad.push(variants)
  const foreign = fixture()
  foreign.products[0].redemption_url = 'https://attacker.example/'
  bad.push(foreign)
  const numeric = fixture()
  Object.assign(numeric.products[0].variants[0], { price: 25 })
  bad.push(numeric)
  const semantics = fixture()
  semantics.products[0].semantics.inventory = 'remaining'
  bad.push(semantics)
  for (const value of bad) assert.throws(() => parseExtoreCatalog(value))
})

test('does not reinterpret unknown price and retains long source descriptions', () => {
  const source = fixture()
  source.products[0].variants[0].price = null as unknown as string
  source.products[0].product.description = 'x'.repeat(20000)
  const catalog = parseExtoreCatalog(source)
  const draft = extoreProductDraft(
    catalog,
    catalog.products[0],
    catalog.products[0].variants[0],
    'en'
  )
  assert.equal(draft.referencePrice, '')
  assert.equal(draft.fields.price_quota, undefined)
  assert.equal(draft.fields.description?.length, 20000)
})

test('optional upstream fields do not invalidate the entire catalog or invent price and visibility', () => {
  const input = fixture()
  Reflect.deleteProperty(input.products[0].product, 'description')
  Reflect.deleteProperty(input.products[0].product, 'public')
  Reflect.deleteProperty(input.products[0].product, 'name')
  Reflect.deleteProperty(input.products[0].variants[0], 'name')
  Reflect.deleteProperty(input.products[0].variants[0], 'currency')
  const catalog = parseExtoreCatalog(input)
  const product = catalog.products[0]
  const draft = extoreProductDraft(catalog, product, product.variants[0], 'en')
  assert.equal(draft.fields.title, '')
  assert.equal(draft.fields.description, '')
  assert.equal(draft.fields.visibility, 'private')
  assert.equal(draft.referenceCurrency, '')
  assert.equal(draft.fields.price_quota, undefined)
})

test('missing variant id or enabled flag is previewable but cannot grant import permission', () => {
  for (const field of ['id', 'enabled']) {
    const input = fixture()
    Reflect.deleteProperty(input.products[0].variants[0], field)
    const catalog = parseExtoreCatalog(input)
    assert.throws(() =>
      extoreProductDraft(
        catalog,
        catalog.products[0],
        catalog.products[0].variants[0],
        'en'
      )
    )
  }
})

test('upstream variants boundary permits zero and 100, not 101', () => {
  for (const count of [0, 100, 101]) {
    const input = fixture()
    input.products[0].variants = Array.from({ length: count }, (_, i) => ({
      ...input.products[0].variants[0],
      id: `v${i}`,
    }))
    if (count <= 100) {
      assert.equal(parseExtoreCatalog(input).products[0].variants.length, count)
    } else {
      assert.throws(() => parseExtoreCatalog(input))
    }
  }
})
