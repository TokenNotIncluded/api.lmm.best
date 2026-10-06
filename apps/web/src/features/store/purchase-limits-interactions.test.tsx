/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreProduct, StoreProductInput } from './types'

const dom = new Window({ url: 'https://shop.example.test/store/seller' })
dom.document.write('<!doctype html><html><head></head><body></body></html>')
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
  'customElements',
  'CSSStyleSheet',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
mock.module('@/components/ui/markdown', () => ({ Markdown: () => null }))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { StoreProductEditor } = await import('./seller-page')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const originalPut = api.put
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const product: StoreProduct = {
  id: 'limited-product',
  seller_id: 9,
  title: 'Fixture',
  description: '',
  image_urls: [],
  contact: '',
  links: [],
  price_quota: 500000,
  template: 'card-key',
  delivery_strategy: 'sequential',
  payment_methods: ['balance'],
  pickup_login_required: false,
  pickup_code_required: false,
  email_pickup_link: false,
  status: 'published',
  official: false,
  available_stock: 8,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
  max_quantity_per_order: 7,
  max_quantity_per_buyer: 23,
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
async function mount(node: React.ReactNode) {
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const mountedRoot = root
  const mountedClient = client
  await act(async () => {
    mountedRoot.render(
      <QueryClientProvider client={mountedClient}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(flush)
}
function field(id: string) {
  const element = document.getElementById(id) as HTMLInputElement | null
  assert.ok(element, id)
  return element
}
async function input(id: string, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    dom.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(field(id), value)
    field(id).dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
async function save() {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((element) => element.textContent === 'Save draft')
  assert.ok(button)
  await act(async () => {
    button.click()
    await flush()
  })
}
afterEach(async () => {
  const mountedRoot = root
  if (mountedRoot) await act(async () => mountedRoot.unmount())
  root = undefined
  client?.clear()
  api.put = originalPut
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('seller sets arbitrary limits, keeps blank unlimited and rejects zero before saving', async () => {
  const writes: StoreProductInput[] = []
  api.put = (async (_url, body) => {
    writes.push(body as StoreProductInput)
    return { data: { success: true, data: product } }
  }) as typeof api.put
  await mount(
    <StoreProductEditor
      product={product}
      minimumPriceQuota={0}
      allowedMethods={['balance']}
      purchaseLimitsSupported
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  assert.equal(field('store-order-limit').value, '7')
  assert.equal(field('store-buyer-limit').value, '23')
  await input('store-order-limit', '12')
  await input('store-buyer-limit', '')
  await save()
  assert.equal(writes.length, 1)
  assert.equal(writes[0].max_quantity_per_order, 12)
  assert.equal(writes[0].max_quantity_per_buyer, null)
  assert.equal('sale_limit' in writes[0], false)
  assert.equal('buyer_purchase_remaining' in writes[0], false)
  await input('store-order-limit', '0')
  await save()
  assert.equal(writes.length, 1)
})

test('older server support omits both limit fields rather than clearing retained limits', async () => {
  let body: StoreProductInput | undefined
  api.put = (async (_url, value) => {
    body = value as StoreProductInput
    return { data: { success: true, data: product } }
  }) as typeof api.put
  await mount(
    <StoreProductEditor
      product={product}
      minimumPriceQuota={0}
      allowedMethods={['balance']}
      onClose={() => {}}
      onSaved={async () => {}}
    />
  )
  assert.equal(document.getElementById('store-order-limit'), null)
  await save()
  assert.ok(body)
  assert.equal('max_quantity_per_order' in body, false)
  assert.equal('max_quantity_per_buyer' in body, false)
})
