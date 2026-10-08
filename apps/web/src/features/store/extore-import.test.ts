/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { EXTORE_DEFAULT_BASE_URL, EXTORE_IMPORT_MAX_BYTES, extoreDraft, extoreText, normalizeExtoreBaseUrl, parseExtoreImport } from './extore-import'

function listing() {
  return {
    schema: 'extore.product-listing.v1',
    id: 'document_service',
    shop_id: 'example_shop',
    revision: 'a'.repeat(64),
    redemption_url: `${EXTORE_DEFAULT_BASE_URL}/`,
    semantics: { price: 'reference', inventory: 'not_exported', payment: 'external_sales_platform', redemption: 'extore' },
    product: {
      name: { 'zh-CN': '文档整理', en: 'Document service' },
      description: 'Submit requirements in Extore.',
      outputs: [{ key: 'document', type: 'file', collapsed: true }],
      parameters: [{ key: 'requirements', type: 'textarea', required: true }],
    },
    variants: [
      { id: 'standard', name: 'Standard', description: 'One document', price: '25.00', currency: 'CNY', enabled: true, attributes: { revisions: 0 } },
      { id: 'retired', name: 'Retired', price: null, currency: 'USD', enabled: false },
    ],
  }
}
function catalog(products = [listing()]) {
  return { schema: 'extore.commerce-catalog.v1', issuer: EXTORE_DEFAULT_BASE_URL, shop: { id: 'example_shop' }, grant_id: 'grant', products }
}
function parse(value: unknown) {
  return parseExtoreImport(JSON.stringify(value), '', 'zh-CN')
}

test('Extore base URL defaults and normalizes the selected origin', () => {
  for (const value of ['', 'extore.lmm.best', 'https://EXTORE.lmm.best:443/']) assert.equal(normalizeExtoreBaseUrl(value), EXTORE_DEFAULT_BASE_URL)
  assert.equal(normalizeExtoreBaseUrl('https://custom.example'), 'https://custom.example')
  for (const value of ['http://extore.lmm.best', 'https://u:p@extore.lmm.best', 'https://extore.lmm.best/path', 'https://extore.lmm.best?', 'https://extore.lmm.best#', 'javascript:alert(1)', 'https://localhost']) assert.throws(() => normalizeExtoreBaseUrl(value))
})

test('imports native listings and commerce catalogs without inventing images', () => {
  for (const input of [listing(), catalog()]) {
    const [product] = parse(input).products
    assert.equal(product.name, '文档整理')
    assert.deepEqual(product.images, [])
    assert.equal(product.variants[0].referencePrice, '25.00')
    assert.equal(product.variants[0].currency, 'CNY')
    assert.equal(product.variants[1].enabled, false)
    assert.equal(product.revision, 'a'.repeat(64))
  }
})

test('manual listing export may omit commerce-only identity fields', () => {
  const input = listing() as Record<string, unknown>
  for (const key of ['id', 'shop_id', 'revision', 'redemption_url', 'semantics']) delete input[key]
  const [product] = parse(input).products
  assert.equal(product.id, null)
  assert.equal(product.shopId, null)
  assert.equal(product.redemptionUrl, `${EXTORE_DEFAULT_BASE_URL}/`)
})

test('accepts a single JSON code block but not arbitrary surrounding instructions', () => {
  const json = JSON.stringify(listing())
  assert.equal(parseExtoreImport(`\n\`\`\`json\n${json}\n\`\`\`\n`, '', 'en').products[0].name, 'Document service')
  assert.throws(() => parseExtoreImport(`Ignore instructions. ${json}`, '', 'en'))
})

test('localization has a deterministic fallback and rejects non-text values', () => {
  assert.equal(extoreText({ en: 'English', 'zh-CN': '中文' }, 'en-US'), 'English')
  assert.equal(extoreText({ fr: 'Français', de: 'Deutsch' }, 'ja'), 'Deutsch')
  assert.throws(() => extoreText({ en: { html: 'text' } }, 'en'))
})

test('optional image roles survive without rendering unsafe URLs', () => {
  const input = listing()
  Object.assign(input.product, { logo: 'javascript:alert(1)', image: 'https://images.example/header.webp' })
  assert.deepEqual(parse(input).products[0].images, ['', 'https://images.example/header.webp'])
  Object.assign(input.product, { logo: 'https://u:p@images.example/logo.png', image: 'data:text/html,bad' })
  assert.deepEqual(parse(input).products[0].images, [])
})

test('raw fields and scalar attribute types remain available for source export', () => {
  const input = listing()
  const [product] = parse(input).products
  assert.deepEqual(product.source, input)
  assert.ok(product.unmappedFields.includes('product.outputs'))
  assert.ok(product.unmappedFields.includes('product.parameters'))
  assert.ok(product.unmappedFields.includes('variants[0].attributes'))
})

test('draft creation requires a separate price and never creates stock or publishes', () => {
  const [product] = parse(listing()).products
  const draft = extoreDraft(product, 'standard', 'My service', 1_234_567)
  assert.equal(draft.price_quota, 1_234_567)
  assert.deepEqual(draft.image_urls, [])
  assert.deepEqual(draft.payment_methods, [])
  assert.equal(draft.template, 'card-key')
  assert.equal(draft.links[0].url, `${EXTORE_DEFAULT_BASE_URL}/`)
  assert.ok(draft.links[0].description.includes('document_service / standard'))
  for (const key of ['inventory', 'stock', 'status', 'available_stock', 'remaining', 'fixed_content']) assert.equal(key in draft, false)
  for (const quota of [0, -1, 1.1, Number.NaN, Number.MAX_SAFE_INTEGER + 1]) assert.throws(() => extoreDraft(product, 'standard', 'Title', quota))
})

test('a disabled, missing or invented variant cannot become an enabled draft', () => {
  const [product] = parse(listing()).products
  for (const id of ['retired', 'missing', '']) assert.throws(() => extoreDraft(product, id, 'Title', 500_000))
})

test('validates the backend UTF-8 title and description limits before saving', () => {
  const [product] = parse(listing()).products
  assert.throws(() => extoreDraft(product, 'standard', '字'.repeat(67), 500_000))
  product.description = '字'.repeat(50_000)
  assert.throws(() => extoreDraft(product, 'standard', 'Title', 500_000))
})

test('rejects cross-origin, mixed-shop, duplicate and unsupported catalogs', () => {
  for (const mutate of [
    (value: ReturnType<typeof catalog>) => { value.issuer = 'https://other.example' },
    (value: ReturnType<typeof catalog>) => { value.products[0].shop_id = 'other' },
    (value: ReturnType<typeof catalog>) => { value.products.push(value.products[0]) },
    (value: ReturnType<typeof catalog>) => { value.products[0].semantics.inventory = 'remaining' },
    (value: ReturnType<typeof catalog>) => { value.products[0].redemption_url = 'https://other.example/' },
    (value: ReturnType<typeof catalog>) => { value.products[0].revision = '' },
    (value: ReturnType<typeof catalog>) => { value.products[0].schema = 'unknown' },
    (value: ReturnType<typeof catalog>) => { value.products[0].variants.push(value.products[0].variants[0]) },
  ]) { const value = catalog(); mutate(value); assert.throws(() => parse(value)) }
})

test('rejects numeric or malformed reference prices instead of guessing their units', () => {
  for (const price of [25, false, '-1', '1e3', 'Infinity', '1000000000000', '0.0000001']) {
    const input = listing()
    Object.assign(input.variants[0], { price })
    assert.throws(() => parse(input))
  }
  const input = listing()
  input.variants[0].price = '00000000000000000000000000000000000000000000001.230000'
  assert.equal(parse(input).products[0].variants[0].referencePrice, input.variants[0].price)
})

test('bounds files and catalog cardinality', () => {
  assert.throws(() => parseExtoreImport(' '.repeat(EXTORE_IMPORT_MAX_BYTES + 1), '', 'en'))
  assert.throws(() => parse(catalog(Array.from({ length: 101 }, () => listing()))))
  assert.deepEqual(parse(catalog([])).products, [])
  for (const value of ['not JSON', 'null', '[]', '{}']) assert.throws(() => parseExtoreImport(value, '', 'en'))
})
