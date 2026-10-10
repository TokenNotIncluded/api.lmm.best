/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { StoreProduct } from './types'

const dom = new Window({
  url: 'https://shop.example.test/store/products/product-fixture',
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
// These fixtures start after the root has completed its session bootstrap.
useAuthStore.getState().auth.setBootstrapState('complete')
const { api } = await import('@/lib/api')
const { StoreCheckout } = await import('./product-page')
const { StoreGuestOrders } = await import('./guest-orders')
const { StoreProductEditor, StoreInventoryImport } =
  await import('./seller-page')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const originalGet = api.get
const originalPost = api.post
const originalPut = api.put
const originalDelete = api.delete
const originalAdapter = api.defaults.adapter
let root: ReturnType<typeof createRoot> | undefined
let client: InstanceType<typeof QueryClient> | undefined
const product: StoreProduct = {
  id: 'product-fixture',
  seller_id: 9,
  title: 'Fixture keys',
  description: 'Text inventory fixture',
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
  official: true,
  available_stock: 5,
  promotion_expires_at: 0,
  created_at: 1,
  updated_at: 1,
  review_note: '',
}
const result = (data: unknown) => ({ data: { success: true, data } })
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 35))
}
async function mount(node: React.ReactNode, guest = false) {
  useAuthStore.getState().auth.setUser(
    guest
      ? null
      : {
          id: 2,
          role: 1,
          username: 'buyer-2',
          quota: 5000000,
          setting: JSON.stringify({ wallet_display_currency: 'CREDIT' }),
        }
  )
  document.body.replaceChildren()
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
function button(text: string) {
  const found = [
    ...document.querySelectorAll<HTMLButtonElement>('button'),
  ].find((node) => node.textContent?.trim() === text)
  assert.ok(found, `button ${text}`)
  return found
}
function field(id: string) {
  const found = document.querySelector<HTMLInputElement>(`#${id}`)
  assert.ok(found, `input ${id}`)
  return found
}
async function click(node: HTMLElement) {
  await act(async () => {
    node.click()
    await flush()
  })
}
async function input(node: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    dom.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(node, value)
    node.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
  })
}
afterEach(async () => {
  const mountedRoot = root
  if (mountedRoot) await act(async () => mountedRoot.unmount())
  root = undefined
  client?.clear()
  api.get = originalGet
  api.post = originalPost
  api.put = originalPut
  api.delete = originalDelete
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.setUser(null)
  localStorage.clear()
  sessionStorage.clear()
  document.body.replaceChildren()
})
after(() => dom.happyDOM.abort())

async function remount(node: React.ReactNode, guest = false) {
  if (root) await act(async () => root?.unmount())
  root = undefined
  client?.clear()
  await mount(node, guest)
}

const sellerTerms = {
  version: '11111111-1111-1111-1111-111111111111',
  content: 'Real seller delivery and refund terms.',
  required: true,
  configured: true,
  accepted: false,
  updated_at: 1,
}
const guestSession = {
  guest_id: '22222222-2222-2222-2222-222222222222',
  token: 'A'.repeat(43),
  expires_at: Math.floor(Date.now() / 1000) + 3600,
}
const order = {
  id: 'guest-order',
  product_id: product.id,
  product_title: product.title,
  variant_id: '',
  variant_name: 'Frozen variant',
  quantity: 1,
  payment_method: 'external:epay',
  unit_price_quota: 500000,
  price_quota: 500000,
  fee_quota: 0,
  status: 'pending',
  trade_no: 'MS-guest-order',
  buyer_id: 0,
  seller_id: 9,
  amount_minor: 100,
  currency: 'CNY',
  frozen_usd_fx: '7',
  created_at: 1,
  paid_at: 0,
  expires_at: 9999999999,
  pickup_login_required: false,
  pickup_code_required: false,
  email_pickup_link: false,
  payment_issued: false,
}
function mockAccess(supported = true, official = true) {
  const writes: Array<{
    url: string
    body: Record<string, unknown>
    options?: Record<string, unknown>
  }> = []
  const reads: string[] = []
  api.get = (async (url: string) => {
    reads.push(url)
    if (url === '/api/store/config') {
      return result(supported ? { store_access_supported: true } : {})
    }
    if (url.endsWith('/terms')) return result(sellerTerms)
    if (url === `/api/store/products/${product.id}`) {
      return result({
        ...product,
        payment_methods: ['external:epay'],
        official,
        visibility: 'public',
        purchase_login_required: false,
      }) as never
    }
    if (url === '/api/store/disclaimer') {
      return result({
        version: 'merchant-store-v1',
        text: 'Platform terms',
        accepted: official,
      })
    }
    if (url.includes('/by-request-key/')) {
      throw {
        response: {
          status: 404,
          data: { success: false, code: 'STORE_NOT_FOUND' },
        },
      }
    }
    if (url === '/api/store/guest/orders/guest-order') return result(order)
    if (url.endsWith('/pickup-link')) {
      return result({
        pickup_url:
          'https://shop.example.test/store/claim/guest-order?token=private-order-proof',
      })
    }
    throw new Error(`Unexpected read: ${url}`)
  }) as typeof api.get
  api.post = (async (
    url: string,
    body: Record<string, unknown>,
    options?: Record<string, unknown>
  ) => {
    writes.push({ url, body, options })
    if (url === '/api/store/guest/session') return result(guestSession)
    if (url === '/api/store/guest/disclaimer/accept') return result(null)
    if (url === '/api/store/guest/orders/lookup') {
      throw {
        response: {
          status: 404,
          data: { success: false, code: 'STORE_NOT_FOUND' },
        },
      }
    }
    if (url === '/api/store/guest/orders' || url === '/api/store/orders') {
      return result({
        order: {
          ...order,
          quantity: body.quantity,
          payment_method: body.payment_method,
          buyer_id: useAuthStore.getState().auth.user?.id || 0,
          variant_id: body.variant_id || '',
          status:
            body.payment_method === 'balance' || body.payment_method === 'free'
              ? 'paid'
              : 'pending',
        },
        created: true,
      })
    }
    if (url === '/api/store/guest/orders/guest-order/pay') {
      return result({
        order_id: order.id,
        method: 'GET',
        payment_url: 'https://gateway.example.test/pay',
        amount_minor: 100,
        amount: '1.00',
        currency: 'CNY',
        status: 'pending',
      })
    }
    if (url === '/api/store/guest/email/status') {
      return result({ verified: false })
    }
    if (url === '/api/store/guest/email/verification/send') {
      return result({
        sent: true,
        challenge_id: '33333333-3333-3333-3333-333333333333',
        expires_at: Math.floor(Date.now() / 1000) + 600,
      })
    }
    if (url === '/api/store/guest/email/verification/confirm') {
      return result({ verified: true })
    }
    throw new Error(`Unexpected write: ${url}`)
  }) as typeof api.post
  return { writes, reads }
}
const guestProduct: StoreProduct = {
  ...product,
  visibility: 'public' as const,
  purchase_login_required: false,
  payment_methods: ['balance', 'external:epay'],
}
async function agreeTerms() {
  await click(button('Place order'))
  const checkbox = document.getElementById('store-accept-seller-terms')
  assert.ok(checkbox)
  await click(checkbox)
}
test('missing access capability preserves sign-in purchase and never calls terms or guest endpoints', async () => {
  const { writes, reads } = mockAccess(false)
  await mount(
    <StoreCheckout
      product={{ ...guestProduct, payment_methods: ['external:epay'] }}
    />,
    true
  )
  assert.match(document.body.textContent || '', /Sign in to buy/)
  assert.equal(writes.length, 0)
  assert.equal(
    reads.some((url) => url.endsWith('/terms')),
    false
  )
})
test('guest external checkout requires real seller terms and uses private session header for create, pay and order read', async () => {
  const { writes } = mockAccess()
  await mount(
    <StoreCheckout
      product={{
        ...guestProduct,
        payment_methods: ['balance', 'external:epay'],
      }}
    />,
    true
  )
  assert.equal(document.querySelector('input[value="balance"]'), null)
  await agreeTerms()
  const agree = button('Agree and place order')
  assert.equal(agree.disabled, false)
  await click(agree)
  const checkout = writes.find(
    (write) => write.url === '/api/store/guest/orders'
  )
  assert.ok(checkout)
  assert.equal(checkout.body.payment_method, 'external:epay')
  assert.equal(checkout.body.seller_terms_version, sellerTerms.version)
  assert.equal(checkout.body.accept_seller_terms, true)
  assert.equal(
    (checkout.options?.headers as Record<string, unknown> | undefined)?.[
      'X-Store-Guest'
    ],
    guestSession.token
  )
  assert.equal(checkout.body.guest_id, undefined)
  await click(button('Prepare payment'))
  const pay = writes.find((write) => write.url.endsWith('/guest-order/pay'))
  assert.ok(pay)
  assert.equal(
    (pay.options?.headers as Record<string, unknown> | undefined)?.[
      'X-Store-Guest'
    ],
    guestSession.token
  )
  assert.match(document.body.textContent || '', /1.00 CNY/)
  assert.match(document.body.textContent || '', /Frozen variant/)
  const cache = JSON.stringify(
    client
      ?.getQueryCache()
      .getAll()
      .map((query) => ({ key: query.queryKey, data: query.state.data }))
  )
  assert.equal(cache.includes(guestSession.token), false)
  assert.equal(cache.includes('private-order-proof'), false)
  assert.equal(localStorage.getItem('lmm.store.guest-session.v1'), null)
  assert.equal(
    JSON.parse(sessionStorage.getItem('lmm.store.guest-session.v1') || '{}')
      .token,
    guestSession.token
  )
})
test('nonofficial guest agrees to both platform and seller terms in one dialog without a user-zero acceptance', async () => {
  const { writes } = mockAccess(true, false)
  await mount(
    <StoreCheckout
      product={{
        ...guestProduct,
        payment_methods: ['external:epay'],
        official: false,
      }}
    />,
    true
  )
  await agreeTerms()
  assert.equal(button('Agree and place order').disabled, true)
  const platformLabel = [
    ...document.querySelectorAll<HTMLLabelElement>('label'),
  ].find((label) =>
    label.textContent?.includes(
      'I have carefully read and agree to this disclaimer.'
    )
  )
  assert.ok(platformLabel)
  const checkbox =
    platformLabel.parentElement?.querySelector<HTMLElement>('[role="checkbox"]')
  assert.ok(checkbox)
  await click(checkbox)
  assert.equal(button('Agree and place order').disabled, false)
  await click(button('Agree and place order'))
  assert.equal(
    writes.filter((write) => write.url === '/api/store/guest/disclaimer/accept')
      .length,
    1
  )
  assert.equal(
    writes.some((write) => write.url === '/api/store/disclaimer/accept'),
    false
  )
  assert.equal(
    writes.filter((write) => write.url === '/api/store/guest/orders').length,
    1
  )
})
test('guest email verification preserves a leading-zero code and changing address requires verification again', async () => {
  const { writes } = mockAccess()
  await mount(
    <StoreCheckout
      product={{
        ...guestProduct,
        payment_methods: ['external:epay'],
        email_pickup_link: true,
      }}
    />,
    true
  )
  await input(field('store-pickup-email'), 'guest@example.test')
  assert.equal(button('Place order').disabled, true)
  await click(button('Send verification code'))
  await input(field('store-guest-email-code'), '001234')
  await click(button('Verify email'))
  assert.equal(button('Place order').disabled, false)
  const confirm = writes.find((write) =>
    write.url.endsWith('/verification/confirm')
  )
  assert.ok(confirm)
  assert.equal(confirm.body.code, '001234')
  await input(field('store-pickup-email'), 'changed@example.test')
  assert.equal(button('Place order').disabled, true)
  assert.equal(
    document.body.textContent?.includes('Pickup email verified.'),
    false
  )
  const cache = JSON.stringify(
    client
      ?.getQueryCache()
      .getAll()
      .map((query) => query.state.data)
  )
  assert.equal(cache.includes('guest@example.test'), false)
  const stored = JSON.stringify(
    Array.from({ length: localStorage.length }, (_, index) =>
      localStorage.getItem(localStorage.key(index) || '')
    )
  )
  assert.equal(stored.includes('guest@example.test'), false)
  assert.equal(stored.includes(guestSession.token), false)
})
test('new access editor has one private mode and keeps the old registered purchase default in the saved body', async () => {
  const { writes } = mockAccess()
  api.put = (async (url: string, body: Record<string, unknown>) => {
    writes.push({ url, body })
    return result(product)
  }) as typeof api.put
  await mount(
    <StoreProductEditor
      product={product}
      allowedMethods={['balance']}
      minimumPriceQuota={500000}
      accessSupported
      testModeSupported
      onClose={() => undefined}
      onSaved={async () => undefined}
    />
  )
  assert.ok(document.getElementById('store-visibility-public'))
  const login = document.getElementById(
    'store-purchase-login'
  ) as HTMLInputElement
  assert.equal(login.checked, true)
  await click(button('Save draft'))
  const update = writes.find(
    (write) => write.url === `/api/store/products/${product.id}`
  )
  assert.ok(update)
  assert.equal(update.body.visibility, 'public')
  assert.equal(update.body.purchase_login_required, true)
  assert.equal(update.body.test_mode, undefined)
  const registered = document.getElementById('store-visibility-registered')
  assert.ok(registered)
  await click(registered)
  await click(button('Save draft'))
  const registeredUpdate = writes
    .slice()
    .reverse()
    .find((write) => write.url === `/api/store/products/${product.id}`)
  assert.ok(registeredUpdate)
  assert.equal(registeredUpdate.body.visibility, 'registered')
  assert.equal(registeredUpdate.body.purchase_login_required, true)
  assert.equal(Object.hasOwn(registeredUpdate.body, 'test_mode'), false)
})

test('a lost balance create is recovered after refresh; clearing its reminder sends no create and another purchase needs a separate click', async () => {
  const { writes } = mockAccess()
  const get = api.get
  const post = api.post
  let paid = {
    ...order,
    buyer_id: 2,
    payment_method: 'balance',
    status: 'paid',
  }
  const creates: Record<string, unknown>[] = []
  api.get = (async (url: string, options?: unknown) =>
    url.includes('/by-request-key/')
      ? result(paid)
      : get(url, options as never)) as typeof api.get
  api.post = (async (
    url: string,
    body: Record<string, unknown>,
    options?: Record<string, unknown>
  ) => {
    if (url !== '/api/store/orders') return post(url, body, options)
    creates.push(body)
    writes.push({ url, body, options })
    paid = {
      ...paid,
      id: `paid-${creates.length}`,
      quantity: Number(body.quantity),
    }
    if (creates.length === 1) throw new Error('Network result unknown')
    return result({ order: paid, created: true })
  }) as typeof api.post
  await mount(<StoreCheckout product={product} />)
  await input(field('store-pickup-code'), 'private-pickup-code')
  await input(field('store-pickup-email'), 'private-buyer@example.test')
  await agreeTerms()
  await click(button('Agree and place order'))
  assert.equal(creates.length, 1)
  const stored = localStorage.getItem('lmm:store:checkout-intents') || ''
  assert.equal(stored.includes('private-pickup-code'), false)
  assert.equal(stored.includes('private-buyer@example.test'), false)
  await remount(<StoreCheckout product={product} />)
  assert.match(document.body.textContent || '', /Order paid/)
  assert.equal(creates.length, 1)
  await click(button('Clear completed order reminders'))
  assert.equal(creates.length, 1)
  assert.equal(
    JSON.parse(localStorage.getItem('lmm:store:checkout-intents') || '{}')
      .records.length,
    0
  )
  assert.ok(button('Place order'))
  await agreeTerms()
  await click(button('Agree and place order'))
  assert.equal(creates.length, 2)
  assert.notEqual(creates[0].request_key, creates[1].request_key)
})

test('legacy unknown checkout never calls a new lookup endpoint and explicit replay requires the original secrets and request key', async () => {
  const { reads } = mockAccess(false)
  const post = api.post
  const creates: Record<string, unknown>[] = []
  api.post = (async (
    url: string,
    body: Record<string, unknown>,
    options?: Record<string, unknown>
  ) => {
    if (url !== '/api/store/orders') return post(url, body, options)
    creates.push(body)
    if (creates.length === 1) throw new Error('Network result unknown')
    return result({
      order: {
        ...order,
        buyer_id: 2,
        quantity: body.quantity,
        payment_method: 'balance',
        status: 'paid',
      },
      created: false,
    })
  }) as typeof api.post
  await mount(<StoreCheckout product={product} />)
  await input(field('store-pickup-code'), 'original-code')
  await click(button('Place order'))
  assert.equal(creates.length, 1)
  await remount(<StoreCheckout product={product} />)
  assert.equal(
    reads.some((url) => url.includes('/by-request-key/')),
    false
  )
  assert.equal(creates.length, 1)
  await input(field('store-pickup-code'), 'changed-code')
  await click(button('Retry this order request'))
  assert.equal(creates.length, 1)
  await input(field('store-pickup-code'), 'original-code')
  await click(button('Retry this order request'))
  assert.equal(creates.length, 2)
  assert.deepEqual(creates[1], creates[0])
  assert.match(document.body.textContent || '', /Order paid/)
})

test('cart selection survives sign-in and an unavailable linked SKU requires an explicit replacement before order or cart writes', async () => {
  const { writes } = mockAccess(false)
  const get = api.get
  api.get = (async (url: string, options?: unknown) => {
    if (url === '/api/store/config') {
      return result({
        store_catalogue_supported: true,
        store_collections_supported: true,
      })
    }
    if (url === '/api/store/favorites') {
      return result({ items: [], has_more: false })
    }
    return get(url, options as never)
  }) as typeof api.get
  const cartWrites: unknown[] = []
  api.put = (async (url: string, body: unknown) => {
    cartWrites.push({ url, body })
    return result(null)
  }) as typeof api.put
  const variant = {
    id: 'sku-blue',
    name: 'Blue size',
    enabled: true,
    price_quota: 500000,
    sale_available: 5,
    available_stock: 5,
    template: 'card-key' as const,
    trading_paused: false,
    product_id: product.id,
    created_at: 1,
    updated_at: 1,
    is_default: true,
    inventory_total: 5,
    inventory_available: 5,
    reserved_stock: 0,
  }
  const fresh = {
    ...product,
    variants: [variant],
    default_variant_id: variant.id,
  }
  await mount(
    <StoreCheckout
      product={fresh}
      initialVariantId={variant.id}
      initialQuantity={3}
      cartItemId='cart-item-1'
    />,
    true
  )
  const href = [...document.querySelectorAll<HTMLAnchorElement>('a')]
    .find((link) => link.textContent === 'Sign in to buy')
    ?.getAttribute('href')
  assert.ok(href)
  const redirect = new URL(href, 'https://shop.example.test').searchParams.get(
    'redirect'
  )
  assert.ok(redirect)
  const selection = new URL(redirect, 'https://shop.example.test')
  assert.equal(selection.pathname, `/store/products/${product.id}`)
  assert.equal(selection.searchParams.get('variant_id'), variant.id)
  assert.equal(selection.searchParams.get('quantity'), '3')
  assert.equal(selection.searchParams.get('cart_item_id'), 'cart-item-1')
  await remount(<StoreCheckout product={fresh} />)
  assert.equal(
    document.querySelector<HTMLInputElement>(
      'input[name="store-variant"]:checked'
    )?.value,
    variant.id
  )
  assert.equal(button('Place order').disabled, false)
  for (const linkedId of ['disabled-sku', 'deleted-sku']) {
    await remount(
      <StoreCheckout
        product={{
          ...fresh,
          variants: [
            { ...variant, id: 'disabled-sku', enabled: false },
            variant,
          ],
        }}
        initialVariantId={linkedId}
        initialQuantity={2}
        cartItemId='stale-cart-item'
      />
    )
    assert.equal(
      document.querySelector('input[name="store-variant"]:checked'),
      null
    )
    assert.match(
      document.body.textContent || '',
      /Choose a variant before ordering/
    )
    assert.equal(button('Place order').disabled, true)
    assert.equal(button('Add to cart').disabled, true)
    await click(button('Place order'))
    await click(button('Add to cart'))
    assert.equal(writes.length, 0)
    assert.equal(cartWrites.length, 0)
  }
  const choice = document.querySelector<HTMLInputElement>(
    `input[name="store-variant"][value="${variant.id}"]`
  )
  assert.ok(choice)
  await click(choice)
  assert.equal(choice.checked, true)
  assert.equal(button('Place order').disabled, false)
  assert.equal(button('Add to cart').disabled, false)
  await click(button('Place order'))
  const create = writes.find((write) => write.url === '/api/store/orders')
  assert.ok(create)
  assert.equal(create.body.variant_id, variant.id)
})

test('a guest free offer uses the authenticated guest quote and dedicated create route; anonymous false is never promoted locally', async () => {
  const { writes } = mockAccess()
  const get = api.get
  const post = api.post
  const quotes: Array<Record<string, unknown> | undefined> = []
  api.get = (async (url: string, options?: unknown) =>
    url.endsWith('/promotions/resolve')
      ? result({
          product_id: product.id,
          code: 'GIFT',
          discount_bps: 10000,
          variant_ids: [],
          expires_at: null,
          status: 'active',
        })
      : get(url, options as never)) as typeof api.get
  api.post = (async (
    url: string,
    body: Record<string, unknown>,
    options?: Record<string, unknown>
  ) => {
    if (url.endsWith('/promotions/quote')) {
      quotes.push(options)
      const allowed =
        (options?.headers as Record<string, unknown> | undefined)?.[
          'X-Store-Guest'
        ] === guestSession.token
      return result({
        product_id: product.id,
        variant_id: '',
        quantity: body.quantity,
        promotion_code: 'GIFT',
        discount_bps: 10000,
        original_price_quota: 500000,
        discount_quota: 500000,
        price_quota: 0,
        free: true,
        checkout_allowed: allowed,
        max_quantity: allowed ? 1 : 0,
        payment_methods: ['free'],
      })
    }
    return post(url, body, options)
  }) as typeof api.post
  await mount(
    <StoreCheckout product={guestProduct} promotionCode='GIFT' />,
    true
  )
  await act(flush)
  assert.ok(
    quotes.some(
      (options) =>
        (options?.headers as Record<string, unknown> | undefined)?.[
          'X-Store-Guest'
        ] === guestSession.token
    )
  )
  assert.equal(button('Free claim').disabled, false)
  await click(button('Free claim'))
  const termsCheckbox = document.querySelector<HTMLElement>('[role="checkbox"]')
  assert.ok(termsCheckbox)
  await click(termsCheckbox)
  await click(button('Agree and place order'))
  const created = writes.find(
    (write) => write.url === '/api/store/guest/orders'
  )
  assert.ok(created)
  assert.equal(created.body.payment_method, 'free')
  assert.equal(created.body.promotion_code, 'GIFT')
  assert.equal(
    writes.some((write) => write.url === '/api/store/orders'),
    false
  )
  assert.equal(
    (localStorage.getItem('lmm:store:checkout-intents') || '').includes(
      guestSession.token
    ),
    false
  )
})

test('an account switch before a late create response keeps the original actor receipt private', async () => {
  mockAccess(false)
  const post = api.post
  let finish: ((value: unknown) => void) | undefined
  api.post = ((
    url: string,
    body: Record<string, unknown>,
    options?: Record<string, unknown>
  ) => {
    if (url !== '/api/store/orders') return post(url, body, options)
    return new Promise<unknown>((resolve) => {
      finish = resolve
    })
  }) as typeof api.post
  await mount(<StoreCheckout product={product} />)
  await click(button('Place order'))
  assert.ok(finish)
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 3, role: 1, username: 'buyer-3' })
    finish?.(
      result({
        order: {
          ...order,
          id: 'old-account-order',
          buyer_id: 2,
          payment_method: 'balance',
          status: 'paid',
        },
        created: true,
      })
    )
    await flush()
  })
  assert.equal(document.body.textContent?.includes('Order paid'), false)
  assert.equal(document.body.textContent?.includes('old-account-order'), false)
  const records = JSON.parse(
    localStorage.getItem('lmm:store:checkout-intents') || '{}'
  ).records
  assert.equal(records[0].actor, 'account:2')
  assert.equal(records[0].orderId, 'old-account-order')
})

test('switching accounts after a paid receipt does not rebind the previous order to the new buyer', async () => {
  mockAccess(false)
  await mount(<StoreCheckout product={product} />)
  await click(button('Place order'))
  assert.match(document.body.textContent || '', /Order paid/)
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 3, role: 1, username: 'buyer-3' })
    await flush()
  })
  assert.equal(document.body.textContent?.includes('Order paid'), false)
  assert.equal(document.body.textContent?.includes(order.trade_no), false)
  assert.equal(
    JSON.parse(localStorage.getItem('lmm:store:checkout-intents') || '{}')
      .records[0].actor,
    'account:2'
  )
})

test('only an exact authoritative precreate terms rejection releases its key; new terms require a fresh checkbox and explicit create', async () => {
  mockAccess()
  const get = api.get
  const post = api.post
  let terms = { ...sellerTerms }
  const requests: Record<string, unknown>[] = []
  api.get = (async (url: string, options?: unknown) =>
    url.endsWith('/terms')
      ? result(terms)
      : get(url, options as never)) as typeof api.get
  api.post = (async (
    url: string,
    body: Record<string, unknown>,
    options?: unknown
  ) => {
    if (url !== '/api/store/orders') return post(url, body, options as never)
    requests.push(body)
    if (requests.length === 1) {
      terms = {
        ...sellerTerms,
        version: '22222222-2222-2222-2222-222222222222',
        content: 'Updated actual seller terms.',
      }
      throw {
        response: {
          status: 409,
          data: {
            success: false,
            code: 'STORE_TERMS_UPDATED',
            request_key: body.request_key,
            order_created: false,
          },
        },
      }
    }
    return post(url, body, options as never)
  }) as typeof api.post
  await mount(<StoreCheckout product={product} />)
  await agreeTerms()
  await click(button('Agree and place order'))
  assert.equal(requests.length, 1)
  assert.equal(
    JSON.parse(localStorage.getItem('lmm:store:checkout-intents') || '{}')
      .records.length,
    0
  )
  assert.match(document.body.textContent || '', /Updated actual seller terms/)
  assert.equal(button('Agree and place order').disabled, true)
  const checkbox = document.querySelector<HTMLElement>('[role="checkbox"]')
  assert.ok(checkbox)
  assert.equal(checkbox.getAttribute('aria-checked'), 'false')
  await click(checkbox)
  assert.equal(requests.length, 1)
  await click(button('Agree and place order'))
  assert.equal(requests.length, 2)
  assert.notEqual(requests[0].request_key, requests[1].request_key)
  assert.equal(requests[1].seller_terms_version, terms.version)
  assert.equal(requests[1].accept_seller_terms, true)
})

test('a plain terms conflict never releases an unknown checkout or creates a replacement request', async () => {
  mockAccess()
  const post = api.post
  let calls = 0
  api.post = (async (
    url: string,
    body: Record<string, unknown>,
    options?: unknown
  ) => {
    if (url !== '/api/store/orders') return post(url, body, options as never)
    calls++
    throw {
      response: {
        status: 409,
        data: {
          success: false,
          code: 'STORE_SELLER_TERMS_REQUIRED',
          request_key: body.request_key,
        },
      },
    }
  }) as typeof api.post
  await mount(<StoreCheckout product={product} />)
  await agreeTerms()
  await click(button('Agree and place order'))
  assert.equal(calls, 1)
  const records = JSON.parse(
    localStorage.getItem('lmm:store:checkout-intents') || '{}'
  ).records
  assert.equal(records.length, 1)
  assert.equal(records[0].state, 'unknown')
  assert.equal(button('Place order').disabled, true)
})

test('removing available inventory requires exact variant and item confirmation; cancelling sends no delete and retained stock has no remove action', async () => {
  mockAccess()
  const variant = {
    id: 'sku-stock',
    product_id: product.id,
    name: 'Seller custom specification',
    price_quota: 500000,
    template: 'card-key' as const,
    enabled: true,
    created_at: 1,
    updated_at: 1,
    is_default: true,
    inventory_total: 3,
    inventory_available: 1,
    reserved_stock: 1,
    sale_available: 1,
    trading_paused: false,
  }
  let items = [
    {
      id: 'stock-available',
      product_id: product.id,
      variant_id: variant.id,
      state: 'available',
      created_at: 1,
    },
    {
      id: 'stock-reserved',
      product_id: product.id,
      variant_id: variant.id,
      state: 'reserved',
      created_at: 1,
    },
    {
      id: 'stock-delivered',
      product_id: product.id,
      variant_id: variant.id,
      state: 'delivered',
      created_at: 1,
    },
  ]
  const get = api.get
  api.get = (async (url: string, options?: unknown) =>
    url.endsWith('/inventory')
      ? result({ items, offset: 0, limit: 20, has_more: false })
      : get(url, options as never)) as typeof api.get
  const deletions: string[] = []
  api.delete = (async (url: string) => {
    deletions.push(url)
    items = items.filter((item) => item.id !== 'stock-available')
    return result(null)
  }) as typeof api.delete
  await mount(
    <StoreInventoryImport
      product={{
        ...product,
        variants: [variant],
        default_variant_id: variant.id,
      }}
      onClose={() => undefined}
      onSaved={async () => undefined}
    />
  )
  assert.equal(
    [...document.querySelectorAll('button')].filter(
      (node) => node.textContent?.trim() === 'Remove'
    ).length,
    1
  )
  await click(button('Remove'))
  assert.equal(deletions.length, 0)
  let confirm = document.querySelector<HTMLElement>('[role="alertdialog"]')
  assert.ok(confirm)
  assert.match(confirm.textContent || '', /Seller custom specification/)
  assert.match(confirm.textContent || '', /stock-available/)
  assert.equal(confirm.textContent?.includes('stock-reserved'), false)
  const cancel = [
    ...confirm.querySelectorAll<HTMLButtonElement>('button'),
  ].find((node) => node.textContent?.trim() === 'Cancel')
  assert.ok(cancel)
  await click(cancel)
  assert.equal(deletions.length, 0)
  await click(button('Remove'))
  confirm = document.querySelector<HTMLElement>('[role="alertdialog"]')
  assert.ok(confirm)
  const remove = [
    ...confirm.querySelectorAll<HTMLButtonElement>('button'),
  ].find((node) => node.textContent?.trim() === 'Remove')
  assert.ok(remove)
  await click(remove)
  assert.deepEqual(deletions, [
    `/api/store/products/${product.id}/variants/${variant.id}/inventory/stock-available`,
  ])
})

test('real axios interceptor refuses a prepared guest request after member login before any HTTP adapter runs', async () => {
  useAuthStore.getState().auth.reset()
  const { storeGuestRequestOptions } = await import('./access-api')
  const options = storeGuestRequestOptions(guestSession.token)
  let transmitted = 0
  api.defaults.adapter = async (config) => {
    transmitted++
    return {
      data: result(null).data,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const pending = originalPost(
    '/api/store/guest/orders',
    { product_id: product.id },
    options
  )
  useAuthStore
    .getState()
    .auth.setUser({ id: 88, role: 1, username: 'switched' })
  await assert.rejects(pending, /Authentication changed before request/)
  assert.equal(transmitted, 0)
})
test('real axios interceptor refuses a captured member checkout after an account switch before sending', async () => {
  useAuthStore.getState().auth.reset()
  useAuthStore.getState().auth.setUser({ id: 2, role: 1, username: 'buyer' })
  let transmitted = 0
  api.defaults.adapter = async (config) => {
    transmitted++
    return {
      data: result(null).data,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const pending = originalPost(
    '/api/store/orders',
    { product_id: product.id },
    {
      skipAuthRefresh: true,
      skipBusinessError: true,
      skipErrorHandler: true,
      authScope: { userId: 2, sessionId: undefined },
    }
  )
  useAuthStore
    .getState()
    .auth.setUser({ id: 3, role: 1, username: 'other-buyer' })
  await assert.rejects(pending, /Authentication changed before request/)
  assert.equal(transmitted, 0)
})

test('guest history reads saved IDs with private credentials and reveals pickup proof only after an explicit read', async () => {
  mockAccess()
  sessionStorage.setItem(
    'lmm.store.guest-session.v1',
    JSON.stringify(guestSession)
  )
  sessionStorage.setItem(
    'lmm.store.guest-order-ids.v1',
    JSON.stringify({ [guestSession.guest_id]: [order.id] })
  )
  api.get = (async (url: string, options?: Record<string, unknown>) => {
    assert.equal(
      (options?.headers as Record<string, unknown>)?.['X-Store-Guest'],
      guestSession.token
    )
    if (url === `/api/store/guest/orders/${order.id}`) {
      return result({ ...order, status: 'paid' })
    }
    if (url.endsWith('/pickup-link')) {
      return result({
        pickup_url:
          'https://shop.example.test/store/claim/guest-order?token=private-order-proof',
      })
    }
    throw new Error(`Unexpected guest read ${url}`)
  }) as typeof api.get
  await mount(<StoreGuestOrders />, true)
  assert.match(document.body.textContent || '', /Frozen variant/)
  assert.equal(document.querySelector('a[href*="private-order-proof"]'), null)
  await click(button('Get pickup link'))
  assert.ok(document.querySelector('a[href*="private-order-proof"]'))
  const cache = JSON.stringify(
    client
      ?.getQueryCache()
      .getAll()
      .map((query) => query.state.data)
  )
  assert.equal(cache.includes(guestSession.token), false)
  assert.equal(cache.includes('private-order-proof'), false)
})
test('guest payment recovery reconciles the original pending orders and refreshes only authoritative server status', async () => {
  const { writes } = mockAccess()
  sessionStorage.setItem(
    'lmm.store.guest-session.v1',
    JSON.stringify(guestSession)
  )
  const currentOrders = [
    { ...order, id: 'pending-epay', payment_issued: true },
    {
      ...order,
      id: 'reconciliation-ldc',
      status: 'reconciliation_pending',
      payment_method: 'platform:linuxdo',
      payment_issued: true,
    },
  ]
  sessionStorage.setItem(
    'lmm.store.guest-order-ids.v1',
    JSON.stringify({
      [guestSession.guest_id]: currentOrders.map(({ id }) => id),
    })
  )
  const reads: string[] = []
  let deferNextRead = false
  let releaseRefresh: () => void = () => {
    assert.fail('Refresh must be deferred first')
  }
  const blockedRefresh = new Promise<void>((resolve) => {
    releaseRefresh = resolve
  })
  api.get = (async (url: string, options?: Record<string, unknown>) => {
    assert.equal(
      (options?.headers as Record<string, unknown>)?.['X-Store-Guest'],
      guestSession.token
    )
    reads.push(url)
    const found = currentOrders.find(
      ({ id }) => url === `/api/store/guest/orders/${id}`
    )
    assert.ok(found, `read only the original guest order: ${url}`)
    const snapshot = { ...found }
    if (deferNextRead) {
      deferNextRead = false
      await blockedRefresh
    }
    return result(snapshot)
  }) as typeof api.get
  let checks = 0
  api.post = (async (
    url: string,
    body: Record<string, unknown>,
    options?: Record<string, unknown>
  ) => {
    writes.push({ url, body, options })
    assert.deepEqual(body, {})
    const config = options as {
      headers: Record<string, unknown>
      authScope: { userId: number | undefined; sessionId: string | undefined }
      skipAuthRefresh: boolean
    }
    assert.equal(config.headers['X-Store-Guest'], guestSession.token)
    assert.equal(config.authScope.userId, undefined)
    assert.equal(config.skipAuthRefresh, true)
    const found = currentOrders.find(
      ({ id }) => url === `/api/store/guest/orders/${id}/reconcile`
    )
    assert.ok(found, `reconcile only the original guest order: ${url}`)
    // A successful query can still be pending. The UI must not assume paid.
    checks++
    if (checks > 1) found.status = 'paid'
    return result(found)
  }) as typeof api.post
  await mount(<StoreGuestOrders />, true)
  const rows = () => [...document.querySelectorAll('article')]
  const check = (index: number) => {
    const node = [...rows()[index].querySelectorAll('button')].find(
      (item) => item.textContent?.trim() === 'Check payment status'
    )
    assert.ok(node)
    return node
  }
  await click(check(0))
  assert.equal(rows()[0].textContent?.includes('Get pickup link'), false)
  assert.ok(rows()[0].textContent?.includes('pending'))
  // An older refresh must finish before the fresh read requested by reconcile.
  deferNextRead = true
  await click(button('Refresh'))
  await click(check(0))
  assert.equal(rows()[0].textContent?.includes('Get pickup link'), false)
  await act(async () => {
    releaseRefresh()
    await flush()
  })
  await act(flush)
  assert.ok(rows()[0].textContent?.includes('Get pickup link'))
  assert.equal(rows()[0].textContent?.includes('Check payment status'), false)
  await click(check(1))
  assert.ok(rows()[1].textContent?.includes('Get pickup link'))
  assert.deepEqual(
    writes.map(({ url }) => url),
    [
      '/api/store/guest/orders/pending-epay/reconcile',
      '/api/store/guest/orders/pending-epay/reconcile',
      '/api/store/guest/orders/reconciliation-ldc/reconcile',
    ]
  )
  assert.equal(
    reads.length,
    10,
    'reconcile queues a fresh read after the older refresh'
  )
  assert.equal(
    localStorage.getItem('lmm:store:checkout-intents'),
    null,
    'recovery never creates a purchase intent'
  )
  assert.equal(
    JSON.stringify(client?.getQueryCache().getAll()).includes(
      guestSession.token
    ),
    false
  )
})
test('private owner purchases are legitimate without relying on the old preview-only condition', async () => {
  mockAccess()
  await mount(
    <StoreCheckout
      product={{
        ...product,
        seller_id: 2,
        visibility: 'private',
        status: 'draft',
      }}
    />
  )
  assert.equal(button('Place order').disabled, false)
  await agreeTerms()
  assert.equal(button('Agree and place order').disabled, false)
})

test('zero stock disables consistent quantity controls and fresh restocking unlocks them without an account reload', async () => {
  mockAccess(false)
  const { useState } = await import('react')
  function Stock() {
    const [count, setCount] = useState(0)
    return (
      <>
        <button type='button' onClick={() => setCount(3)}>
          Restock fixture
        </button>
        <StoreCheckout product={{ ...product, available_stock: count }} />
      </>
    )
  }
  await mount(<Stock />)
  assert.equal(field('store-quantity').disabled, true)
  assert.ok(
    Number(field('store-quantity').getAttribute('aria-valuemax')) >=
      Number(field('store-quantity').getAttribute('aria-valuemin'))
  )
  assert.equal(button('Place order').disabled, true)
  await click(button('Restock fixture'))
  assert.equal(field('store-quantity').disabled, false)
  assert.equal(button('Place order').disabled, false)
  const increase = document.querySelector<HTMLButtonElement>(
    'button[aria-label="Increase quantity"]'
  )
  assert.ok(increase)
  assert.equal(increase.disabled, false)
})

test('guest history looks up an unknown saved request under its original guest identity without creating another order', async () => {
  const { writes } = mockAccess()
  sessionStorage.setItem(
    'lmm.store.guest-session.v1',
    JSON.stringify(guestSession)
  )
  useAuthStore.getState().auth.setUser(null)
  const { createStoreCheckoutIntentJournal } = await import('./checkout-intent')
  const { isStoreCheckoutActorCurrent } = await import('./checkout-recovery')
  const journal = createStoreCheckoutIntentJournal({
    isActorCurrent: isStoreCheckoutActorCurrent,
  })
  const prepared = await journal.prepare(
    { kind: 'guest', guestId: guestSession.guest_id },
    { productId: product.id, quantity: 1 },
    { product_id: product.id, quantity: 1, payment_method: 'external:epay' }
  )
  const post = api.post
  api.post = (async (
    url: string,
    body: Record<string, unknown>,
    options?: Record<string, unknown>
  ) => {
    if (url !== '/api/store/guest/orders/lookup') {
      return post(url, body, options)
    }
    writes.push({ url, body, options })
    assert.equal(body.request_key, prepared.record.requestKey)
    return result(order)
  }) as typeof api.post
  await mount(<StoreGuestOrders lookupSupported />, true)
  assert.match(document.body.textContent || '', /Frozen variant/)
  assert.equal(
    writes.some((item) => item.url === '/api/store/guest/orders'),
    false
  )
  const lookup = writes.find((item) => item.url.endsWith('/lookup'))
  assert.ok(lookup)
  assert.equal(
    (lookup.options?.headers as Record<string, unknown> | undefined)?.[
      'X-Store-Guest'
    ],
    guestSession.token
  )
  assert.equal(
    (localStorage.getItem('lmm:store:checkout-intents') || '').includes(
      guestSession.token
    ),
    false
  )
})
