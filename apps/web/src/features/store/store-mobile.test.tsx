/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreCatalogueProduct } from './catalogue-types'

const dom = new Window({ url: 'https://shop.example.test/store/cart' })
dom.document.write('<!doctype html><html><body></body></html>')
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
  'sessionStorage',
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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { StorePage } = await import('./store-page')
const { StoreNavigation } = await import('./store-navigation')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const originalGet = api.get
const flush = () => new Promise((resolve) => setTimeout(resolve, 30))
function required<T>(value: T | null | undefined): T {
  assert.ok(value !== null && value !== undefined)
  return value
}
async function mount(node: React.ReactNode) {
  const host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  await act(async () => {
    required(root).render(
      <QueryClientProvider client={required(client)}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  await act(flush)
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click()
    await flush()
  })
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  client = undefined
  api.get = originalGet
  useAuthStore.getState().auth.setUser(null)
  localStorage.clear()
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())
const product: StoreCatalogueProduct = {
  id: 'mobile-product',
  seller_id: 9,
  title: 'A product without an image',
  description: 'A merchant description.',
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
  available_stock: 0,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
}
type Request = { url: string; config: unknown }
const result = (data: unknown) => ({ data: { success: true, data } })
const page = (items: unknown[]) =>
  result({ items, offset: 0, limit: 100, has_more: false })
function mockRequests(
  reply?: (request: Request) => ReturnType<typeof result>,
  supported = true
) {
  const requests: Request[] = []
  api.get = (async (url: string, config: unknown) => {
    const request = { url, config }
    requests.push(request)
    if (url === '/api/store/config') {
      return result({
        store_catalogue_supported: supported,
        store_collections_supported: supported,
        store_likes_supported: false,
      })
    }
    return reply
      ? reply(request)
      : page(url === '/api/store/products' ? [product] : [])
  }) as typeof api.get
  return requests
}
function lastProductRequest(requests: Request[]) {
  return required(
    [...requests]
      .reverse()
      .find((request) => request.url === '/api/store/products')
  )
}

test('mobile filters start folded, keep explicit false filters, and expose search options', async () => {
  const requests = mockRequests()
  await mount(<StorePage />)
  const toggle = required(
    document.querySelector<HTMLButtonElement>(
      'button[aria-label="More filters"]'
    )
  )
  const panel = required(
    document.getElementById(required(toggle.getAttribute('aria-controls')))
  )
  assert.equal(toggle.getAttribute('aria-expanded'), 'false')
  assert.equal(panel.hidden, true)
  assert.ok(panel.querySelector('#store-search-type'))
  assert.ok(document.querySelector('form[role="search"] input[type="search"]'))
  await click(toggle)
  assert.equal(panel.hidden, false)
  const guest = required(
    document.querySelector<HTMLSelectElement>('#store-catalogue-guestPurchase')
  )
  await act(async () => {
    guest.value = 'false'
    guest.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  assert.equal(
    (
      lastProductRequest(requests).config as {
        params: { guest_purchase: boolean }
      }
    ).params.guest_purchase,
    false
  )
  await click(toggle)
  assert.equal(panel.hidden, true)
  assert.ok(toggle.textContent?.includes('Filters active'))
})

test('mobile product actions keep seller details and cart controls behind a named disclosure', async () => {
  mockRequests((request) =>
    request.url === '/api/store/products'
      ? page([
          {
            ...product,
            seller: {
              id: 9,
              username: 'example',
              display_name: 'Example seller',
            },
          },
        ])
      : page([])
  )
  await mount(<StorePage />)
  const card = required(
    document.querySelector<HTMLElement>('[data-store-product-id]')
  )
  const toggle = required(
    card.querySelector<HTMLButtonElement>(
      'button[aria-label="Cart and favorites"]'
    )
  )
  const panel = required(
    document.getElementById(required(toggle.getAttribute('aria-controls')))
  )
  assert.equal(panel.hidden, true)
  assert.equal(card.querySelectorAll('img').length, 0)
  assert.ok(card.querySelector('a[aria-label="Shop by Example seller"]'))
  assert.ok(panel.textContent?.includes('User ID: 9'))
  assert.ok(panel.textContent?.includes('Add to cart'))
  await click(toggle)
  assert.equal(toggle.getAttribute('aria-expanded'), 'true')
  assert.equal(panel.hidden, false)
  await click(toggle)
  assert.equal(panel.hidden, true)
})

for (const canReview of [false, true]) {
  test(`mobile navigation preserves folded seller links and reviewer permission: ${canReview}`, async () => {
    await mount(<StoreNavigation path='/store/cart' canReview={canReview} />)
    const nav = required(
      document.querySelector('nav[aria-label="Store navigation"]')
    )
    const toggle = required(
      nav.querySelector<HTMLButtonElement>('button[aria-label="More"]')
    )
    const panel = required(
      document.getElementById(required(toggle.getAttribute('aria-controls')))
    )
    assert.equal(panel.hidden, true)
    assert.equal(
      nav.querySelector('a[aria-current="page"]')?.getAttribute('href'),
      '/store/cart'
    )
    assert.ok(panel.querySelector('a[href="/store/manage"]'))
    assert.ok(panel.querySelector('a[href="/store/settings"]'))
    assert.equal(!!panel.querySelector('a[href="/store/review"]'), canReview)
    await click(toggle)
    assert.equal(panel.hidden, false)
    await click(toggle)
    assert.equal(panel.hidden, true)
  })
}

test('mobile search options remain available without catalogue capability', async () => {
  mockRequests(undefined, false)
  await mount(<StorePage />)
  const toggle = required(
    document.querySelector<HTMLButtonElement>(
      'button[aria-label="More filters"]'
    )
  )
  await click(toggle)
  assert.ok(document.querySelector('#store-search-type'))
  assert.equal(document.querySelector('#store-catalogue-sort'), null)
  assert.equal(document.querySelector('#store-catalogue-stock'), null)
})
