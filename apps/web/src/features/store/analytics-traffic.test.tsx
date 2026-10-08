/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreCatalogueProduct } from './catalogue-types'

const dom = new Window({ url: 'https://shop.example.test/store' })
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
let visibility: DocumentVisibilityState = 'visible'
Object.defineProperty(document, 'visibilityState', {
  configurable: true,
  get: () => visibility,
})
mock.module('@/components/ui/markdown', () => ({
  Markdown: ({ children }: { children: string }) => <div>{children}</div>,
}))

class MockIntersectionObserver {
  static instances: MockIntersectionObserver[] = []
  readonly targets = new Set<Element>()
  constructor(
    private readonly callback: IntersectionObserverCallback,
    readonly options?: IntersectionObserverInit
  ) {
    MockIntersectionObserver.instances.push(this)
  }
  observe(target: Element) {
    this.targets.add(target)
  }
  unobserve(target: Element) {
    this.targets.delete(target)
  }
  disconnect() {
    this.targets.clear()
  }
  emit(target: Element, ratio: number, intersecting = ratio > 0) {
    this.callback(
      [
        {
          target,
          intersectionRatio: ratio,
          isIntersecting: intersecting,
        } as IntersectionObserverEntry,
      ],
      this as unknown as IntersectionObserver
    )
  }
}
Object.defineProperty(globalThis, 'IntersectionObserver', {
  configurable: true,
  value: MockIntersectionObserver,
})

const { act, StrictMode } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { createStoreTrafficPage } = await import('./traffic-page')
const { StoreProductPage } = await import('./product-page')
const { StorePage } = await import('./store-page')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

const product: StoreCatalogueProduct = {
  id: 'traffic-product',
  seller_id: 9,
  title: 'Traffic product',
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
  available_stock: 99,
  sale_available: 99,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
  visibility: 'public',
  purchase_login_required: true,
}
const original = { get: api.get, post: api.post }
const originalSetTimeout = globalThis.setTimeout
const originalClearTimeout = globalThis.clearTimeout
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const disposers: (() => void)[] = []
type TrafficRequest = {
  url: string
  body: { kind: string; page_key: string; page_started_at: number }
  options: Record<string, unknown>
}

function mockTraffic(
  respond: () => Promise<{ data: { success: boolean } }> = async () => ({
    data: { success: true },
  })
) {
  const requests: TrafficRequest[] = []
  api.post = (async (
    url: string,
    body: TrafficRequest['body'],
    options: TrafficRequest['options']
  ) => {
    requests.push({ url, body, options })
    return respond()
  }) as typeof api.post
  return requests
}

function owner(id: number | null) {
  useAuthStore
    .getState()
    .auth.setUser(id ? { id, role: 1, username: `user-${id}` } : null)
}

function element() {
  const target = document.createElement('article')
  document.body.append(target)
  return target
}

function intersect(target: Element, ratio: number, intersecting = ratio > 0) {
  const observer = MockIntersectionObserver.instances.find((item) =>
    item.targets.has(target)
  )
  assert.ok(observer, 'an observer is attached to the real card node')
  observer.emit(target, ratio, intersecting)
}

function clock() {
  let now = 0
  let sequence = 0
  const timers = new Map<number, { at: number; run: () => void }>()
  globalThis.setTimeout = ((run: () => void, delay = 0) => {
    const id = ++sequence
    timers.set(id, { at: now + delay, run })
    return id
  }) as unknown as typeof setTimeout
  globalThis.clearTimeout = ((id: number) => {
    timers.delete(id)
  }) as unknown as typeof clearTimeout
  return {
    async advance(duration: number) {
      const end = now + duration
      for (;;) {
        const next = [...timers]
          .filter(([, item]) => item.at <= end)
          .sort((a, b) => a[1].at - b[1].at)[0]
        if (!next) break
        timers.delete(next[0])
        now = next[1].at
        next[1].run()
        await Promise.resolve()
      }
      now = end
      await Promise.resolve()
    },
    size: () => timers.size,
  }
}

function setVisibility(value: DocumentVisibilityState) {
  visibility = value
  document.dispatchEvent(new Event('visibilitychange'))
}

const flush = () => new Promise((resolve) => originalSetTimeout(resolve, 30))
function required<T>(value: T | null | undefined): T {
  assert.ok(value !== null && value !== undefined)
  return value
}
async function render(node: React.ReactNode) {
  if (!root) {
    const host = document.createElement('div')
    document.body.append(host)
    root = createRoot(host)
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  }
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

function mockCatalogue(failure = false) {
  api.get = (async (url: string) => {
    if (url === '/api/store/config') {
      return {
        data: { success: true, data: { store_catalogue_supported: true } },
      }
    }
    if (url === '/api/store/products') {
      return {
        data: {
          success: true,
          data: { items: [product], has_more: false, offset: 0, limit: 24 },
        },
      }
    }
    if (
      url === `/api/store/products/${product.id}` ||
      url === `/api/store/my/products/${product.id}/preview`
    ) {
      if (failure) throw new Error('Product not found')
      return { data: { success: true, data: product } }
    }
    return { data: { success: true, data: { version: '', text: '' } } }
  }) as typeof api.get
}

afterEach(async () => {
  globalThis.setTimeout = originalSetTimeout
  globalThis.clearTimeout = originalClearTimeout
  for (const dispose of disposers.splice(0)) dispose()
  if (root) await act(() => required(root).unmount())
  root = undefined
  client?.clear()
  client = undefined
  document.body.innerHTML = ''
  api.get = original.get
  api.post = original.post
  owner(null)
  visibility = 'visible'
  MockIntersectionObserver.instances = []
  localStorage.clear()
  sessionStorage.clear()
})
after(() => dom.happyDOM.abort())

test('impressions require at least half of the card for a continuous 500ms', async () => {
  const requests = mockTraffic()
  const time = clock()
  const page = createStoreTrafficPage()
  const target = element()
  disposers.push(page.observe(target, product))
  assert.deepEqual(
    MockIntersectionObserver.instances[0].options?.threshold,
    [0, 0.5]
  )
  intersect(target, 0.49)
  await time.advance(1000)
  assert.equal(requests.length, 0)
  intersect(target, 0.5)
  await time.advance(499)
  assert.equal(requests.length, 0)
  intersect(target, 0.49)
  await time.advance(1)
  assert.equal(requests.length, 0)
  intersect(target, 0.5)
  await time.advance(500)
  assert.equal(requests.length, 1)
  assert.equal(requests[0].body.kind, 'impression')
  assert.match(
    requests[0].body.page_key,
    /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
  )
  assert.equal(requests[0].body.page_started_at, page.startedAt)
})

test('hidden pages cancel dwell time and need a fresh visible observation', async () => {
  const requests = mockTraffic()
  const time = clock()
  const page = createStoreTrafficPage()
  const target = element()
  disposers.push(page.observe(target, product))
  intersect(target, 1)
  await time.advance(400)
  setVisibility('hidden')
  await time.advance(5000)
  assert.equal(requests.length, 0)
  intersect(target, 1)
  await time.advance(500)
  assert.equal(requests.length, 0)
  setVisibility('visible')
  await time.advance(1000)
  assert.equal(requests.length, 0)
  intersect(target, 1)
  await time.advance(499)
  assert.equal(requests.length, 0)
  await time.advance(1)
  assert.equal(requests.length, 1)
})

test('owner views are excluded even when authentication changes during dwell', async () => {
  const requests = mockTraffic()
  const time = clock()
  const page = createStoreTrafficPage()
  owner(product.seller_id)
  disposers.push(page.observe(element(), product))
  await page.record(product, 'click')
  assert.equal(MockIntersectionObserver.instances.length, 0)
  assert.equal(requests.length, 0)
  owner(null)
  const target = element()
  disposers.push(page.observe(target, product))
  intersect(target, 1)
  await time.advance(400)
  owner(product.seller_id)
  await time.advance(100)
  assert.equal(requests.length, 0)
})

test('one browse scope deduplicates each product and kind across card remounts', async () => {
  const requests = mockTraffic()
  const time = clock()
  const page = createStoreTrafficPage()
  const first = element()
  const dispose = page.observe(first, product)
  intersect(first, 1)
  await time.advance(500)
  dispose()
  first.remove()
  const second = element()
  disposers.push(page.observe(second, product))
  intersect(second, 1)
  await time.advance(500)
  await page.record(product, 'click')
  await page.record(product, 'click')
  assert.deepEqual(
    requests.map((item) => item.body.kind),
    ['impression', 'click']
  )
  assert.ok(requests.every((item) => item.body.page_key === page.pageKey))
  const returned = createStoreTrafficPage()
  await returned.record(product, 'click')
  assert.equal(requests.length, 3)
  assert.notEqual(requests[2].body.page_key, page.pageKey)
})

test('pending requests are deduplicated and failures can retry without UI errors', async () => {
  let release!: (value: { data: { success: boolean } }) => void
  const requests = mockTraffic(
    () =>
      new Promise((resolve) => {
        release = resolve
      })
  )
  const page = createStoreTrafficPage()
  const first = page.record(product, 'click')
  await page.record(product, 'click')
  assert.equal(requests.length, 1)
  release({ data: { success: false } })
  await first
  const retry = page.record(product, 'click')
  assert.equal(requests.length, 2)
  release({ data: { success: true } })
  await retry
  await page.record(product, 'click')
  assert.equal(requests.length, 2)
  assert.ok(requests.every((item) => item.body.page_key === page.pageKey))
  assert.ok(
    requests.every((item) => item.body.page_started_at === page.startedAt)
  )
  assert.deepEqual(requests[0].options, {
    skipErrorHandler: true,
    skipBusinessError: true,
    skipAuthRefresh: true,
    disableDuplicate: true,
    authScope: { userId: undefined, sessionId: undefined },
  })
})

test('traffic requests cancel when the actor changes before the HTTP interceptor runs', async () => {
  owner(2)
  api.post = original.post
  const originalAdapter = api.defaults.adapter
  let sent = 0
  api.defaults.adapter = async (config) => {
    sent += 1
    return {
      data: { success: true },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  try {
    const page = createStoreTrafficPage()
    const report = page.record(product, 'click')
    owner(product.seller_id)
    await report
    assert.equal(sent, 0)
    owner(2)
    await page.record(product, 'click')
    assert.equal(sent, 1, 'a cancelled receipt is not marked sent')
  } finally {
    api.defaults.adapter = originalAdapter
  }
})

test('a rejected HTTP request leaves its receipt available for a later retry', async () => {
  let offline = true
  const requests = mockTraffic(async () => {
    if (offline) throw new Error('Network unavailable')
    return { data: { success: true } }
  })
  const page = createStoreTrafficPage()
  await page.record(product, 'impression')
  offline = false
  await page.record(product, 'impression')
  await page.record(product, 'impression')
  assert.equal(requests.length, 2)
  assert.equal(requests[0].body.page_key, requests[1].body.page_key)
})

test('removing the last observed card clears its timer and observer', async () => {
  const requests = mockTraffic()
  const time = clock()
  const target = element()
  const dispose = createStoreTrafficPage().observe(target, product)
  intersect(target, 1)
  assert.equal(time.size(), 1)
  dispose()
  await time.advance(500)
  assert.equal(time.size(), 0)
  assert.equal(requests.length, 0)
  assert.equal(MockIntersectionObserver.instances[0].targets.size, 0)
})

test('store cards retain receipts through rerenders and list/card layout changes', async () => {
  const requests = mockTraffic()
  mockCatalogue()
  await render(<StorePage />)
  const firstCard = document.querySelector<HTMLElement>(
    '[data-store-product-id]'
  )
  assert.ok(firstCard)
  const time = clock()
  intersect(firstCard, 1)
  await time.advance(500)
  globalThis.setTimeout = originalSetTimeout
  globalThis.clearTimeout = originalClearTimeout
  assert.equal(requests.length, 1)
  await render(<StorePage />)
  const unchanged = document.querySelector<HTMLElement>(
    '[data-store-product-id]'
  )
  assert.ok(unchanged)
  const rerenderTime = clock()
  intersect(unchanged, 1)
  await rerenderTime.advance(500)
  assert.equal(requests.length, 1)
  globalThis.setTimeout = originalSetTimeout
  globalThis.clearTimeout = originalClearTimeout
  const pageKey = requests[0].body.page_key
  const listButton = [...document.querySelectorAll('button')].find(
    (button) => button.textContent?.trim() === 'List'
  )
  assert.ok(listButton)
  await act(async () => {
    listButton.click()
    await flush()
  })
  const row = document.querySelector<HTMLElement>(
    'article[data-store-product-id]'
  )
  assert.ok(row)
  const rowTime = clock()
  intersect(row, 1)
  await rowTime.advance(500)
  assert.equal(requests.length, 1)
  globalThis.setTimeout = originalSetTimeout
  globalThis.clearTimeout = originalClearTimeout
  const sort = document.querySelector<HTMLSelectElement>(
    '#store-catalogue-sort'
  )
  assert.ok(sort)
  await act(async () => {
    sort.value = 'newest'
    sort.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  await act(flush)
  const filtered = document.querySelector<HTMLElement>(
    '[data-store-product-id]'
  )
  assert.ok(filtered)
  const filterTime = clock()
  intersect(filtered, 1)
  await filterTime.advance(500)
  assert.equal(requests.length, 1)
  globalThis.setTimeout = originalSetTimeout
  globalThis.clearTimeout = originalClearTimeout
  await render(null)
  await render(<StorePage />)
  const returned = document.querySelector<HTMLElement>(
    '[data-store-product-id]'
  )
  assert.ok(returned)
  const returnedTime = clock()
  intersect(returned, 1)
  await returnedTime.advance(500)
  assert.equal(requests.length, 2)
  assert.notEqual(requests[1].body.page_key, pageKey)
})

test('leaving the actual store page before dwell completes cancels its receipt', async () => {
  const requests = mockTraffic()
  mockCatalogue()
  await render(<StorePage />)
  const card = document.querySelector<HTMLElement>('[data-store-product-id]')
  assert.ok(card)
  const time = clock()
  intersect(card, 1)
  await time.advance(499)
  await act(() => required(root).render(null))
  await time.advance(1)
  assert.equal(requests.length, 0)
  assert.ok(
    MockIntersectionObserver.instances.every((item) => !item.targets.size)
  )
})

test('successful detail loads record one click through StrictMode and refetch', async () => {
  const requests = mockTraffic()
  mockCatalogue()
  await render(
    <StrictMode>
      <StoreProductPage id={product.id} />
    </StrictMode>
  )
  assert.equal(requests.length, 1)
  assert.equal(requests[0].body.kind, 'click')
  await act(async () => {
    await required(client).invalidateQueries({ queryKey: ['store', 'product'] })
  })
  await render(
    <StrictMode>
      <StoreProductPage id={product.id} />
    </StrictMode>
  )
  assert.equal(requests.length, 1)
})

test('failed details, owner previews and the seller normal page do not record clicks', async () => {
  const requests = mockTraffic()
  mockCatalogue(true)
  await render(<StoreProductPage id={product.id} />)
  assert.equal(requests.length, 0)
  await act(() => required(root).unmount())
  root = undefined
  client?.clear()
  mockCatalogue()
  owner(product.seller_id)
  await render(<StoreProductPage id={product.id} ownerPreview />)
  assert.equal(requests.length, 0)
  await render(<StoreProductPage id={product.id} />)
  assert.equal(requests.length, 0)
})
