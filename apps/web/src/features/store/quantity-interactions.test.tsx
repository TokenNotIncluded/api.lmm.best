/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreOrder, StoreProduct } from './types'

const dom = new Window({
  url: 'https://shop.example.test/store/products/guest-product',
})
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
  'sessionStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(dom.navigator, 'locks', {
  configurable: true,
  value: {
    request: async (_name: string, task: () => Promise<unknown>) => task(),
  },
})
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
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const originalConfig = useSystemConfigStore.getState().config
const { api } = await import('@/lib/api')
const { StoreCheckout } = await import('./product-page')
const { StoreClaimPage } = await import('./claim-page')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Store purchase disclaimer v1': 'Independent seller terms.',
      },
    },
  },
})
const originalGet = api.get
const originalPost = api.post
const envelope = (data: unknown) => ({ data: { success: true, data } })
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const product: StoreProduct = {
  id: 'guest-product',
  seller_id: 9,
  title: 'Guest fixture',
  description: 'Text items',
  image_urls: [],
  contact: '',
  links: [],
  price_quota: 500000,
  template: 'card-key',
  delivery_strategy: 'sequential',
  payment_methods: ['external:epay', 'balance'],
  pickup_login_required: false,
  pickup_code_required: false,
  email_pickup_link: false,
  status: 'published',
  official: true,
  available_stock: 5,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
}
const pending: StoreOrder = {
  id: 'guest-fixture-order',
  trade_no: 'MS-guest-fixture',
  buyer_id: 2,
  seller_id: 9,
  fee_quota: 0,
  product_id: product.id,
  product_title: product.title,
  quantity: 1,
  unit_price_quota: 500000,
  price_quota: 500000,
  payment_method: 'external:epay',
  status: 'pending',
  amount_minor: 100,
  currency: 'USD',
  frozen_usd_fx: '1',
  payment_issued: false,
  pickup_login_required: false,
  pickup_code_required: false,
  email_pickup_link: false,
  created_at: 1,
  paid_at: 0,
  expires_at: 9999999999,
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
async function mount(node: React.ReactNode, signedIn = true) {
  const mockPost = api.post
  api.post = (async (url: string, body: unknown, options?: unknown) => {
    const buyerId = useAuthStore.getState().auth.user?.id
    const response = await mockPost(url, body, options as never)
    if (
      url === '/api/store/orders' &&
      response.data?.success === true &&
      response.data?.data?.order
    ) {
      const request = body as Record<string, unknown>
      response.data.data.order = {
        product_id: request.product_id,
        variant_id: request.variant_id,
        quantity: request.quantity,
        payment_method: request.payment_method,
        buyer_id: buyerId,
        ...response.data.data.order,
      }
    }
    return response
  }) as typeof api.post

  useSystemConfigStore.setState({
    config: {
      ...originalConfig,
      currency: {
        ...originalConfig.currency,
        currencyUnit: 'credit',
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 7,
        cnyPerUsdExact: '7',
      },
    },
  })
  useAuthStore.getState().auth.setUser(
    signedIn
      ? {
          id: 2,
          role: 1,
          username: 'buyer',
          quota: 5000000,
          setting: JSON.stringify({ wallet_display_currency: 'CREDIT' }),
        }
      : null
  )
  const host = document.createElement('div')
  document.body.append(host)
  const mountedRoot = createRoot(host)
  root = mountedRoot
  const mountedClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
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
async function replaceCheckout(next: StoreProduct) {
  assert.ok(root)
  assert.ok(client)
  const mountedRoot = root
  const mountedClient = client
  await act(async () => {
    mountedRoot.render(
      <QueryClientProvider client={mountedClient}>
        <I18nextProvider i18n={i18n}>
          <StoreCheckout product={next} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await flush()
  })
}
function required<T>(value: T | null | undefined): T {
  assert.ok(value)
  return value
}
function button(text: string) {
  const element = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((item) => item.textContent?.trim() === text)
  assert.ok(element, `button ${text}`)
  return element
}
async function click(element: HTMLElement) {
  await act(async () => {
    element.click()
    await flush()
  })
}
async function input(id: string, value: string) {
  const element = document.getElementById(id) as HTMLInputElement | null
  assert.ok(element)
  const setter = required(
    required(
      Object.getOwnPropertyDescriptor(dom.HTMLInputElement.prototype, 'value')
    ).set
  )
  await act(async () => {
    setter.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
function mockCheckout() {
  const writes: Array<Record<string, unknown>> = []
  api.get = (async () =>
    envelope({
      version: 'merchant-store-v1',
      accepted: true,
    })) as typeof api.get
  api.post = (async (url, body) => {
    assert.equal(url, '/api/store/orders')
    const checkout = body as Record<string, unknown>
    writes.push(checkout)
    const quantity = checkout.quantity as number
    return envelope({
      order: {
        ...pending,
        quantity,
        price_quota: quantity * product.price_quota,
      },
      created: true,
    })
  }) as typeof api.post
  return writes
}
afterEach(async () => {
  const mountedRoot = root
  if (mountedRoot) await act(async () => mountedRoot.unmount())
  root = undefined
  client?.clear()
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.setUser(null)
  useSystemConfigStore.setState({ config: originalConfig })
  localStorage.clear()
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

test('visible quantity controls obey stock and a merchant can order their own whole batch', async () => {
  const writes = mockCheckout()
  await mount(
    <StoreCheckout product={{ ...product, seller_id: 2, available_stock: 2 }} />
  )
  const less = required(
    document.querySelector<HTMLButtonElement>(
      '[aria-label="Decrease quantity"]'
    )
  )
  const more = required(
    document.querySelector<HTMLButtonElement>(
      '[aria-label="Increase quantity"]'
    )
  )
  assert.equal(less.disabled, true)
  await click(more)
  assert.equal(
    (document.getElementById('store-quantity') as HTMLInputElement).value,
    '2'
  )
  assert.equal(more.disabled, true)
  assert.match(
    document.querySelector('aside strong')?.textContent || '',
    /1,000,000/
  )
  await click(less)
  await click(more)
  await click(button('Place order'))
  assert.equal(writes.length, 1)
  assert.equal(writes[0].quantity, 2)
  assert.equal(writes[0].payment_method, 'external:epay')
  assert.equal('price_quota' in writes[0], false)
  assert.equal('unit_price_quota' in writes[0], false)
  assert.equal('variant_id' in writes[0], false)
})

test('restocking keeps quantity controls visible and a refreshed buyer or order cap clamps the count', async () => {
  mockCheckout()
  await mount(<StoreCheckout product={{ ...product, available_stock: 1 }} />)
  const more = required(
    document.querySelector<HTMLButtonElement>(
      '[aria-label="Increase quantity"]'
    )
  )
  assert.equal(more.disabled, true)
  await replaceCheckout({ ...product, available_stock: 8 })
  assert.equal(more.disabled, false)
  await click(more)
  await click(more)
  assert.equal(
    (document.getElementById('store-quantity') as HTMLInputElement).value,
    '3'
  )
  await replaceCheckout({
    ...product,
    available_stock: 8,
    max_quantity_per_order: 2,
  })
  assert.equal(
    (document.getElementById('store-quantity') as HTMLInputElement).value,
    '2'
  )
  assert.equal(more.disabled, true)
  await replaceCheckout({
    ...product,
    available_stock: 8,
    max_quantity_per_buyer: 4,
    buyer_purchase_remaining: 1,
  })
  assert.equal(
    (document.getElementById('store-quantity') as HTMLInputElement).value,
    '1'
  )
  assert.equal(more.disabled, true)
  await replaceCheckout({
    ...product,
    available_stock: 8,
    max_quantity_per_buyer: 4,
    buyer_purchase_remaining: 0,
  })
  assert.equal(button('Place order').disabled, true)
  assert.ok(document.getElementById('store-quantity'))
  assert.equal(
    (document.getElementById('store-quantity') as HTMLInputElement).value,
    '1'
  )
})

test('switching to a smaller specification adjusts the selected quantity without changing sales quota', async () => {
  mockCheckout()
  const variant = (id: string, capacity: number) => ({
    id,
    product_id: product.id,
    name: id,
    price_quota: product.price_quota,
    template: product.template,
    enabled: true,
    created_at: 1,
    updated_at: 1,
    is_default: false,
    inventory_total: capacity,
    inventory_available: capacity,
    reserved_stock: 0,
    sale_available: capacity,
    trading_paused: false,
  })
  const withVariants = {
    ...product,
    sale_available: 8,
    sale_limit: 20,
    variants: [variant('large', 5), variant('small', 1)],
  }
  await mount(<StoreCheckout product={withVariants} />)
  await click(
    required(
      document.querySelector<HTMLInputElement>(
        'input[name="store-variant"][value="large"]'
      )
    )
  )
  await input('store-quantity', '4')
  await click(
    required(
      document.querySelector<HTMLInputElement>(
        'input[name="store-variant"][value="small"]'
      )
    )
  )
  assert.equal(
    (document.getElementById('store-quantity') as HTMLInputElement).value,
    '1'
  )
  assert.equal(withVariants.sale_limit, 20)
  assert.equal(withVariants.sale_available, 8)
})

test('fractional, exponent, excessive, empty and unsafe total inputs cannot place an order', async () => {
  const writes = mockCheckout()
  await mount(
    <StoreCheckout
      product={{
        ...product,
        available_stock: 1000,
        price_quota: Number.MAX_SAFE_INTEGER,
      }}
    />
  )
  for (const value of [
    '1.5',
    '1.0',
    '1e2',
    '0',
    '-1',
    '',
    '2',
    '101',
    '9007199254740992',
  ]) {
    await input('store-quantity', value)
    assert.equal(button('Place order').disabled, true, value)
    await click(button('Place order'))
  }
  assert.equal(writes.length, 0)
  await input('store-quantity', '1')
  assert.equal(button('Place order').disabled, false)
})

test('unknown network retries keep the original quantity and request key until the original order is resolved', async () => {
  const attempts: Array<Record<string, unknown>> = []
  api.get = (async () =>
    envelope({
      version: 'merchant-store-v1',
      accepted: true,
    })) as typeof api.get
  api.post = (async (url, body) => {
    assert.equal(url, '/api/store/orders')
    attempts.push(body as Record<string, unknown>)
    if (attempts.length <= 2) {
      throw new Error('Simulated retryable network error')
    }
    return envelope({
      order: { ...pending, quantity: 1, price_quota: 500000 },
      created: true,
    })
  }) as typeof api.post
  await mount(<StoreCheckout product={product} />)
  await click(button('Place order'))
  await click(button('Retry this order request'))
  await input('store-quantity', '2')
  await click(button('Retry this order request'))
  assert.deepEqual(
    attempts.map((attempt) => attempt.quantity),
    [1, 1, 1]
  )
  assert.equal(attempts[0].request_key, attempts[1].request_key)
  assert.equal(attempts[0].request_key, attempts[2].request_key)
})

test('balance quantity can be 1000; switching to a platform gateway clamps the count to its current cap', async () => {
  const writes = mockCheckout()
  await mount(
    <StoreCheckout
      product={{
        ...product,
        available_stock: 2000,
        payment_methods: ['balance', 'platform:waffo_pancake'],
      }}
    />
  )
  await input('store-quantity', '1000')
  assert.equal(button('Place order').disabled, false)
  await input('store-quantity', '1001')
  assert.equal(button('Place order').disabled, true)
  await input('store-quantity', '200')
  await click(
    required(
      document.querySelector<HTMLElement>(
        '[name="store-payment"][value="platform:waffo_pancake"]'
      )
    )
  )
  assert.equal(
    (document.getElementById('store-quantity') as HTMLInputElement).value,
    '100'
  )
  assert.equal(button('Place order').disabled, false)
  await input('store-quantity', '100')
  await click(button('Place order'))
  assert.equal(writes[0].quantity, 100)
  assert.equal(writes[0].payment_method, 'platform:waffo_pancake')
})

test('stock changes clamp excessive input before an explicit purchase', async () => {
  const writes = mockCheckout()
  const { useState } = await import('react')
  function StockChange() {
    const [stock, setStock] = useState(5)
    return (
      <>
        <button type='button' onClick={() => setStock(1)}>
          Reduce stock
        </button>
        <StoreCheckout product={{ ...product, available_stock: stock }} />
      </>
    )
  }
  await mount(<StockChange />)
  await input('store-quantity', '3')
  assert.equal(button('Place order').disabled, false)
  await click(button('Reduce stock'))
  assert.equal(
    (document.getElementById('store-quantity') as HTMLInputElement).value,
    '1'
  )
  assert.equal(button('Place order').disabled, false)
  assert.equal(writes.length, 0)
  await input('store-quantity', '1')
  assert.equal(button('Place order').disabled, false)
  await click(button('Place order'))
  assert.equal(writes[0].quantity, 1)
})

test('batch collection displays and copies every delivered item together', async () => {
  const items = ['BATCH-KEY-ONE', 'BATCH-KEY-TWO', 'BATCH-KEY-THREE']
  let copied = ''
  const originalWriteText = dom.navigator.clipboard.writeText
  dom.navigator.clipboard.writeText = async (value) => {
    copied = value
  }
  api.get = (async () =>
    envelope({
      product_title: 'Batch fixture',
      quantity: 3,
      status: 'paid',
      pickup_login_required: false,
      pickup_code_required: false,
    })) as typeof api.get
  api.post = (async () =>
    envelope({
      product_title: 'Batch fixture',
      items,
    })) as typeof api.post
  try {
    await mount(<StoreClaimPage token={'a'.repeat(64)} />)
    assert.equal(document.body.textContent?.includes(items[0]), false)
    await click(button('Collect items'))
    for (const item of items) {
      assert.ok(document.body.textContent?.includes(item))
    }
    await click(button('Copy all items'))
    assert.equal(copied, items.join('\n'))
  } finally {
    dom.navigator.clipboard.writeText = originalWriteText
  }
})
