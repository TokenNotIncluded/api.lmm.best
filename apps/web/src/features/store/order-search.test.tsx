/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreOrderSummary } from './types'

const dom = new Window({ url: 'https://shop.example.test/store' })
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
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { api } = await import('@/lib/api')
const { StoreOrderSearch } = await import('./order-search')
const { StorePage } = await import('./store-page')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const originalGet = api.get
const originalPost = api.post
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const email = 'lookup@example.invalid'
const challenge = 'private-challenge-fixture'
const token = 'private-search-capability-fixture'
const tradeNo = `MS${'Ab0'.repeat(10)}`
const summary: StoreOrderSummary = {
  id: 'order-summary-fixture',
  trade_no: tradeNo,
  product_title: 'Verified email order',
  quantity: 2,
  status: 'paid',
  created_at: 1,
  paid_at: 2,
  pickup_login_required: true,
  pickup_code_required: false,
}
const result = (data: unknown) => ({ data: { success: true, data } })
const sendResult = () =>
  result({ challenge_id: challenge, expires_in: 600, resend_after: 60 })
const confirmResult = () => result({ search_token: token, expires_in: 600 })
const ordersResult = (items = [summary], offset = 0, hasMore = false) =>
  result({ items, offset, limit: 20, has_more: hasMore })
type Request = {
  method: 'GET' | 'POST'
  url: string
  body?: unknown
  config?: unknown
}
function mockRequests(
  reply?: (
    request: Request
  ) => ReturnType<typeof result> | Promise<ReturnType<typeof result>>
) {
  const requests: Request[] = []
  function response(request: Request) {
    requests.push(request)
    if (request.method === 'GET' && request.url === '/api/store/config') {
      return result({
        store_catalogue_supported: false,
        store_collections_supported: false,
      })
    }
    const productListing =
      request.method === 'GET' && request.url === '/api/store/products'
    const orderSummary =
      request.method === 'GET' &&
      /^\/api\/store\/order-search\/[^/?#]+$/.test(request.url)
    const verifiedSearch =
      request.method === 'POST' &&
      [
        '/api/store/order-search/email/send',
        '/api/store/order-search/email/confirm',
        '/api/store/order-search',
      ].includes(request.url)
    assert.ok(
      productListing || orderSummary || verifiedSearch,
      `Unexpected ${request.method} fixture request: ${request.url}`
    )
    if (reply) return reply(request)
    if (productListing) {
      return result({ items: [], offset: 0, limit: 24, has_more: false })
    }
    if (request.url.endsWith('/email/send')) return sendResult()
    if (request.url.endsWith('/email/confirm')) return confirmResult()
    if (request.url === '/api/store/order-search') return ordersResult()
    return result(summary)
  }
  api.get = (async (url: string, config: unknown) =>
    response({ method: 'GET', url, config })) as typeof api.get
  api.post = (async (url: string, body: unknown, config: unknown) =>
    response({ method: 'POST', url, body, config })) as typeof api.post
  return requests
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
function owner(id: number | null) {
  useAuthStore
    .getState()
    .auth.setUser(
      id ? { id, role: 1, username: `buyer-${id}`, quota: 5000000 } : null
    )
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
async function mount(node: React.ReactNode) {
  document.body.replaceChildren()
  const host = document.createElement('div')
  document.body.append(host)
  const mountedRoot = createRoot(host)
  const mountedClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  root = mountedRoot
  client = mountedClient
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
function button(text: string) {
  const found = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((node) => node.textContent?.trim() === text)
  assert.ok(found, `button ${text}: ${document.body.textContent}`)
  return found
}
function lookup() {
  return document.querySelector<HTMLElement>(
    'section[aria-label="Order lookup"]'
  )
}
function requiredLookup() {
  const found = lookup()
  assert.ok(found)
  return found
}
function searchInput() {
  const found = document.querySelector<HTMLInputElement>('input[type="search"]')
  assert.ok(found)
  return found
}
function codeInput() {
  const found = document.querySelector<HTMLInputElement>(
    '#store-order-search-code'
  )
  assert.ok(found)
  return found
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click()
    await flush()
  })
}
async function input(node: HTMLInputElement, value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      dom.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    setter.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
async function search(value: string) {
  await input(searchInput(), value)
  await click(button('Search'))
}
async function verify() {
  await click(button('Send verification code'))
  await input(codeInput(), '123456')
  await click(button('Verify and find orders'))
}
function assertMemoryOnly(...privateValues: string[]) {
  assert.ok(client)
  const storage = Array.from({ length: localStorage.length }, (_, index) => {
    const key = localStorage.key(index)
    assert.ok(key)
    return [key, localStorage.getItem(key)]
  })
  const caches = {
    queries: client
      .getQueryCache()
      .getAll()
      .map((query) => ({
        key: query.queryKey,
        data: query.state.data,
      })),
    mutations: client
      .getMutationCache()
      .getAll()
      .map((mutation) => mutation.state),
  }
  for (const value of privateValues) {
    assert.equal(
      JSON.stringify(storage).includes(value),
      false,
      'private value in localStorage'
    )
    assert.equal(
      window.location.href.includes(value),
      false,
      'private value in URL'
    )
    assert.equal(
      JSON.stringify(caches).includes(value),
      false,
      'private value in shared cache'
    )
  }
}
afterEach(async () => {
  const mountedRoot = root
  if (mountedRoot) await act(async () => mountedRoot.unmount())
  root = undefined
  client?.clear()
  api.get = originalGet
  api.post = originalPost
  owner(null)
  localStorage.clear()
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('email search asks for explicit verification without reading or sending the email automatically', async () => {
  const requests = mockRequests()
  await mount(<StorePage />)
  await input(searchInput(), email)
  assert.equal(lookup(), null)
  await click(button('Search'))
  assert.ok(lookup())
  assert.match(
    requiredLookup().textContent || '',
    /Verify that you own this email/
  )
  assert.equal(document.querySelector('#store-order-search-code'), null)
  assert.deepEqual(
    requests.map(({ method, url }) => ({ method, url })),
    [
      { method: 'GET', url: '/api/store/config' },
      { method: 'GET', url: '/api/store/products' },
    ]
  )
  assert.equal(JSON.stringify(requests).includes(email), false)
  assertMemoryOnly(email)
})

test('six-digit email verification sends only the challenge and code, then reads orders with a memory-only capability', async () => {
  const confirmation = deferred<ReturnType<typeof result>>()
  const requests = mockRequests((request) => {
    if (request.url.endsWith('/email/send')) return sendResult()
    if (request.url.endsWith('/email/confirm')) return confirmation.promise
    return ordersResult([
      {
        ...summary,
        pickup_url:
          'https://shop.example.test/store/claim/server-provided-token',
      },
    ])
  })
  await mount(<StoreOrderSearch mode='email' value={email} />)
  assert.equal(requests.length, 0)
  await click(button('Send verification code'))
  assert.deepEqual(requests[0], {
    method: 'POST',
    url: '/api/store/order-search/email/send',
    body: { email },
    config: { skipErrorHandler: true, skipBusinessError: true },
  })
  const resend = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((node) => /^Wait \d+ seconds$/.test(node.textContent?.trim() || ''))
  assert.ok(resend)
  assert.equal(resend.disabled, true)
  await input(codeInput(), '12345')
  assert.equal(button('Verify and find orders').disabled, true)
  assert.equal(requests.length, 1)
  await input(codeInput(), '123456')
  await click(button('Verify and find orders'))
  assert.deepEqual(requests[1].body, {
    challenge_id: challenge,
    code: '123456',
  })
  assert.equal(requests[1].url, '/api/store/order-search/email/confirm')
  assert.equal(
    requests.length,
    2,
    'orders must wait for successful confirmation'
  )
  await act(async () => {
    confirmation.resolve(confirmResult())
    await flush()
  })
  assert.equal(requests[2].method, 'POST')
  assert.equal(requests[2].url, '/api/store/order-search')
  assert.deepEqual(requests[2].body, {
    search_token: token,
    offset: 0,
    limit: 20,
  })
  assert.match(requiredLookup().textContent || '', /Verified email order/)
  assert.equal(
    requiredLookup().querySelector('a')?.getAttribute('href'),
    'https://shop.example.test/store/claim/server-provided-token'
  )
  assertMemoryOnly(email, challenge, token, summary.product_title)
})

test('verified email pagination reuses only the capability and server page offsets', async () => {
  const requests = mockRequests((request) => {
    if (request.url.endsWith('/email/send')) return sendResult()
    if (request.url.endsWith('/email/confirm')) return confirmResult()
    const offset = (request.body as { offset: number }).offset
    return ordersResult(
      [
        {
          ...summary,
          product_title:
            offset === 20 ? 'Second page order' : summary.product_title,
        },
      ],
      offset,
      offset === 0
    )
  })
  await mount(<StoreOrderSearch mode='email' value={email} />)
  await verify()
  assert.equal(button('Previous page').disabled, true)
  await click(button('Next page'))
  assert.deepEqual(requests.at(-1)?.body, {
    search_token: token,
    offset: 20,
    limit: 20,
  })
  assert.match(requiredLookup().textContent || '', /Second page order/)
  assert.equal(button('Next page').disabled, true)
  await click(button('Previous page'))
  assert.deepEqual(requests.at(-1)?.body, {
    search_token: token,
    offset: 0,
    limit: 20,
  })
  assert.equal(
    requests.filter(({ url }) => url.endsWith('/email/confirm')).length,
    1
  )
  assertMemoryOnly(token, summary.product_title)
})

for (const phase of ['send', 'confirm', 'list'] as const) {
  test(`editing the search unmounts lookup and ignores a late ${phase} response from the previous email`, async () => {
    const pending = deferred<ReturnType<typeof result>>()
    const requests = mockRequests((request) => {
      if (request.url === '/api/store/products') return ordersResult([])
      if (request.url.endsWith('/email/send')) {
        return phase === 'send' ? pending.promise : sendResult()
      }
      if (request.url.endsWith('/email/confirm')) {
        return phase === 'confirm' ? pending.promise : confirmResult()
      }
      return pending.promise
    })
    await mount(<StorePage />)
    await search(email)
    await click(button('Send verification code'))
    if (phase !== 'send') {
      await input(codeInput(), '123456')
      await click(button('Verify and find orders'))
    }
    await input(searchInput(), 'different@example.invalid')
    assert.equal(
      lookup(),
      null,
      'editing must immediately hide the previous lookup'
    )
    await click(button('Search'))
    assert.match(
      requiredLookup().textContent || '',
      /different@example.invalid/
    )
    await act(async () => {
      pending.resolve(
        phase === 'send'
          ? sendResult()
          : phase === 'confirm'
            ? confirmResult()
            : ordersResult()
      )
      await flush()
    })
    assert.equal(document.querySelector('#store-order-search-code'), null)
    assert.equal(
      requiredLookup().textContent?.includes(summary.product_title),
      false
    )
    assert.equal(requiredLookup().textContent?.includes(email), false)
    assert.equal(button('Send verification code').disabled, false)
    assert.equal(
      requests.filter(({ url }) => url === '/api/store/order-search').length,
      phase === 'list' ? 1 : 0,
      'a late confirmation must not start a lookup for the previous email'
    )
    assertMemoryOnly(token, summary.product_title)
  })
}

test('switching accounts clears verified email results and requires a new explicit verification', async () => {
  owner(2)
  const requests = mockRequests()
  await mount(<StorePage />)
  await search(email)
  await verify()
  assert.match(requiredLookup().textContent || '', /Verified email order/)
  await act(async () => {
    owner(3)
    await flush()
  })
  assert.equal(
    requiredLookup().textContent?.includes(summary.product_title),
    false
  )
  assert.equal(document.querySelector('#store-order-search-code'), null)
  assert.equal(button('Send verification code').disabled, false)
  assert.equal(requests.filter(({ method }) => method === 'POST').length, 3)
  assertMemoryOnly(token, summary.product_title)
})

test('invalid email lookup fails visibly without making a request', async () => {
  const requests = mockRequests()
  await mount(<StoreOrderSearch mode='email' value='invalid@address' />)
  assert.match(
    document.querySelector('[role="alert"]')?.textContent || '',
    /Enter a valid email address/
  )
  assert.equal(button('Send verification code').disabled, true)
  assert.equal(requests.length, 0)
})

for (const phase of ['send', 'confirm', 'list'] as const) {
  test(`email ${phase} errors remain visible without exposing order results`, async () => {
    const requests = mockRequests((request) => {
      const currentPhase = request.url.endsWith('/email/send')
        ? 'send'
        : request.url.endsWith('/email/confirm')
          ? 'confirm'
          : 'list'
      if (currentPhase === phase) {
        return {
          data: {
            success: false,
            message: `${phase} fixture failure`,
            data: null,
          },
        }
      }
      return currentPhase === 'send' ? sendResult() : confirmResult()
    })
    await mount(<StoreOrderSearch mode='email' value={email} />)
    await click(button('Send verification code'))
    if (phase !== 'send') {
      await input(codeInput(), '123456')
      await click(button('Verify and find orders'))
    }
    assert.match(
      document.querySelector('[role="alert"]')?.textContent || '',
      new RegExp(`${phase} fixture failure`)
    )
    assert.equal(
      requiredLookup().textContent?.includes(summary.product_title),
      false
    )
    assert.equal(
      requests.length,
      phase === 'send' ? 1 : phase === 'confirm' ? 2 : 3
    )
    assertMemoryOnly(token, summary.product_title)
  })
}

test('automatic mode recognizes a new MS order number and displays only its safe summary', async () => {
  const requests = mockRequests((request) =>
    request.url === '/api/store/products'
      ? ordersResult([])
      : result({
          ...summary,
          items: ['private-delivery-content'],
          claim_token: 'private-claim-token',
        })
  )
  await mount(<StorePage />)
  await search(tradeNo)
  assert.deepEqual(
    requests.map(({ method, url }) => ({ method, url })),
    [
      { method: 'GET', url: '/api/store/config' },
      { method: 'GET', url: '/api/store/products' },
      { method: 'GET', url: `/api/store/order-search/${tradeNo}` },
    ]
  )
  assert.match(requiredLookup().textContent || '', /Verified email order/)
  assert.match(requiredLookup().textContent || '', /Quantity: 2/)
  assert.equal(
    requiredLookup().textContent?.includes('private-delivery-content'),
    false
  )
  assert.equal(
    requiredLookup().textContent?.includes('private-claim-token'),
    false
  )
  assert.equal(
    requiredLookup().querySelector('a'),
    null,
    'a summary without pickup_url must not invent a claim link'
  )
  assert.equal(document.querySelector('textarea'), null)
})

test('explicit order number mode accepts legacy and custom order identifiers without sending them to product search', async () => {
  const requests = mockRequests()
  await mount(<StorePage />)
  const selector = document.querySelector<HTMLSelectElement>(
    'select[aria-label="Search type"]'
  )
  assert.ok(selector)
  assert.ok(selector.querySelector('option[value="order"]'))
  await act(async () => {
    selector.value = 'order'
    selector.dispatchEvent(new Event('change', { bubbles: true }))
    await flush()
  })
  for (const legacy of ['MS-fixture', 'custom/legacy order?reference=old']) {
    await search(legacy)
    assert.equal(
      requests.at(-1)?.url,
      `/api/store/order-search/${encodeURIComponent(legacy)}`
    )
    assert.ok(lookup())
  }
  assert.equal(
    requests.filter(({ url }) => url === '/api/store/products').length,
    1
  )
  assert.equal(
    requests.some(({ method }) => method === 'POST'),
    false
  )
})

test('order-number errors are visible and unsafe pickup URLs never create links', async () => {
  const requests = mockRequests((request) =>
    request.url.endsWith('/failed-order')
      ? {
          data: {
            success: false,
            message: 'Order lookup fixture failure',
            data: null,
          },
        }
      : result({ ...summary, pickup_url: 'javascript:alert(1)' })
  )
  await mount(<StoreOrderSearch mode='order' value='failed-order' />)
  assert.match(
    document.querySelector('[role="alert"]')?.textContent || '',
    /Order lookup fixture failure/
  )
  assert.equal(document.querySelector('article'), null)
  const mountedRoot = root
  const mountedClient = client
  assert.ok(mountedRoot)
  assert.ok(mountedClient)
  await act(async () => {
    mountedRoot.render(
      <QueryClientProvider client={mountedClient}>
        <I18nextProvider i18n={i18n}>
          <StoreOrderSearch key='unsafe' mode='order' value='unsafe-order' />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
  assert.match(requiredLookup().textContent || '', /Verified email order/)
  assert.equal(requiredLookup().querySelector('a'), null)
  assert.equal(
    requests.some(({ method }) => method === 'POST'),
    false
  )
})

test('seller storefront sends its seller filter and displays seller identity rather than the signed-in buyer', async () => {
  owner(99)
  const requests = mockRequests(() =>
    result({
      items: [],
      offset: 0,
      limit: 24,
      has_more: false,
      seller: {
        id: 27,
        username: 'actual-merchant',
        display_name: 'Actual merchant shop',
        contact_email: 'public-sales@example.test',
      },
    })
  )
  await mount(<StorePage sellerId={27} />)
  assert.equal(requests.length, 2)
  const listing = requests.find(({ url }) => url === '/api/store/products')
  assert.ok(listing)
  assert.deepEqual((listing.config as { params: unknown }).params, {
    q: '',
    offset: 0,
    limit: 24,
    sort: 'comprehensive',
    seller_id: 27,
  })
  assert.match(
    document.querySelector('h1')?.textContent || '',
    /Actual merchant shop/
  )
  assert.match(document.body.textContent || '', /@actual-merchant/)
  assert.doesNotMatch(document.body.textContent || '', /buyer-99/)
  const merchantLink = [
    ...document.querySelectorAll<HTMLAnchorElement>('a'),
  ].find((node) => node.textContent?.trim() === 'User ID: 27')
  assert.equal(merchantLink?.getAttribute('href'), '/store?seller_id=27')
  assert.equal(
    document.querySelector('a[href^="mailto:"]')?.getAttribute('href'),
    'mailto:public-sales%40example.test'
  )
  assert.ok(
    [...document.querySelectorAll<HTMLAnchorElement>('a')].some(
      (node) =>
        node.getAttribute('href') === '/store' &&
        node.textContent?.trim() === 'Browse all sellers'
    )
  )
})
