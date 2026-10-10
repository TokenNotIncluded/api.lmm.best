/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { ApiRequestConfig } from '@/lib/api'

import type {
  StoreAnalyticsCounts,
  StoreAnalyticsPage,
} from './analytics-types'

const dom = new Window({ url: 'https://shop.example.test/store/manage' })
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
const { StoreAnalyticsPanel, StoreProductAnalytics } =
  await import('./analytics')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const original = { get: api.get, put: api.put }
const counts: StoreAnalyticsCounts = {
  impressions: 100,
  clicks: 25,
  orders: 5,
  paid_orders: 4,
  refunded_orders: 1,
  quantity_refunded_orders: 1,
  amount_refunded_orders: 1,
  ordered_quantity: 10,
  paid_quantity: 8,
  refunded_quantity: 2,
  net_paid_quantity: 6,
}
const result = (data: unknown) => ({ data: { success: true, data } })
function page(overrides: Partial<StoreAnalyticsPage> = {}): StoreAnalyticsPage {
  return {
    items: [
      {
        ...counts,
        product_id: 'product-1',
        seller_id: 9,
        title: 'Measured product',
        status: 'published',
      },
    ],
    offset: 0,
    limit: 20,
    has_more: false,
    totals: { ...counts, orders: 12 },
    traffic_supported: true,
    traffic_retention_days: 365,
    traffic_since: 1791388800,
    ...overrides,
  }
}
function signIn(id: number, role = 1, sessionId = `session-${id}`) {
  const now = Math.floor(Date.now() / 1000)
  useAuthStore.getState().auth.setBundle({
    access_token: 'fixture-token',
    token_type: 'Bearer',
    access_expires_at: now + 3600,
    user: { id, username: `user-${id}`, role, status: 1 },
    session: {
      sid: sessionId,
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: now,
      last_active_at: now,
      expires_at: now + 3600,
    },
  })
}
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 40))
  })
}
async function render(node: React.ReactNode = <StoreAnalyticsPanel />) {
  document.body.innerHTML = '<main></main>'
  const main = document.querySelector('main')
  assert.ok(main)
  root = createRoot(main)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  await act(async () => {
    root?.render(
      <QueryClientProvider client={client as InstanceType<typeof QueryClient>}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
  })
  await settle()
}
function button(label: string) {
  const target = [...document.querySelectorAll('button')].find(
    (item) => item.textContent === label
  )
  assert.ok(target, `Missing button: ${label}`)
  return target
}
async function click(label: string) {
  await act(async () => button(label).click())
  await settle()
}
async function period(value: string) {
  const select = document.querySelector('select')
  assert.ok(select)
  await act(async () => {
    select.value = value
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  await settle()
}
afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  Object.assign(api, original)
  useAuthStore.getState().auth.reset()
})
after(() => dom.happyDOM.abort())

test('seller analytics separates disabled traffic from measured zero and loads only when opened', async () => {
  signIn(9)
  const requests: { url: string; config: unknown }[] = []
  api.get = (async (url: string, config: unknown) => {
    requests.push({ url, config })
    return result(
      page({
        traffic_supported: false,
        totals: { ...counts, impressions: null, clicks: null, orders: 0 },
        items: [
          {
            ...counts,
            impressions: null,
            clicks: null,
            orders: 0,
            product_id: 'zero-product',
            seller_id: 9,
            title: 'Zero-order product',
            status: 'draft',
          },
        ],
      })
    )
  }) as typeof api.get
  await render(<StoreProductAnalytics />)
  assert.equal(requests.length, 0)
  await click('Product analytics')
  assert.equal(requests.length, 1)
  assert.equal(requests[0].url, '/api/store/my/analytics')
  const config = requests[0].config as {
    authScope: unknown
    params: unknown
    signal: AbortSignal
  }
  assert.deepEqual(config.authScope, { userId: 9, sessionId: 'session-9' })
  assert.deepEqual(config.params, { days: 30, offset: 0, limit: 20 })
  assert.ok(config.signal instanceof AbortSignal)
  assert.equal(
    document
      .querySelector(
        '[role="group"][aria-label="Totals for all matching products"]'
      )
      ?.textContent?.includes('Orders placed0'),
    true
  )
  assert.match(document.body.textContent ?? '', /Not enabled/)
  assert.equal(document.body.textContent?.includes('All sellers'), false)
  assert.equal(
    document.body.textContent?.includes('Analytics retention settings'),
    false
  )
  assert.equal(document.body.textContent?.includes('Click-through rate'), false)
})

test('funnel rates use event counts and all-time hides ratios while retaining order and quantity counts', async () => {
  signIn(9)
  api.get = (async () => result(page())) as typeof api.get
  await render()
  const row = document.querySelector('tbody tr')
  assert.ok(row)
  assert.match(row.textContent ?? '', /25%/)
  assert.match(row.textContent ?? '', /20%/)
  assert.match(
    document.querySelector('[aria-label="Totals for all matching products"]')
      ?.textContent ?? '',
    /Orders placed12/
  )
  assert.match(
    document.querySelector('[aria-label="Totals for all matching products"]')
      ?.textContent ?? '',
    /Refunded orders1Quantity-refunded orders: 1Amount-refunded orders: 1/
  )
  assert.match(
    document.body.textContent ?? '',
    /Amount-only refunds do not reduce item quantities/
  )
  await period('all')
  assert.equal(document.body.textContent?.includes('Click-through rate'), false)
  assert.match(
    document.body.textContent ?? '',
    /Rates are hidden because these periods differ/
  )
  await click('Item quantities')
  assert.match(document.querySelector('tbody tr')?.textContent ?? '', /10826/)
})

test('administrator scope and pagination remain scoped and range changes restart at page one', async () => {
  signIn(9, 10)
  const requests: { url: string; params: { days: unknown; offset: number } }[] =
    []
  api.get = (async (
    url: string,
    config: { params: { days: unknown; offset: number } }
  ) => {
    requests.push({ url, params: config.params })
    return result(page({ has_more: config.params.offset === 0 }))
  }) as typeof api.get
  await render()
  await click('All sellers')
  assert.equal(requests.at(-1)?.url, '/api/store/analytics')
  assert.match(document.body.textContent ?? '', /Seller ID: 9/)
  assert.equal(
    document.body.textContent?.includes('Analytics retention settings'),
    false
  )
  await click('Next')
  assert.equal(requests.at(-1)?.params.offset, 20)
  await period('7')
  assert.deepEqual(requests.at(-1)?.params, { days: 7, offset: 0, limit: 20 })
  await act(async () =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 9, username: 'seller', role: 1, status: 1 })
  )
  await settle()
  assert.equal(requests.at(-1)?.url, '/api/store/my/analytics')
  assert.equal(document.body.textContent?.includes('Seller ID'), false)
})

test('account and session changes cancel old statistics and reject late results without displaying prior seller data', async () => {
  signIn(9)
  let release: ((value: ReturnType<typeof result>) => void) | undefined
  let oldSignal: AbortSignal | undefined
  api.get = (async (_url: string, config?: ApiRequestConfig) => {
    assert.ok(config?.authScope)
    assert.ok(config.signal instanceof AbortSignal)
    if (config.authScope.userId === 9) {
      oldSignal = config.signal
      return await new Promise<ReturnType<typeof result>>((resolve) => {
        release = resolve
      })
    }
    return result(
      page({
        items: [
          {
            ...counts,
            product_id: 'other',
            seller_id: 27,
            title: 'Current seller only',
            status: 'published',
          },
        ],
      })
    )
  }) as typeof api.get
  await render()
  await act(async () => signIn(27))
  await settle()
  assert.equal(oldSignal?.aborted, true)
  await act(async () => release?.(result(page())))
  await settle()
  assert.match(document.body.textContent ?? '', /Current seller only/)
  assert.equal(document.body.textContent?.includes('Measured product'), false)
  const ownerKeys = client
    ?.getQueryCache()
    .getAll()
    .filter((query) => query.queryKey[1] === 'analytics')
    .map((query) => String(query.queryKey[2]))
  assert.equal(
    ownerKeys?.some((key) => key.startsWith('account:9:')),
    false
  )
  const sessionRequests: unknown[] = []
  api.get = (async (_url: string, config: { authScope: unknown }) => {
    sessionRequests.push(config.authScope)
    return result(page())
  }) as typeof api.get
  await act(async () => signIn(27, 1, 'new-session'))
  await settle()
  assert.deepEqual(sessionRequests, [{ userId: 27, sessionId: 'new-session' }])
})

test('zero traffic shows unavailable ratios and retention shorter than the period hides rates', async () => {
  signIn(9)
  api.get = (async () =>
    result(
      page({
        items: [
          {
            ...counts,
            impressions: 0,
            clicks: 0,
            product_id: 'zero',
            seller_id: 9,
            title: 'No traffic',
            status: 'published',
          },
        ],
      })
    )) as typeof api.get
  await render()
  assert.match(document.querySelector('tbody tr')?.textContent ?? '', /—.*—/)
  api.get = (async () =>
    result(page({ traffic_retention_days: 7 }))) as typeof api.get
  await click('Refresh')
  assert.equal(document.body.textContent?.includes('Click-through rate'), false)
  assert.match(
    document.body.textContent ?? '',
    /selected period exceeds traffic retention/
  )
})

test('same-account session replacement cancels pending reads and ignores the expired session result', async () => {
  signIn(9, 1, 'previous-session')
  let release: ((value: ReturnType<typeof result>) => void) | undefined
  let previousSignal: AbortSignal | undefined
  api.get = (async (_url: string, config?: ApiRequestConfig) => {
    assert.ok(config?.authScope)
    assert.ok(config.signal instanceof AbortSignal)
    if (config.authScope.sessionId === 'previous-session') {
      previousSignal = config.signal
      return await new Promise<ReturnType<typeof result>>((resolve) => {
        release = resolve
      })
    }
    return result(page({ items: [] }))
  }) as typeof api.get
  await render()
  await act(async () => signIn(9, 1, 'replacement-session'))
  await settle()
  assert.equal(previousSignal?.aborted, true)
  await act(async () => release?.(result(page())))
  await settle()
  assert.match(document.body.textContent ?? '', /No product analytics yet/)
  assert.equal(document.body.textContent?.includes('Measured product'), false)
})

test('root retention settings load on expansion, validate the relationship and save the configured days', async () => {
  signIn(9, 100)
  const writes: unknown[] = []
  const reads: string[] = []
  api.get = (async (url: string) => {
    reads.push(url)
    return result(
      url.endsWith('/config') ? { retention_days: 365, dedupe_days: 7 } : page()
    )
  }) as typeof api.get
  api.put = (async (
    url: string,
    body: unknown,
    config: { authScope: unknown }
  ) => {
    writes.push({ url, body, authScope: config.authScope })
    return result(body)
  }) as typeof api.put
  await render()
  assert.deepEqual(reads, ['/api/store/my/analytics'])
  const details = document.querySelector('details')
  assert.ok(details)
  await act(async () => {
    details.open = true
    details.dispatchEvent(new Event('toggle'))
  })
  await settle()
  assert.equal(reads.at(-1), '/api/store/analytics/config')
  const inputs = [...document.querySelectorAll('input')]
  assert.equal(inputs.length, 2)
  const form = document.querySelector('form')
  assert.ok(form)
  async function submit(retention: string, dedupe: string) {
    assert.ok(form)
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        dom.HTMLInputElement.prototype,
        'value'
      )?.set
      setter?.call(inputs[0], retention)
      inputs[0].dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(inputs[1], dedupe)
      inputs[1].dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () =>
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true })
      )
    )
    await settle()
  }
  await submit('3', '7')
  assert.equal(writes.length, 0)
  assert.match(document.body.textContent ?? '', /Enter whole days/)
  await submit('90', '3')
  assert.deepEqual(writes, [
    {
      url: '/api/store/analytics/config',
      body: { retention_days: 90, dedupe_days: 3 },
      authScope: { userId: 9, sessionId: 'session-9' },
    },
  ])
  assert.match(document.body.textContent ?? '', /Analytics settings saved/)
})

test('a pending root settings save cannot restore private config or a success message after switching accounts', async () => {
  signIn(9, 100)
  let release: ((value: ReturnType<typeof result>) => void) | undefined
  api.get = (async (url: string) =>
    result(
      url.endsWith('/config') ? { retention_days: 365, dedupe_days: 7 } : page()
    )) as typeof api.get
  api.put = (async () =>
    await new Promise<ReturnType<typeof result>>((resolve) => {
      release = resolve
    })) as typeof api.put
  await render()
  const details = document.querySelector('details')
  assert.ok(details)
  await act(async () => {
    details.open = true
    details.dispatchEvent(new Event('toggle'))
  })
  await settle()
  await click('Save analytics settings')
  assert.ok(release)
  await act(async () => signIn(27))
  await settle()
  await act(async () =>
    release?.(result({ retention_days: 365, dedupe_days: 7 }))
  )
  await settle()
  assert.equal(
    document.body.textContent?.includes('Analytics settings saved'),
    false
  )
  assert.equal(document.querySelector('details'), null)
  assert.equal(
    client
      ?.getQueryCache()
      .getAll()
      .some((query) => query.queryKey[1] === 'analytics-config'),
    false
  )
})
